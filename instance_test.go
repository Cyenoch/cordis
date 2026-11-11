package cordis_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis"
)

func TestParentStopCleansSubtreeFirst(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		grandchild := plugin("grandchild", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(note(&log, "grandchild"))
			return nil
		})
		child := plugin("child", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(note(&log, "child"))
			load(s, grandchild)
			return nil
		})
		parent := load(app.Scope(), plugin("parent", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(note(&log, "parent"))
			load(s, child)
			return nil
		}))
		sibling := load(app.Scope(), plugin("sibling", nil, func(context.Context, *cordis.Scope) error { return nil }))
		mustWait(t, ctx, parent, cordis.Active)
		synctest.Wait()
		if err := parent.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "grandchild", "child", "parent")
		mustWait(t, ctx, sibling, cordis.Active)
		if got := parent.Status().State; got != cordis.Stopped {
			t.Fatalf("parent state = %v", got)
		}
	})
}

func TestParentRestartRebuildsChildren(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var children []*cordis.Instance[struct{}]
		child := plugin("child", nil, func(context.Context, *cordis.Scope) error { return nil })
		parent := load(app.Scope(), plugin("parent", nil, func(_ context.Context, s *cordis.Scope) error {
			children = append(children, load(s, child))
			return nil
		}))
		mustWait(t, ctx, parent, cordis.Active)
		synctest.Wait()
		if err := parent.Restart(ctx); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if len(children) != 2 {
			t.Fatalf("children created = %d, want 2", len(children))
		}
		if got := children[0].Status().State; got != cordis.Stopped {
			t.Fatalf("old child = %v, want stopped", got)
		}
		if err := children[0].Restart(ctx); !errors.Is(err, cordis.ErrInactive) {
			t.Fatalf("old handle restart = %v, want ErrInactive", err)
		}
		if got := children[1].Status().State; got != cordis.Active {
			t.Fatalf("new child = %v, want active", got)
		}
	})
}

func TestSamePluginLoadedTwice(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		p := cordis.Plugin[string]{Name: "p", Start: func(_ context.Context, s *cordis.Scope, name string) error {
			s.Defer(note(&log, "cleanup "+name))
			return nil
		}}
		first, second := p.Load(app.Scope(), "a"), p.Load(app.Scope(), "b")
		mustWait(t, ctx, first, cordis.Active)
		mustWait(t, ctx, second, cordis.Active)
		if err := first.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "cleanup a")
		if got := second.Status(); got.State != cordis.Active || got.Activations != 1 {
			t.Fatalf("second = %+v", got)
		}
	})
}

func TestDerivedScopeRegistrationsBelongToInstance(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		ping := cordis.NewEvent[int]("ping")
		db := cordis.NewService[int]("db")
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			isolated := s.Isolate(db)
			isolated.Defer(note(&log, "cleanup"))
			ping.On(isolated, func(context.Context, int) error { log.add("ping"); return nil })
			return nil
		}))
		mustWait(t, ctx, inst, cordis.Active)
		if err := ping.Emit(ctx, app.Scope(), 1); err != nil {
			t.Fatal(err)
		}
		if err := inst.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		if err := ping.Emit(ctx, app.Scope(), 2); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "ping", "cleanup")
	})
}

func TestPartialCleanupFailure(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(note(&log, "first"))
			s.Defer(func(context.Context) error { log.add("second"); panic("cleanup exploded") })
			return errBoom
		}))
		err := inst.Wait(ctx)
		log.expect(t, "second", "first")
		if !errors.Is(err, errBoom) || !errors.Is(err, cordis.ErrPanic) {
			t.Fatalf("error = %v, want start error and cleanup panic", err)
		}
		if got := inst.Status().State; got != cordis.Failed {
			t.Fatalf("state = %v", got)
		}
	})
}

func TestEarlyReleaseRunsOnce(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		var release func(context.Context) error
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			release = s.Defer(note(&log, "cleanup"))
			return nil
		}))
		mustWait(t, ctx, inst, cordis.Active)
		for range 2 {
			if err := release(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if err := inst.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "cleanup")
	})
}

func TestServiceRegistersOnCallerScope(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var mu sync.Mutex
		routes := map[string]bool{}
		handle := func(s *cordis.Scope, path string) {
			mu.Lock()
			defer mu.Unlock()
			routes[path] = true
			s.Defer(func(context.Context) error {
				mu.Lock()
				defer mu.Unlock()
				delete(routes, path)
				return nil
			})
		}
		a := load(app.Scope(), plugin("a", nil, func(_ context.Context, s *cordis.Scope) error { handle(s, "/a"); return nil }))
		b := load(app.Scope(), plugin("b", nil, func(_ context.Context, s *cordis.Scope) error { handle(s, "/b"); return nil }))
		mustWait(t, ctx, a, cordis.Active)
		mustWait(t, ctx, b, cordis.Active)
		if err := a.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		if routes["/a"] || !routes["/b"] {
			t.Fatalf("routes = %v", routes)
		}
	})
}

