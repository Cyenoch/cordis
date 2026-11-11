package cordis_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis"
)

// Regressions for defects found by review.

func TestStopIsIdempotent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		app := cordis.New(cordis.Options{})
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(func(context.Context) error { return errBoom })
			return nil
		}))
		mustWait(t, ctx, inst, cordis.Active)
		for range 2 {
			if err := inst.Stop(ctx); !errors.Is(err, errBoom) {
				t.Fatalf("stop = %v", err)
			}
		}
		other := load(app.Scope(), plugin("other", nil, func(context.Context, *cordis.Scope) error { return nil }))
		mustWait(t, ctx, other, cordis.Active)
		// The first instance stopped before the shutdown, so its cleanup
		// error is not part of what Close reports.
		if err := app.Close(ctx); err != nil {
			t.Fatalf("close = %v", err)
		}
		if err := other.Stop(ctx); err != nil {
			t.Fatalf("stop after close = %v", err)
		}
		late := load(app.Scope(), plugin("late", nil, func(context.Context, *cordis.Scope) error { return nil }))
		if err := late.Stop(ctx); !errors.Is(err, cordis.ErrInactive) {
			t.Fatalf("stop of a handle loaded after close = %v", err)
		}
	})
}

func TestSubReleaseReportsOnce(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		sub, release := app.Scope().Sub()
		sub.Defer(func(context.Context) error { return errBoom })
		if err := release(ctx); !errors.Is(err, errBoom) {
			t.Fatalf("release = %v", err)
		}
		if err := release(ctx); err != nil {
			t.Fatalf("second release = %v", err)
		}
	})
}

// No instance may be loaded into a scope after its last child has stopped.
func TestLoadRacingWithStopLeavesNoOrphan(t *testing.T) {
	for range 300 {
		app := cordis.New(cordis.Options{})
		ctx := context.Background()
		var scope *cordis.Scope
		parent := load(app.Scope(), plugin("parent", nil, func(_ context.Context, s *cordis.Scope) error {
			scope = s
			return nil
		}))
		if err := parent.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		child := plugin("child", nil, func(context.Context, *cordis.Scope) error { return nil })
		var loaded []*cordis.Instance[struct{}]
		var mu sync.Mutex
		var group sync.WaitGroup
		for range 4 {
			group.Go(func() {
				for range 8 {
					inst := load(scope, child)
					mu.Lock()
					loaded = append(loaded, inst)
					mu.Unlock()
				}
			})
		}
		if err := parent.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		group.Wait()
		for _, inst := range loaded {
			inst.Wait(ctx)
			if got := inst.Status().State; got != cordis.Stopped {
				t.Fatalf("child is %v after its parent stopped", got)
			}
		}
		if err := app.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLateFailureOfObsoleteStartIsIgnored(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		release := make(chan struct{})
		var starts []int
		p := cordis.Plugin[int]{Name: "p", Start: func(_ context.Context, _ *cordis.Scope, n int) error {
			starts = append(starts, n)
			if n == 1 {
				<-release // ignores its context
				return errBoom
			}
			return nil
		}}
		inst := p.Load(app.Scope(), 1)
		synctest.Wait()
		done := make(chan error, 1)
		go func() { done <- inst.Update(ctx, 2) }()
		synctest.Wait()
		close(release)
		if err := <-done; err != nil {
			t.Fatalf("update = %v", err)
		}
		if got := inst.Status(); got.State != cordis.Active || len(starts) != 2 || starts[1] != 2 {
			t.Fatalf("status = %+v, starts = %v", got, starts)
		}
	})
}

func TestFilterMayCallIntoTheKernel(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		if err := cacheKey.Provide(app.Scope(), "v"); err != nil {
			t.Fatal(err)
		}
		calls := 0
		ping.On(app.Scope(), func(context.Context, int) error { calls++; return nil })
		scoped := app.Scope().Filter(func(l *cordis.Scope) bool {
			_, err := cacheKey.Get(l)
			return err == nil
		})
		if err := ping.Emit(ctx, scoped, 1); err != nil || calls != 1 {
			t.Fatalf("emit = %v, calls = %d", err, calls)
		}
	})
}

func TestOnUpdateThroughStaleScope(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var first *cordis.Scope
		starts := 0
		p := cordis.Plugin[int]{Name: "p", Start: func(_ context.Context, s *cordis.Scope, _ int) error {
			starts++
			if first == nil {
				first = s
			}
			return nil
		}}
		inst := p.Load(app.Scope(), 1)
		mustWait(t, ctx, inst, cordis.Active)
		if err := inst.Restart(ctx); err != nil {
			t.Fatal(err)
		}
		handler := func(context.Context, int) (bool, error) { return true, nil }
		if err := cordis.OnUpdate(first, handler); !errors.Is(err, cordis.ErrInactive) {
			t.Fatalf("on update through the previous activation's scope = %v", err)
		}
		if err := inst.Update(ctx, 2); err != nil {
			t.Fatal(err)
		}
		if starts != 3 {
			t.Fatalf("starts = %d, want a restart because no handler is installed", starts)
		}
		if err := inst.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		if err := cordis.OnUpdate(first, handler); !errors.Is(err, cordis.ErrInactive) {
			t.Fatalf("on update after stop = %v", err)
		}
	})
}

type listError []string

func (e listError) Error() string { return "invalid" }

func TestChainHandlesUncomparableErrors(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		pipeline.Use(app.Scope(), func(ctx context.Context, in int, next cordis.Handler[int, string]) (string, error) {
			return next(ctx, in)
		})
		_, err := pipeline.Do(ctx, app.Scope(), 1, func(context.Context, int) (string, error) {
			return "", listError{"a"}
		})
		var list listError
		if !errors.As(err, &list) {
			t.Fatalf("do = %v", err)
		}
	})
}

