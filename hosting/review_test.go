package hosting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

// Regressions for defects found by review.

func TestCanceledDeliveriesDoNotKeepTheGuestFromStopping(t *testing.T) {
	f := newFixture(t)
	f.pack(plainManifest("test.listener"), sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		s.Defer(func(context.Context) error {
			f.log.add("cleaned up")
			return nil
		})
		_, err := noteEvent.On(ctx, s, func(context.Context, note) error { return nil })
		return err
	}}))
	f.open()
	must(t, f.enable(contract.Entry{ID: "listener", Package: "test.listener"}).Wait(f.ctx()))
	for n := range 400 {
		ctx, cancel := context.WithTimeout(f.ctx(), time.Duration(n%4)*100*time.Microsecond)
		noteEvent.Emit(ctx, f.host.Scope(), note{Text: "x"})
		cancel()
	}
	must(t, f.host.Disable(f.ctx(), "listener"))
	if f.log.count("cleaned up") != 1 {
		t.Fatal("the guest did not clean up")
	}
}

func TestStoppingAnEntryWhileItStarts(t *testing.T) {
	f := newFixture(t)
	f.pack(plainManifest("test.plain"), sdk.Define(f.plain("plain")))
	f.open()
	for n := range 150 {
		f.enable(contract.Entry{ID: "entry", Package: "test.plain"})
		time.Sleep(time.Duration(n%15) * 100 * time.Microsecond)
		if err := f.host.Disable(f.ctx(), "entry"); err != nil {
			t.Fatalf("round %d: disable = %v", n, err)
		}
	}
	if started, stopped := f.log.count("start plain"), f.log.count("stop plain"); started != stopped {
		t.Fatalf("%d starts, %d stops", started, stopped)
	}
}

func TestRequestsOfGoroutinesStartedInStart(t *testing.T) {
	f := newFixture(t)
	var group sync.WaitGroup
	failures := make(chan error, 64)
	f.pack(plainManifest("test.eager"), sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		for range 16 {
			group.Go(func() {
				for range 3 {
					// A request that is still under way when Start returns
					// ends with it; that is all that may go wrong.
					if err := noteEvent.Emit(context.Background(), s, note{Text: "x"}); err != nil && !errors.Is(err, context.Canceled) {
						failures <- err
					}
				}
			})
		}
		return nil
	}}))
	f.open()
	for n := range 40 {
		instance := f.enable(contract.Entry{ID: "eager", Package: "test.eager"})
		must(t, instance.Wait(f.ctx()))
		group.Wait()
		if f.state("eager") != contract.Active {
			t.Fatalf("round %d: %+v", n, instance.Status())
		}
		must(t, f.host.Disable(f.ctx(), "eager"))
	}
	select {
	case err := <-failures:
		t.Fatalf("a request made by a goroutine of Start failed: %v", err)
	default:
	}
}

func TestChainThroughOneGuestManyTimesDoesNotLockTheSession(t *testing.T) {
	f := newFixture(t)
	const executions = 80
	arrived := make(chan struct{}, executions)
	release := make(chan struct{})
	f.pack(plainManifest("test.twice"), sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		if _, err := render.Use(ctx, s, func(ctx context.Context, in string, next next) (string, error) {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return next(ctx, in)
		}); err != nil {
			return err
		}
		_, err := render.Use(ctx, s, func(ctx context.Context, in string, next next) (string, error) { return next(ctx, in) })
		return err
	}}))
	f.open()
	must(t, f.enable(contract.Entry{ID: "twice", Package: "test.twice"}).Wait(f.ctx()))
	results := make(chan error, executions)
	for range executions {
		go func() {
			ctx, cancel := context.WithTimeout(f.ctx(), 8*time.Second)
			defer cancel()
			_, err := render.Do(ctx, f.host.Scope(), "x", func(_ context.Context, in string) (string, error) { return in, nil })
			results <- err
		}()
	}
	// Let as many as the session admits reach the first middleware.
	deadline := time.After(3 * time.Second)
	for admitted := 0; admitted < executions; admitted++ {
		select {
		case <-arrived:
			continue
		case <-deadline:
		}
		break
	}
	close(release)
	succeeded := 0
	for range executions {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, contract.Fail(contract.NotReady, "")):
			// More hops than the session has requests: refused, not stuck.
		default:
			t.Fatalf("an execution ended with %v", err)
		}
	}
	// How many get through depends on timing; what matters is that every
	// one of them ended and the session works.
	_ = succeeded
	if out, err := render.Do(f.ctx(), f.host.Scope(), "after", func(_ context.Context, in string) (string, error) { return in, nil }); err != nil || out != "after" {
		t.Fatalf("the session is stuck: %q, %v", out, err)
	}
}