func TestManagedWorkFailureFailsInstance(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		fail := make(chan struct{})
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(note(&log, "cleanup"))
			s.Go(func(ctx context.Context) error {
				select {
				case <-fail:
					return errBoom
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			return nil
		}))
		mustWait(t, ctx, inst, cordis.Active)
		close(fail)
		synctest.Wait()
		status := inst.Status()
		if status.State != cordis.Failed || !errors.Is(status.Err, errBoom) {
			t.Fatalf("status = %+v", status)
		}
		log.expect(t, "cleanup")
	})
}

func TestStaleScopeAfterRestart(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		ping := cordis.NewEvent[int]("ping")
		var first *cordis.Scope
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			if first == nil {
				first = s
			}
			return nil
		}))
		mustWait(t, ctx, inst, cordis.Active)
		if err := inst.Restart(ctx); err != nil {
			t.Fatal(err)
		}
		first.Defer(note(&log, "late cleanup"))
		log.expect(t, "late cleanup")
		ping.On(first, func(context.Context, int) error { log.add("ping"); return nil })
		if err := ping.Emit(ctx, app.Scope(), 1); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "late cleanup")
	})
}

func TestFailedRecoversOnlyExplicitly(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		starts := 0
		p := cordis.Plugin[bool]{Name: "p", Start: func(_ context.Context, _ *cordis.Scope, ok bool) error {
			starts++
			if !ok {
				return errBoom
			}
			return nil
		}}
		inst := p.Load(app.Scope(), false)
		if err := inst.Wait(ctx); !errors.Is(err, errBoom) {
			t.Fatalf("wait = %v", err)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		if starts != 1 {
			t.Fatalf("starts = %d, want no background retry", starts)
		}
		if err := inst.Update(ctx, true); err != nil {
			t.Fatal(err)
		}
		if got := inst.Status(); got.State != cordis.Active || got.Err != nil {
			t.Fatalf("status = %+v", got)
		}
	})
}

func TestInvalidInitialConfig(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		started := false
		p := cordis.Plugin[int]{Name: "p",
			Validate: func(n int) error {
				if n < 0 {
					return errBoom
				}
				return nil
			},
			Start: func(context.Context, *cordis.Scope, int) error { started = true; return nil },
		}
		inst := p.Load(app.Scope(), -1)
		if err := inst.Wait(ctx); !errors.Is(err, errBoom) {
			t.Fatalf("wait = %v", err)
		}
		if started || inst.Status().State != cordis.Failed {
			t.Fatalf("started = %v, state = %v", started, inst.Status().State)
		}
	})
}

func TestStartPanicIsIsolated(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		healthy := load(app.Scope(), plugin("healthy", nil, func(context.Context, *cordis.Scope) error { return nil }))
		broken := load(app.Scope(), plugin("broken", nil, func(context.Context, *cordis.Scope) error { panic("no") }))
		err := broken.Wait(ctx)
		if !errors.Is(err, cordis.ErrPanic) {
			t.Fatalf("wait = %v, want ErrPanic", err)
		}
		if got := err.Error(); !contains(got, "start") {
			t.Fatalf("error %q does not name the start stage", got)
		}
		mustWait(t, ctx, healthy, cordis.Active)
	})
}

func TestWaitCancellationLeavesInstanceAlone(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		proceed := make(chan struct{})
		inst := load(app.Scope(), plugin("slow", nil, func(context.Context, *cordis.Scope) error {
			<-proceed
			return nil
		}))
		waitCtx, cancel := context.WithCancel(ctx)
		cancel()
		if err := inst.Wait(waitCtx); !errors.Is(err, context.Canceled) {
			t.Fatalf("wait = %v", err)
		}
		close(proceed)
		mustWait(t, ctx, inst, cordis.Active)
	})
}

func TestCloseReportsCleanupErrorAndCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := cordis.New(cordis.Options{})
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(func(context.Context) error { return errBoom })
			return nil
		}))
		mustWait(t, t.Context(), inst, cordis.Active)
		if err := app.Close(t.Context()); !errors.Is(err, errBoom) {
			t.Fatalf("close = %v", err)
		}
		select {
		case <-app.Done():
		default:
			t.Fatal("close returned an error but did not report completion")
		}
		late := load(app.Scope(), plugin("late", nil, func(context.Context, *cordis.Scope) error { return nil }))
		if got := late.Status().State; got != cordis.Stopped {
			t.Fatalf("load after close = %v", got)
		}
		if err := cordis.NewService[int]("x").Provide(app.Scope(), 1); !errors.Is(err, cordis.ErrInactive) {
			t.Fatalf("provide after close = %v", err)
		}
	})
}

func TestCloseTimeoutCanBeResumed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := cordis.New(cordis.Options{})
		release := make(chan struct{})
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(func(context.Context) error { <-release; return nil })
			return nil
		}))
		mustWait(t, t.Context(), inst, cordis.Active)
		short, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := app.Close(short); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("close = %v", err)
		}
		select {
		case <-app.Done():
			t.Fatal("reported completion while a cleanup is blocked")
		default:
		}
		close(release)
		if err := app.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		<-app.Done()
	})
}

func contains(s, part string) bool {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
