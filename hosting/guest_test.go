package hosting

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
)

func TestDynamicChildren(t *testing.T) {
	f := newFixture(t)
	results := make(chan error, 3)
	parent := cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		f.log.add("start parent")
		s.Defer(func(context.Context) error {
			f.log.add("stop parent")
			return nil
		})
		// The child lacks its dependency; creating it must not wait.
		_, err := sdk.CreateChild(ctx, s, contract.Child{ID: "feature", Definition: "feature"})
		results <- err
		_, err = sdk.CreateChild(ctx, s, contract.Child{ID: "feature", Definition: "feature"})
		results <- err
		_, err = sdk.CreateChild(ctx, s, contract.Child{ID: "other", Definition: "missing"})
		results <- err
		return nil
	}}
	feature := f.userPlugin("feature")
	manifest := contract.Manifest{ID: "test.parent", Definitions: []contract.DefinitionSpec{
		{ID: "main"}, {ID: "feature", Requires: []contract.Consumer{echoRequirement(0, 9)}}}}
	f.pack(manifest, sdk.Define(parent), sdk.Define(feature))
	f.open()
	instance := f.enable(contract.Entry{ID: "parent", Package: "test.parent"})
	must(t, instance.Wait(f.ctx()))
	must(t, <-results)
	if err := <-results; !errors.Is(err, contract.Fail(contract.Conflict, "")) {
		t.Fatalf("a second child with the same id = %v", err)
	}
	if err := <-results; !errors.Is(err, contract.Fail(contract.Invalid, "")) {
		t.Fatalf("a child of an unknown definition = %v", err)
	}
	if f.state("parent") != contract.Active || f.state("parent.feature") != contract.Waiting {
		t.Fatalf("parent %s, child %s", f.state("parent"), f.state("parent.feature"))
	}
	infos := f.host.Instances()
	if len(infos) != 2 || infos[1].Origin != "dynamic" || infos[1].Owner == nil {
		t.Fatalf("instances = %+v", infos)
	}
	provider := f.echoPlugin("provider", "").Load(f.host.Scope(), struct{}{})
	eventually(t, "the child", func() bool { return f.state("parent.feature") == contract.Active })
	must(t, f.host.Disable(f.ctx(), "parent"))
	must(t, provider.Stop(f.ctx()))
	want := []string{"start parent", "start provider", "feature got start", "feature got cleanup", "stop parent", "stop provider"}
	if got := f.log.get(); !slices.Equal(got, want) {
		t.Fatalf("events = %q", got)
	}
	entries, err := f.host.Store().Entries()
	must(t, err)
	if len(entries) != 1 || len(f.host.Instances()) != 0 {
		t.Fatalf("children were persisted or left behind: %+v, %+v", entries, f.host.Instances())
	}
}

func TestConcurrentCallsShareTheSessionLimit(t *testing.T) {
	f := newFixture(t)
	limit := wire.DefaultLimits().Inflight
	const calls = 3 * 64
	var running, peak atomic.Int32
	release := make(chan struct{})
	slow := cordis.Plugin[struct{}]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		return echoKey.Provide(s, contract.Methods(echoService, map[string]contract.Handler{
			"echo": contract.Typed(func(ctx context.Context, in echoRequest) (echoResponse, error) {
				now := running.Add(1)
				for {
					seen := peak.Load()
					if now <= seen || peak.CompareAndSwap(seen, now) {
						break
					}
				}
				defer running.Add(-1)
				select {
				case <-release:
				case <-ctx.Done():
					return echoResponse{}, ctx.Err()
				}
				return echoResponse{Text: in.Text}, nil
			}),
		}))
	}}
	f.pack(echoManifest("test.slow", true), sdk.Define(slow))
	f.open()
	must(t, f.enable(contract.Entry{ID: "slow", Package: "test.slow"}).Wait(f.ctx()))
	provider, err := echoKey.Get(f.host.Scope())
	must(t, err)

	var group sync.WaitGroup
	errs := make([]error, calls)
	for n := range calls {
		group.Go(func() {
			_, errs[n] = contract.Invoke[echoRequest, echoResponse](f.ctx(), provider, "echo", echoRequest{Text: fmt.Sprint(n)})
		})
	}
	// Handlers run concurrently, up to the session's limit and not beyond.
	eventually(t, "concurrent handlers", func() bool { return peak.Load() > 1 })
	canceled, cancel := context.WithCancel(f.ctx())
	waiting := make(chan error, 1)
	go func() {
		_, err := contract.Invoke[echoRequest, echoResponse](canceled, provider, "echo", echoRequest{})
		waiting <- err
	}()
	cancel()
	if err := <-waiting; !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled caller waiting for a slot = %v", err)
	}
	close(release)
	group.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("calls beyond the limit failed: %v", err)
	}
	if got := int(peak.Load()); got > limit {
		t.Fatalf("%d handlers ran at once, the session admits %d", got, limit)
	}
}