func TestLifecycleReachesEveryListener(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		seen := 0
		cordis.InstanceChanged.On(app.Scope(), func(context.Context, cordis.InstanceChange) error { return errBoom })
		cordis.InstanceChanged.On(app.Scope(), func(context.Context, cordis.InstanceChange) error { seen++; return nil })
		mustWait(t, ctx, load(app.Scope(), plugin("p", nil, func(context.Context, *cordis.Scope) error { return nil })), cordis.Active)
		synctest.Wait()
		if seen == 0 {
			t.Fatal("a failing listener hid the transition from the next one")
		}
	})
}

func TestConditionalScopeFailureFailsItsInstance(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		if err := cacheKey.Provide(app.Scope(), "v"); err != nil {
			t.Fatal(err)
		}
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			s.When([]cordis.Requirement{cacheKey}, func(context.Context, *cordis.Scope) error { return errBoom })
			return nil
		}))
		synctest.Wait()
		inst.Wait(ctx)
		if status := inst.Status(); status.State != cordis.Failed || !errors.Is(status.Err, errBoom) {
			t.Fatalf("status = %+v", status)
		}
	})
}

func TestSubScopeCleanupErrorIsReportedByOwner(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			sub, _ := s.Sub()
			sub.Defer(func(context.Context) error { return errBoom })
			return nil
		}))
		mustWait(t, ctx, inst, cordis.Active)
		if err := inst.Stop(ctx); !errors.Is(err, errBoom) {
			t.Fatalf("stop = %v", err)
		}
	})
}

func TestServiceTypeChecks(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		s := app.Scope()
		concrete := cordis.NewService[sqlite]("db")
		if err := concrete.Provide(s, "one"); err != nil {
			t.Fatal(err)
		}
		if _, err := dbKey.Get(s); !errors.Is(err, cordis.ErrTypeMismatch) {
			t.Fatalf("get through a key of another type = %v", err)
		}
		absent := cordis.NewService[database]("absent")
		if err := absent.Provide(s, nil); err != nil {
			t.Fatal(err)
		}
		if got, err := absent.Get(s); err != nil || got != nil {
			t.Fatalf("nil interface value = %v, %v", got, err)
		}
		optional := cordis.NewSetting[database](absent)
		if got := optional.Values(optional.With(s, nil)); len(got) != 1 || got[0] != nil {
			t.Fatalf("nil setting value = %v", got)
		}
	})
}

func TestSubScopeProvisionIsOwnProvision(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var own, child error
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			sub, _ := s.Sub()
			if err := cacheKey.Provide(sub, "v"); err != nil {
				return err
			}
			_, own = cacheKey.Get(s)
			load(s, plugin("child", nil, func(_ context.Context, s *cordis.Scope) error {
				_, child = cacheKey.Get(s)
				return nil
			}))
			return nil
		}))
		mustWait(t, ctx, inst, cordis.Active)
		synctest.Wait()
		if own != nil || child != nil {
			t.Fatalf("own = %v, child = %v", own, child)
		}
	})
}

func TestRootScopeWorkFailureDoesNotStopTheKernel(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		inst := load(app.Scope(), plugin("p", nil, func(context.Context, *cordis.Scope) error { return nil }))
		mustWait(t, ctx, inst, cordis.Active)
		app.Scope().Go(func(context.Context) error { return errBoom })
		synctest.Wait()
		mustWait(t, ctx, inst, cordis.Active)
		mustWait(t, ctx, load(app.Scope(), plugin("later", nil, func(context.Context, *cordis.Scope) error { return nil })), cordis.Active)
	})
}

func TestCloseFromCallbackStartsShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := cordis.New(cordis.Options{})
		var got error
		ping.On(app.Scope(), func(ctx context.Context, _ int) error {
			got = app.Close(ctx)
			return nil
		})
		ping.Emit(t.Context(), app.Scope(), 1)
		if !errors.Is(got, cordis.ErrReentrant) {
			t.Fatalf("close from a callback = %v", got)
		}
		<-app.Done()
	})
}

func TestWorkErrorAfterCancellationIsNotAFailure(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		closed := errors.New("server closed")
		inst := load(app.Scope(), plugin("server", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Go(func(ctx context.Context) error {
				<-ctx.Done()
				return closed
			})
			return nil
		}))
		mustWait(t, ctx, inst, cordis.Active)
		if err := inst.Restart(ctx); err != nil {
			t.Fatalf("restart = %v", err)
		}
		mustWait(t, ctx, inst, cordis.Active)
	})
}

func TestRestartDoesNotSupersedeUpdate(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		gate := make(chan struct{})
		p := cordis.Plugin[int]{Name: "p", Start: func(ctx context.Context, _ *cordis.Scope, n int) error {
			if n == 2 {
				select {
				case <-gate:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}}
		inst := p.Load(app.Scope(), 1)
		mustWait(t, ctx, inst, cordis.Active)
		update := make(chan error, 1)
		go func() { update <- inst.Update(ctx, 2) }()
		synctest.Wait()
		restart := make(chan error, 1)
		go func() { restart <- inst.Restart(ctx) }()
		synctest.Wait()
		close(gate)
		if err := <-update; err != nil {
			t.Fatalf("update = %v, want success because its configuration is the one running", err)
		}
		if err := <-restart; err != nil {
			t.Fatalf("restart = %v", err)
		}
	})
}
