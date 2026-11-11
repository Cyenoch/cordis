package hosting

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
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
	// A starved machine may take longer than the default second to answer a
	// cancellation; that is not what this test is about.
	f.runtime.Limits.Grace = 30 * time.Second
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

// A caller whose provider went away can tell that from a failure the
// provider answered with: it is not_ready however the entry went, and the
// reason is in the text.
func TestProviderGoneIsNotReady(t *testing.T) {
	r := &run{identity: contract.Identity{Entry: "provider"}}
	closed := &contract.ClosedError{Err: io.EOF}
	if err := r.gone(closed); !errors.Is(err, contract.Fail(contract.NotReady, "")) {
		t.Fatalf("after an ordinary stop: %v", err)
	}
	r.failure = contract.Fail(contract.PluginFailed, "crashed")
	if err := r.gone(closed); !errors.Is(err, contract.Fail(contract.NotReady, "")) || !strings.Contains(err.Error(), "crashed") {
		t.Fatalf("after a failure: %v", err)
	}
	answered := contract.Fail(contract.Denied, "no")
	if err := r.gone(answered); err != answered {
		t.Fatalf("an error the provider answered with: %v", err)
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
	if ok.Entry != "provider" || ok.Activation == "" || ok.Service.Name != "test.echo" || ok.Method != "echo" || ok.Stream || ok.Err != nil || ok.Duration < 0 {
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

// A replace its caller gives up on while the old version cleans up does not
// leave the entry in a state nothing can move it out of: once the cleanup
// has finished the entry can be restored.
func TestReplaceGivenUpOnLeavesTheEntryRestorable(t *testing.T) {
	f := newFixture(t)
	slow := sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		f.log.add("start")
		s.Defer(func(context.Context) error {
			f.log.add("stopping")
			time.Sleep(200 * time.Millisecond)
			f.log.add("stop")
			return nil
		})
		return nil
	}})
	manifest := plainManifest("test.slow")
	first := f.pack(manifest, slow)
	manifest.Version = "2.0.0"
	second := f.pack(manifest, slow)
	f.open()
	must(t, f.host.Select(f.ctx(), first))
	must(t, f.enable(contract.Entry{ID: "slow", Package: "test.slow"}).Wait(f.ctx()))

	impatient, cancel := context.WithCancel(f.ctx())
	go func() {
		for f.log.count("stopping") == 0 && impatient.Err() == nil {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	if err := f.host.Replace(impatient, second); err == nil {
		t.Fatal("a replace its caller gave up on reported success")
	}
	eventually(t, "the old version to finish cleaning up", func() bool { return f.log.count("stop") == 1 })
	eventually(t, "the entry to be restorable", func() bool {
		if _, err := f.host.Restore(f.ctx()); err != nil {
			return false
		}
		instance, err := f.host.Instance("slow")
		return err == nil && instance.Status().State == contract.Active
	})
	if f.log.count("start") != 2 {
		t.Fatalf("events = %q", f.log.get())
	}
}

// The limit binds the plugin's own requests too: a plugin that calls the
// host from many goroutines never has more of them open than the session
// carries, and none of them fails.
func TestInflightLimitBindsThePluginsRequests(t *testing.T) {
	const limit, callers = contract.MinInflight, 3 * contract.MinInflight
	f := newFixture(t)
	var entered, most atomic.Int64
	release := make(chan struct{})
	results := make(chan error, callers)
	f.pack(echoManifest("test.user", false, echoRequirement(0, 9)), sdk.Define(cordis.Plugin[struct{}]{Name: "main",
		Requires: []cordis.Requirement{contract.Require(echoKey, 0, 9)},
		Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			provider, err := echoKey.Get(s)
			if err != nil {
				return err
			}
			for range callers {
				s.Go(func(ctx context.Context) error {
					_, err := contract.Invoke[echoRequest, echoResponse](ctx, provider, "echo", echoRequest{Text: "x"})
					results <- err
					return nil
				})
			}
			return nil
		}}))
	f.open(func(o *Options) { o.Inflight = limit })
	must(t, echoKey.Provide(f.host.Scope(), contract.Methods(echoService, map[string]contract.Handler{
		"echo": contract.Typed(func(ctx context.Context, in echoRequest) (echoResponse, error) {
			now := entered.Add(1)
			defer entered.Add(-1)
			for seen := most.Load(); now > seen && !most.CompareAndSwap(seen, now); seen = most.Load() {
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return echoResponse(in), ctx.Err()
		}),
	})))
	must(t, f.enable(contract.Entry{ID: "user", Package: "test.user"}).Wait(f.ctx()))
	eventually(t, "the session to fill", func() bool { return entered.Load() == limit-8 })
	close(release)
	for range callers {
		if err := <-results; err != nil {
			t.Errorf("a request of the plugin beyond the limit failed instead of waiting: %v", err)
		}
	}
	if got := most.Load(); got != limit-8 {
		t.Fatalf("the host saw %d requests of the plugin at once, want %d", got, limit-8)
	}
}

// Subscribing and unsubscribing share the session with the plugin's calls.
// With every place for calls taken, any number of subscriptions cancelled at
// once still go through, in turn, and none is refused for lack of room.
func TestControlRequestsDoNotCrowdOutCalls(t *testing.T) {
	const calls, subscriptions = 40, 60
	f := newFixture(t)
	noted := contract.NewEventKey[string]("test.noted")
	called, subscribed := make(chan error, calls), make(chan error, subscriptions)
	var entered atomic.Int64
	release, full := make(chan struct{}), make(chan struct{})
	f.pack(echoManifest("test.busy", false, echoRequirement(0, 9)), sdk.Define(cordis.Plugin[struct{}]{Name: "main",
		Requires: []cordis.Requirement{contract.Require(echoKey, 0, 9)},
		Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
			provider, err := echoKey.Get(s)
			if err != nil {
				return err
			}
			for range calls {
				s.Go(func(ctx context.Context) error {
					_, err := contract.Invoke[echoRequest, echoResponse](ctx, provider, "echo", echoRequest{Text: "x"})
					called <- err
					return nil
				})
			}
			for range subscriptions {
				cancel, err := noted.On(ctx, s, func(context.Context, string) error { return nil })
				if err != nil {
					return err
				}
				s.Go(func(ctx context.Context) error {
					<-full // the calls hold every place they may have
					subscribed <- cancel(ctx)
					return nil
				})
			}
			return nil
		}}))
	f.open(func(o *Options) { o.Inflight = contract.MinInflight })
	must(t, echoKey.Provide(f.host.Scope(), contract.Methods(echoService, map[string]contract.Handler{
		"echo": contract.Typed(func(ctx context.Context, in echoRequest) (echoResponse, error) {
			entered.Add(1)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return echoResponse(in), ctx.Err()
		}),
	})))
	must(t, f.enable(contract.Entry{ID: "busy", Package: "test.busy"}).Wait(f.ctx()))
	eventually(t, "the calls to fill the session", func() bool { return entered.Load() == contract.MinInflight-8 })
	close(full)
	for range subscriptions {
		select {
		case err := <-subscribed:
			if err != nil {
				t.Errorf("a cancellation was refused instead of waiting: %v", err)
			}
		case <-f.ctx().Done():
			t.Fatal("subscriptions did not get through a session full of calls")
		}
	}
	close(release)
	for range calls {
		if err := <-called; err != nil {
			t.Errorf("a call failed: %v", err)
		}
	}
}