type note struct {
	Text string `json:"text"`
}

var (
	noteEvent = contract.NewEventKey[note]("test.note")
	noteQuery = contract.NewQueryKey[note, string]("test.note")
)

func TestEventsAndQueriesCrossTheSession(t *testing.T) {
	f := newFixture(t)
	type control struct {
		cancel func(context.Context) error
	}
	controls := make(chan control, 1)
	listener := cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		cancel, err := noteEvent.On(ctx, s, func(_ context.Context, n note) error {
			f.log.add("guest heard " + n.Text)
			if n.Text == "fail" {
				return errors.New("guest listener failed")
			}
			return nil
		})
		if err != nil {
			return err
		}
		if _, err := noteEvent.On(ctx, s, func(_ context.Context, n note) error {
			f.log.add("guest once " + n.Text)
			return nil
		}, cordis.Once); err != nil {
			return err
		}
		if _, err := noteQuery.Handle(ctx, s, func(_ context.Context, n note) (string, bool, error) {
			return "", n.Text == "guest", nil // a zero answer is an answer
		}, cordis.Prepend); err != nil {
			return err
		}
		// A plain kernel service stays in this process.
		if err := cordis.NewService[string]("test.local").Provide(s, "guest only"); err != nil {
			return err
		}
		controls <- control{cancel: cancel}
		return nil
	}}
	f.pack(plainManifest("test.listener"), sdk.Define(listener))
	f.open()
	root := f.host.Scope()
	if _, err := noteQuery.Handle(f.ctx(), root, func(_ context.Context, n note) (string, bool, error) {
		return "host", true, nil
	}); err != nil {
		t.Fatal(err)
	}
	must(t, f.enable(contract.Entry{ID: "listener", Package: "test.listener"}).Wait(f.ctx()))
	c := <-controls

	must(t, noteEvent.Emit(f.ctx(), root, note{Text: "one"}))
	must(t, noteEvent.EmitParallel(f.ctx(), root, note{Text: "two"}))
	err := noteEvent.Emit(f.ctx(), root, note{Text: "fail"})
	var located *cordis.HandlerError
	if !errors.As(err, &located) || located.Instance.Name != "listener" || !strings.Contains(err.Error(), "test.note") {
		t.Fatalf("a failing guest listener = %v", err)
	}
	if err := contract.RawEvent("test.note").Emit(f.ctx(), root, []byte(`{"text":1}`)); err == nil {
		t.Fatal("a payload of the wrong type was delivered")
	}

	// The prepended guest handler answers first, with a zero value.
	if out, handled, err := noteQuery.Ask(f.ctx(), root, note{Text: "guest"}); err != nil || !handled || out != "" {
		t.Fatalf("ask = %q, %v, %v", out, handled, err)
	}
	if out, handled, err := noteQuery.Ask(f.ctx(), root, note{Text: "anyone"}); err != nil || !handled || out != "host" {
		t.Fatalf("ask = %q, %v, %v", out, handled, err)
	}

	// Canceling takes effect for later emissions; canceling again succeeds.
	must(t, c.cancel(f.ctx()))
	must(t, c.cancel(f.ctx()))
	must(t, noteEvent.Emit(f.ctx(), root, note{Text: "three"}))
	want := []string{"guest heard one", "guest once one", "guest heard two", "guest heard fail"}
	if got := f.log.get(); !slices.Equal(got, want) {
		t.Fatalf("events = %q", got)
	}
	if _, err := cordis.NewService[string]("test.local").Get(root); err == nil {
		t.Fatal("a plain service of the guest is visible in the host")
	}
}

