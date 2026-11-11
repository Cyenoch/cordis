package hosting

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

type countRequest struct {
	Upto   int  `json:"upto"`
	FailAt int  `json:"failAt"`
	Big    bool `json:"big"`
}

var (
	countService = contract.Service{Name: "test.count", Major: 1}
	countKey     = contract.EndpointKey("test.count", 1)
)

// counter provides test.count: a stream of the numbers below Upto.
func (f *fixture) counter(name string, produced *atomic.Int64) cordis.Plugin[struct{}] {
	return cordis.Plugin[struct{}]{Name: name, Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		return countKey.Provide(s, contract.ServiceMethods{Provides: countService, Streams: map[string]contract.StreamHandler{
			"count": contract.TypedStream(func(ctx context.Context, in countRequest) iter.Seq2[string, error] {
				return func(yield func(string, error) bool) {
					defer func() { f.log.add(fmt.Sprintf("%s ended: %v", name, ctx.Err())) }()
					for n := range in.Upto {
						if in.FailAt > 0 && n == in.FailAt {
							yield("", contract.Fail(contract.Denied, "no more"))
							return
						}
						produced.Add(1)
						item := fmt.Sprint(n)
						if in.Big {
							item = strings.Repeat("x", contract.MinFrameBytes)
						}
						if !yield(item, nil) {
							return
						}
					}
				}
			}),
		}})
	}}
}

func countManifest(id string, provides bool, requires ...contract.Consumer) contract.Manifest {
	definition := contract.DefinitionSpec{ID: "main", Requires: requires}
	if provides {
		definition.Provides = []contract.Service{countService}
	}
	manifest := contract.Manifest{ID: id, Definitions: []contract.DefinitionSpec{definition}}
	manifest.Protocol = contract.Protocol{Major: contract.ProtocolMajor, RequiredFeatures: []string{"streams"}}
	return manifest
}

func collect(seq iter.Seq2[string, error]) (items []string, err error) {
	for item, itemErr := range seq {
		if itemErr != nil {
			return items, itemErr
		}
		items = append(items, item)
	}
	return items, nil
}

func TestStreamsAcrossTheSessionAndInProcess(t *testing.T) {
	for _, native := range []bool{true, false} {
		t.Run(fmt.Sprintf("native=%v", native), func(t *testing.T) {
			f := newFixture(t)
			var produced atomic.Int64
			f.pack(countManifest("test.counter", true), sdk.Define(f.counter("main", &produced)))
			f.open(func(o *Options) { o.FrameBytes = contract.MinFrameBytes })
			if native {
				must(t, f.enable(contract.Entry{ID: "counter", Package: "test.counter"}).Wait(f.ctx()))
			} else {
				must(t, f.counter("main", &produced).Load(f.host.Scope(), struct{}{}).Wait(f.ctx()))
			}
			provider, err := countKey.Get(f.host.Scope())
			must(t, err)
			count := func(ctx context.Context, request countRequest) iter.Seq2[string, error] {
				return contract.InvokeStream[countRequest, string](ctx, provider, "count", request)
			}

			// Item by item, in order.
			items, err := collect(count(f.ctx(), countRequest{Upto: 40}))
			if err != nil || len(items) != 40 || items[39] != "39" {
				t.Fatalf("items %v, error %v", items, err)
			}

			// A paused consumer holds the producer back.
			produced.Store(0)
			next, stop := iter.Pull2(count(f.ctx(), countRequest{Upto: 1000}))
			if item, err, ok := next(); !ok || err != nil || item != "0" {
				t.Fatalf("first item = %q, %v", item, err)
			}
			if native {
				eventually(t, "the producer to fill the window", func() bool { return produced.Load() >= 16 })
				if got := produced.Load(); got > 20 {
					t.Fatalf("the producer ran %d items ahead", got)
				}
			}
			// Stopping early cancels the producer.
			stop()
			eventually(t, "the producer to end", func() bool { return f.log.count("main ended: context canceled") >= 1 || !native })

			// What was produced precedes the error.
			items, err = collect(count(f.ctx(), countRequest{Upto: 10, FailAt: 3}))
			if !slices.Equal(items, []string{"0", "1", "2"}) || !errors.Is(err, contract.Fail(contract.Denied, "")) {
				t.Fatalf("items %v, error %v", items, err)
			}

			// A canceled consumer.
			ctx, cancel := context.WithCancel(f.ctx())
			seen := 0
			for _, err := range count(ctx, countRequest{Upto: 1000}) {
				if err != nil {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("a canceled stream ended with %v", err)
					}
					break
				}
				if seen++; seen == 3 {
					cancel()
				}
				if !native && ctx.Err() != nil {
					break // in process the consumer notices its own cancellation
				}
			}
			cancel()

			if native {
				// One item beyond the frame budget fails that stream only.
				if _, err := collect(count(f.ctx(), countRequest{Upto: 1, Big: true})); err == nil {
					t.Fatal("an oversized item was delivered")
				}
				if items, err := collect(count(f.ctx(), countRequest{Upto: 2})); err != nil || len(items) != 2 {
					t.Fatalf("the session did not survive: %v, %v", items, err)
				}
				if f.state("counter") != contract.Active {
					t.Fatalf("the provider is %s", f.state("counter"))
				}
			}
		})
	}
}

