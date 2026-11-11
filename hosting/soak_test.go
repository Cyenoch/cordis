package hosting

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

// Callers keep calling a service and reading a stream while their providers
// are disabled and enabled again. A caller may be told the provider is gone;
// it is never left hanging, and every activation that started is cleaned up.
func TestCallsAndStreamsWhileProvidersComeAndGo(t *testing.T) {
	f := newFixture(t)
	var produced atomic.Int64
	f.pack(echoManifest("test.provider", true), sdk.Define(f.echoPlugin("main", "> ")))
	f.pack(countManifest("test.counter", true), sdk.Define(f.counter("main", &produced)))
	f.open()
	entries := []contract.Entry{{ID: "provider", Package: "test.provider"}, {ID: "counter", Package: "test.counter"}}
	for _, entry := range entries {
		must(t, f.enable(entry).Wait(f.ctx()))
	}

	// What a caller may see when its provider goes away under it.
	expected := func(err error) bool {
		_, coded := errors.AsType[*contract.Error](err)
		return coded || errors.Is(err, cordis.ErrUnavailable) || errors.Is(err, cordis.ErrInactive) ||
			errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	}
	ctx, stop := context.WithTimeout(t.Context(), 30*time.Second)
	defer stop()
	churning, cancel := context.WithCancel(ctx)
	var succeeded, turnedAway atomic.Int64
	var callers sync.WaitGroup
	for worker := range 12 {
		callers.Go(func() {
			random := rand.New(rand.NewPCG(uint64(worker), 11))
			for churning.Err() == nil {
				var err error
				if random.IntN(2) == 0 {
					_, err = f.host.Call(ctx, contract.Requirement{Name: "test.echo", Major: 1, MaxMinor: 9}, "echo", []byte(`{"text":"x"}`))
				} else if provider, getErr := countKey.Get(f.host.Scope()); getErr != nil {
					err = getErr
				} else {
					limit := random.IntN(40)
					for _, itemErr := range contract.InvokeStream[countRequest, string](ctx, provider, "count", countRequest{Upto: 60}) {
						if err = itemErr; err != nil {
							break
						}
						if limit--; limit < 0 {
							break
						}
					}
				}
				switch {
				case err == nil:
					succeeded.Add(1)
				case expected(err):
					turnedAway.Add(1)
				default:
					t.Errorf("worker %d: unexpected %T: %v", worker, err, err)
					return
				}
			}
		})
	}
	for round := range 12 {
		entry := entries[round%len(entries)]
		must(t, f.host.Disable(ctx, entry.ID))
		instance, err := f.host.Enable(ctx, entry)
		must(t, err)
		must(t, instance.Wait(ctx))
	}
	cancel()
	callers.Wait()
	if ctx.Err() != nil {
		t.Fatal("callers or the provider churn did not finish in time")
	}
	t.Logf("%d calls succeeded, %d were turned away", succeeded.Load(), turnedAway.Load())
	if succeeded.Load() == 0 {
		t.Fatal("no call ever succeeded")
	}

	// Both providers are back and answer.
	out, err := f.host.Call(f.ctx(), contract.Requirement{Name: "test.echo", Major: 1, MaxMinor: 9}, "echo", []byte(`{"text":"x"}`))
	if err != nil || string(out) != `{"text":"\u003e x"}` {
		t.Fatalf("call after the churn = %s, %v", out, err)
	}
	provider, err := countKey.Get(f.host.Scope())
	must(t, err)
	if items, err := collect(contract.InvokeStream[countRequest, string](f.ctx(), provider, "count", countRequest{Upto: 5})); err != nil || len(items) != 5 {
		t.Fatalf("stream after the churn = %v, %v", items, err)
	}
	// Every activation of the echo provider but the running one was stopped.
	eventually(t, "stopped activations to be cleaned up", func() bool {
		return f.log.count("start main")-f.log.count("stop main") == 1
	})
}

// A caller whose provider stopped under it can tell that from a failure of
// the call itself: the error always has a code.
func TestProviderGoneIsACodedError(t *testing.T) {
	r := &run{identity: contract.Identity{Entry: "provider"}}
	if err := r.gone(io.EOF); !errors.Is(err, contract.Fail(contract.NotReady, "")) {
		t.Fatalf("after an ordinary stop: %v", err)
	}
	r.failure = contract.Fail(contract.PluginFailed, "crashed")
	if err := r.gone(io.EOF); !errors.Is(err, contract.Fail(contract.PluginFailed, "")) {
		t.Fatalf("after a failure: %v", err)
	}
}