func TestGuestEmitsToTheHostAndCleansUpItsOwnTree(t *testing.T) {
	f := newFixture(t)
	emitted := make(chan error, 1)
	guest := cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		s.Defer(func(context.Context) error {
			f.log.add("stop parent")
			return nil
		})
		cordis.Plugin[struct{}]{Name: "inner", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			s.Defer(func(context.Context) error {
				f.log.add("stop inner")
				return nil
			})
			return nil
		}}.Load(s, struct{}{})
		s.Go(func(ctx context.Context) error {
			emitted <- noteEvent.Emit(ctx, s, note{Text: "from guest"})
			return nil
		})
		return nil
	}}
	f.pack(plainManifest("test.emitter"), sdk.Define(guest))
	f.open()
	if _, err := noteEvent.On(f.ctx(), f.host.Scope(), func(_ context.Context, n note) error {
		f.log.add("host heard " + n.Text)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	must(t, f.enable(contract.Entry{ID: "emitter", Package: "test.emitter"}).Wait(f.ctx()))
	must(t, <-emitted)
	must(t, f.host.Disable(f.ctx(), "emitter"))
	want := []string{"host heard from guest", "stop inner", "stop parent"}
	if got := f.log.get(); !slices.Equal(got, want) {
		t.Fatalf("events = %q", got)
	}
}

func TestEndpointWithdrawnAfterStart(t *testing.T) {
	f := newFixture(t)
	withdraw := make(chan func(context.Context) error, 1)
	guest := cordis.Plugin[struct{}]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		sub, release := s.Sub()
		withdraw <- release
		return f.echoPlugin("guest", "g:").Start(nil, sub, struct{}{})
	}}
	f.pack(echoManifest("test.withdraw", true), sdk.Define(guest))
	f.open()
	must(t, f.enable(contract.Entry{ID: "entry", Package: "test.withdraw"}).Wait(f.ctx()))
	provider, err := echoKey.Get(f.host.Scope())
	must(t, err)
	if _, err := contract.Invoke[echoRequest, echoResponse](f.ctx(), provider, "echo", echoRequest{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	must(t, (<-withdraw)(f.ctx()))
	_, err = contract.Invoke[echoRequest, echoResponse](f.ctx(), provider, "echo", echoRequest{Text: "x"})
	if !errors.Is(err, contract.Fail(contract.NotReady, "")) || f.state("entry") != contract.Active {
		t.Fatalf("call of a withdrawn endpoint = %v, entry %s", err, f.state("entry"))
	}
}

func TestConsumerSurvivesProviderRestarts(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int64
	poller := cordis.Plugin[struct{}]{Name: "main", Requires: []cordis.Requirement{contract.Require(echoKey, 0, 9)},
		Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			provider, err := echoKey.Get(s)
			if err != nil {
				return err
			}
			s.Go(func(ctx context.Context) error {
				for {
					_, err := contract.Invoke[echoRequest, echoResponse](ctx, provider, "echo", echoRequest{Text: "x"})
					if ctx.Err() != nil {
						return nil
					}
					if err != nil {
						return fmt.Errorf("poll: %w", err)
					}
					calls.Add(1)
				}
			})
			return nil
		}}
	f.pack(echoManifest("test.provider", true), sdk.Define(f.echoPlugin("main", "")))
	f.pack(echoManifest("test.poller", false, echoRequirement(0, 9)), sdk.Define(poller))
	f.open()
	provider := f.enable(contract.Entry{ID: "provider", Package: "test.provider"})
	consumer := f.enable(contract.Entry{ID: "poller", Package: "test.poller"})
	must(t, consumer.Wait(f.ctx()))
	for range 20 {
		before := calls.Load()
		deadline := time.Now().Add(5 * time.Second)
		for calls.Load() <= before {
			if time.Now().After(deadline) {
				t.Fatalf("the consumer stopped calling: consumer %+v\nprovider %+v", consumer.Status(), provider.Status())
			}
			time.Sleep(time.Millisecond)
		}
		if _, err := provider.Update(f.ctx(), []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		must(t, consumer.Wait(f.ctx()))
		if status := consumer.Status(); status.State != contract.Active {
			t.Fatalf("the consumer did not come back: %+v", status)
		}
	}
	must(t, f.host.Disable(f.ctx(), "poller"))
	must(t, f.host.Disable(f.ctx(), "provider"))
}

func TestSubscriptionRaces(t *testing.T) {
	f := newFixture(t)
	type handles struct {
		self, once, plain func(context.Context) error
		scope             *cordis.Scope
	}
	ready := make(chan handles, 1)
	var selfCalls, onceCalls, plainCalls atomic.Int64
	listener := cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		var h handles
		var err error
		h.scope = s
		// A listener that cancels itself from inside its callback.
		if h.self, err = noteEvent.On(ctx, s, func(ctx context.Context, _ note) error {
			selfCalls.Add(1)
			return h.self(ctx)
		}); err != nil {
			return err
		}
		if h.once, err = noteEvent.On(ctx, s, func(context.Context, note) error {
			onceCalls.Add(1)
			return nil
		}, cordis.Once); err != nil {
			return err
		}
		if h.plain, err = contract.NewEventKey[note]("test.busy").On(ctx, s, func(context.Context, note) error {
			plainCalls.Add(1)
			return nil
		}); err != nil {
			return err
		}
		ready <- h
		return nil
	}}
	failing := cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		if _, err := noteEvent.On(ctx, s, func(context.Context, note) error {
			f.log.add("a listener of a failed start ran")
			return nil
		}); err != nil {
			return err
		}
		return errors.New("start failed after subscribing")
	}}
	f.pack(plainManifest("test.listener"), sdk.Define(listener))
	f.pack(plainManifest("test.failing"), sdk.Define(failing))
	f.open()
	root := f.host.Scope()
	if err := f.enable(contract.Entry{ID: "failing", Package: "test.failing"}).Wait(f.ctx()); err == nil {
		t.Fatal("the failing entry started")
	}
	entry := f.enable(contract.Entry{ID: "listener", Package: "test.listener"})
	must(t, entry.Wait(f.ctx()))
	h := <-ready

	// Two concurrent emissions: the once listener and the self-canceling one
	// each run at most once, and nothing deadlocks.
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			if err := noteEvent.EmitParallel(f.ctx(), root, note{Text: "x"}); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	must(t, noteEvent.Emit(f.ctx(), root, note{Text: "again"}))
	if onceCalls.Load() != 1 || selfCalls.Load() < 1 || selfCalls.Load() > 2 {
		t.Fatalf("once ran %d times, the self-canceling listener %d times", onceCalls.Load(), selfCalls.Load())
	}
	// Canceling what was consumed or canceled already succeeds.
	must(t, h.once(f.ctx()))
	must(t, h.self(f.ctx()))
	if len(f.log.get()) != 0 {
		t.Fatalf("events = %q", f.log.get())
	}

	// A cancellation racing deliveries never loses an admitted callback.
	busy := contract.NewEventKey[note]("test.busy")
	stop := make(chan struct{})
	emitErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				emitErr <- nil
				return
			default:
			}
			if err := busy.Emit(f.ctx(), root, note{Text: "x"}); err != nil {
				emitErr <- err
				return
			}
		}
	}()
	eventually(t, "deliveries", func() bool { return plainCalls.Load() > 10 })
	must(t, h.plain(f.ctx()))
	settled := plainCalls.Load()
	close(stop)
	must(t, <-emitErr)
	must(t, busy.Emit(f.ctx(), root, note{Text: "after"}))
	if plainCalls.Load() > settled+1 {
		t.Fatalf("deliveries were admitted after the cancellation was confirmed: %d then %d", settled, plainCalls.Load())
	}

	// A registration of the old activation that arrives after it stopped is
	// refused, by the old session and by the new one.
	instance := entry.(*hostInstance)
	instance.mu.Lock()
	old := instance.run
	instance.mu.Unlock()
	late := []byte(`{"id":"` + old.identity.Activation + `/99","kind":"event","name":"test.note","options":{"once":false,"prepend":false}}`)
	must(t, entry.Stop(f.ctx()))
	if _, err := old.handle(f.ctx(), "event.subscribe", late); !errors.Is(err, contract.Fail(contract.NotReady, "")) {
		t.Fatalf("a stopped activation registered a listener: %v", err)
	}
	must(t, f.host.Resume(f.ctx(), "listener"))
	<-ready
	instance.mu.Lock()
	current := instance.run
	instance.mu.Unlock()
	if _, err := current.handle(f.ctx(), "event.subscribe", late); !errors.Is(err, contract.Fail(contract.Invalid, "")) {
		t.Fatalf("the new activation took a registration of the old one: %v", err)
	}
	must(t, noteEvent.Emit(f.ctx(), root, note{Text: "new activation"}))
}