func TestDisableThatTimesOutLeavesTheEntryRemovable(t *testing.T) {
	f := newFixture(t)
	f.pack(plainManifest("test.slow"), sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		f.log.add("start")
		s.Defer(func(context.Context) error {
			time.Sleep(300 * time.Millisecond)
			f.log.add("stop")
			return nil
		})
		return nil
	}}))
	f.open()
	must(t, f.enable(contract.Entry{ID: "slow", Package: "test.slow"}).Wait(f.ctx()))
	short, cancel := context.WithTimeout(f.ctx(), 50*time.Millisecond)
	if err := f.host.Disable(short, "slow"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a disable that cannot finish in time = %v", err)
	}
	cancel()
	eventually(t, "the entry to finish stopping", func() bool { return f.log.count("stop") == 1 })
	if err := f.host.Disable(f.ctx(), "slow"); err != nil && !errors.Is(err, contract.Fail(contract.NotReady, "")) {
		t.Fatalf("the second disable = %v", err)
	}
	again := f.enable(contract.Entry{ID: "slow", Package: "test.slow"})
	must(t, again.Wait(f.ctx()))
	must(t, again.Stop(f.ctx()))
	if status := again.Status(); status.State != contract.Stopped || !status.TemporaryStopped || f.log.count("stop") != 2 {
		t.Fatalf("status = %+v, events %q", status, f.log.get())
	}
}

func TestWorkThatFailsRightAfterStartKeepsItsReason(t *testing.T) {
	f := newFixture(t)
	f.pack(plainManifest("test.boom"), sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		s.Go(func(context.Context) error { return errors.New("boom-reason") })
		return nil
	}}))
	f.open()
	for n := range 40 {
		id := fmt.Sprint("boom", n)
		instance := f.enable(contract.Entry{ID: id, Package: "test.boom"})
		instance.Wait(f.ctx())
		eventually(t, "the entry to fail", func() bool { return f.state(id) == contract.Failed })
		if status := instance.Status(); !strings.Contains(status.Error, "boom-reason") {
			t.Fatalf("round %d: the reason was lost: %q", n, status.Error)
		}
	}
}

func TestRegistrationsShareOneQuota(t *testing.T) {
	f := newFixture(t)
	counts := make(chan [2]int, 1)
	f.pack(plainManifest("test.greedy"), sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		var chains, listeners int
		for range 200 {
			if _, err := render.Use(ctx, s, func(ctx context.Context, in string, next next) (string, error) { return next(ctx, in) }); err == nil {
				chains++
			}
		}
		for range 200 {
			if _, err := noteEvent.On(ctx, s, func(context.Context, note) error { return nil }); err == nil {
				listeners++
			}
		}
		counts <- [2]int{chains, listeners}
		return nil
	}}))
	f.open()
	must(t, f.enable(contract.Entry{ID: "greedy", Package: "test.greedy"}).Wait(f.ctx()))
	if got := <-counts; got[0] != 200 || got[0]+got[1] != contract.MaxSubscriptions {
		t.Fatalf("%d middleware and %d listeners were registered, the quota is %d", got[0], got[1], contract.MaxSubscriptions)
	}
}

func TestStoppedChildFreesItsIdentifier(t *testing.T) {
	f := newFixture(t)
	results := make(chan error, 1)
	parent := cordis.Plugin[struct{}]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		s.Go(func(ctx context.Context) error {
			results <- func() error {
				for range 3 {
					child, err := sdk.CreateChild(ctx, s, contract.Child{ID: "feature", Definition: "feature"})
					if err != nil {
						return err
					}
					if err := child.Wait(ctx); err != nil {
						return err
					}
					if err := child.Stop(ctx); err != nil {
						return err
					}
				}
				return nil
			}()
			return nil
		})
		return nil
	}}
	manifest := contract.Manifest{ID: "test.parent", Definitions: []contract.DefinitionSpec{{ID: "main"}, {ID: "feature"}}}
	f.pack(manifest, sdk.Define(parent), sdk.Define(cordis.Plugin[struct{}]{Name: "feature"}))
	f.open()
	must(t, f.enable(contract.Entry{ID: "parent", Package: "test.parent"}).Wait(f.ctx()))
	must(t, <-results)
	if infos := f.host.Instances(); len(infos) != 1 {
		t.Fatalf("stopped children are still listed: %+v", infos)
	}
}