func TestGuestConsumesAStreamOfTheHost(t *testing.T) {
	f := newFixture(t)
	var produced atomic.Int64
	result := make(chan []string, 1)
	consumer := cordis.Plugin[struct{}]{Name: "main", Requires: []cordis.Requirement{contract.Require(countKey, 0, 0)},
		Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			provider, err := countKey.Get(s)
			if err != nil {
				return err
			}
			s.Go(func(ctx context.Context) error {
				items, err := collect(contract.InvokeStream[countRequest, string](ctx, provider, "count", countRequest{Upto: 50}))
				if err != nil {
					return err
				}
				result <- items
				// A second stream that this instance leaves open when it stops.
				for range contract.InvokeStream[countRequest, string](ctx, provider, "count", countRequest{Upto: 1 << 30}) {
				}
				return nil
			})
			return nil
		}}
	requires := contract.Consumer{Requirement: contract.Requirement{Name: "test.count", Major: 1}, Contract: strings.Repeat("ab", 32)}
	f.pack(countManifest("test.consumer", false, requires), sdk.Define(consumer))
	f.open()
	provider := f.counter("provider", &produced).Load(f.host.Scope(), struct{}{})
	must(t, f.enable(contract.Entry{ID: "consumer", Package: "test.consumer"}).Wait(f.ctx()))
	if items := <-result; len(items) != 50 || items[49] != "49" {
		t.Fatalf("items = %v", items)
	}
	// Stopping the consumer ends its stream; the provider stays Active.
	must(t, f.host.Disable(f.ctx(), "consumer"))
	eventually(t, "the provider's stream to end", func() bool { return f.log.count("provider ended: context canceled") == 1 })
	if provider.Status().State != cordis.Active {
		t.Fatalf("provider = %+v", provider.Status())
	}
}

func TestStreamEndsWhenItsProviderGoesAway(t *testing.T) {
	f := newFixture(t)
	exit := make(chan struct{})
	var produced atomic.Int64
	slow := cordis.Plugin[struct{}]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		s.Go(func(ctx context.Context) error {
			select {
			case <-exit:
				return errors.New("provider crashed")
			case <-ctx.Done():
				return nil
			}
		})
		return f.counter("inner", &produced).Start(nil, s, struct{}{})
	}}
	f.pack(countManifest("test.counter", true), sdk.Define(slow))
	f.open()
	must(t, f.enable(contract.Entry{ID: "counter", Package: "test.counter"}).Wait(f.ctx()))
	provider, err := countKey.Get(f.host.Scope())
	must(t, err)
	var failure error
	for item, err := range contract.InvokeStream[countRequest, string](f.ctx(), provider, "count", countRequest{Upto: 1 << 30}) {
		if err != nil {
			failure = err
			break
		}
		if item == "5" {
			close(exit)
		}
	}
	if failure == nil || !strings.Contains(failure.Error(), `"counter"`) {
		t.Fatalf("a stream of a provider that went away ended with %v", failure)
	}
}

func TestPackageThatNeedsStreamsOnAHostWithoutThem(t *testing.T) {
	f := newFixture(t)
	var produced atomic.Int64
	f.pack(countManifest("test.counter", true), sdk.Define(cordis.Plugin[struct{}]{Name: "main",
		Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
			f.log.add("started")
			return f.counter("inner", &produced).Start(ctx, s, struct{}{})
		}}))
	f.open()
	previous := hostFeatures
	hostFeatures = []string{"cancel", "parent", "events", "children", "subscriptions"}
	defer func() { hostFeatures = previous }()
	err := f.enable(contract.Entry{ID: "counter", Package: "test.counter"}).Wait(f.ctx())
	if !errors.Is(err, contract.Fail(contract.Incompatible, "")) || !strings.Contains(err.Error(), "streams") || len(f.log.get()) != 0 {
		t.Fatalf("wait = %v, events %q", err, f.log.get())
	}
}