// The host chooses how many requests a session carries at once; the guest
// never sees more of them, and callers beyond the limit wait their turn.
func TestInflightLimitIsTheHostsChoice(t *testing.T) {
	for _, limit := range []int{0, contract.MinInflight, 200} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			usable := limit - 8 // the session keeps eight for its own control
			if limit == 0 {
				usable = contract.DefaultInflight - 8
			}
			f := newFixture(t)
			var entered, most atomic.Int64
			release := make(chan struct{})
			f.pack(echoManifest("test.provider", true), sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
				return echoKey.Provide(s, contract.Methods(echoService, map[string]contract.Handler{
					"echo": contract.Typed(func(ctx context.Context, in echoRequest) (echoResponse, error) {
						now := entered.Add(1)
						defer entered.Add(-1)
						for seen := most.Load(); now > seen && !most.CompareAndSwap(seen, now); seen = most.Load() {
						}
						select {
						case <-release:
						case <-ctx.Done():
						}
						return echoResponse{Text: in.Text}, ctx.Err()
					}),
				}))
			}}))
			f.open(func(o *Options) { o.Inflight = limit })
			must(t, f.enable(contract.Entry{ID: "provider", Package: "test.provider"}).Wait(f.ctx()))

			var callers sync.WaitGroup
			failures := make(chan error, usable+4)
			for range usable + 4 {
				callers.Go(func() {
					if _, err := f.host.Call(f.ctx(), contract.Requirement{Name: "test.echo", Major: 1, MaxMinor: 9}, "echo", []byte(`{"text":"x"}`)); err != nil {
						failures <- err
					}
				})
			}
			eventually(t, "the session to fill", func() bool { return entered.Load() == int64(usable) })
			close(release)
			callers.Wait()
			close(failures)
			for err := range failures {
				t.Errorf("a caller beyond the limit failed instead of waiting: %v", err)
			}
			if got := most.Load(); got != int64(usable) {
				t.Fatalf("the guest saw %d requests at once, want %d", got, usable)
			}
		})
	}
	f := newFixture(t)
	f.pack(echoManifest("test.provider", true), sdk.Define(f.echoPlugin("main", "")))
	f.open(func(o *Options) { o.Inflight = contract.MaxInflight + 1 })
	if err := f.enable(contract.Entry{ID: "provider", Package: "test.provider"}).Wait(f.ctx()); !errors.Is(err, contract.Fail(contract.Invalid, "")) {
		t.Fatalf("a limit above the maximum = %v", err)
	}
}

// An application counts and times calls through OnCall; it sees who was
// called and how the call ended, never what was said.
func TestCallsAreReportedToTheObserver(t *testing.T) {
	f := newFixture(t)
	var produced atomic.Int64
	f.pack(echoManifest("test.provider", true), sdk.Define(f.echoPlugin("main", "")))
	f.pack(countManifest("test.counter", true), sdk.Define(f.counter("main", &produced)))
	var mu sync.Mutex
	var records []CallRecord
	f.open(func(o *Options) {
		o.OnCall = func(record CallRecord) {
			mu.Lock()
			records = append(records, record)
			mu.Unlock()
		}
	})
	provider := f.enable(contract.Entry{ID: "provider", Package: "test.provider"})
	must(t, provider.Wait(f.ctx()))
	must(t, f.enable(contract.Entry{ID: "counter", Package: "test.counter"}).Wait(f.ctx()))
	echo := contract.Requirement{Name: "test.echo", Major: 1, MaxMinor: 9}
	_, err := f.host.Call(f.ctx(), echo, "echo", []byte(`{"text":"x"}`))
	must(t, err)
	if _, err := f.host.Call(f.ctx(), echo, "missing", []byte(`{}`)); err == nil {
		t.Fatal("an unknown method succeeded")
	}
	counter, err := countKey.Get(f.host.Scope())
	must(t, err)
	if items, err := collect(contract.InvokeStream[countRequest, string](f.ctx(), counter, "count", countRequest{Upto: 5})); err != nil || len(items) != 5 {
		t.Fatalf("stream = %v, %v", items, err)
	}
	if _, err := collect(contract.InvokeStream[countRequest, string](f.ctx(), counter, "count", countRequest{Upto: 5, FailAt: 2})); err == nil {
		t.Fatal("a failing stream ended cleanly")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(records) != 4 {
		t.Fatalf("records = %+v", records)
	}
	ok, failed, stream, broken := records[0], records[1], records[2], records[3]
	if ok.Entry != "provider" || ok.Activation == "" || ok.Service.Name != "test.echo" || ok.Method != "echo" || ok.Stream || ok.Err != nil || ok.Duration <= 0 {
		t.Errorf("successful call = %+v", ok)
	}
	if failed.Method != "missing" || !errors.Is(failed.Err, contract.Fail(contract.Invalid, "")) {
		t.Errorf("failed call = %+v", failed)
	}
	if stream.Entry != "counter" || !stream.Stream || stream.Items != 5 || stream.Err != nil {
		t.Errorf("stream = %+v", stream)
	}
	if !broken.Stream || broken.Items != 2 || !errors.Is(broken.Err, contract.Fail(contract.Denied, "")) {
		t.Errorf("failing stream = %+v", broken)
	}
}
