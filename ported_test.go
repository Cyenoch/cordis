package cordis_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis"
)

// Behaviors taken from the reference test suite that the scenario tests do
// not already cover. Each test names its origin.

// fiber.spec: "failed fiber does not re-enter on dependency refresh".
func TestFailedInstanceIgnoresDependencyRefresh(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		starts := 0
		provider := load(app.Scope(), provider("db", dbKey, database(sqlite("one"))))
		consumer := load(app.Scope(), plugin("c", []cordis.Requirement{dbKey}, func(context.Context, *cordis.Scope) error {
			starts++
			return errBoom
		}))
		synctest.Wait()
		mustWait(t, ctx, consumer, cordis.Failed)
		if err := provider.Restart(ctx); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if starts != 1 || consumer.Status().State != cordis.Failed {
			t.Fatalf("starts = %d, state = %v", starts, consumer.Status().State)
		}
	})
}

// fiber.spec: "settles a pending fiber to DISPOSED on dispose" and
// "settles a failed fiber to DISPOSED on dispose".
func TestStopWaitingAndFailedInstances(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		waiting := load(app.Scope(), plugin("w", []cordis.Requirement{dbKey}, func(context.Context, *cordis.Scope) error { return nil }))
		failed := load(app.Scope(), plugin("f", nil, func(context.Context, *cordis.Scope) error { return errBoom }))
		mustWait(t, ctx, waiting, cordis.Waiting)
		mustWait(t, ctx, failed, cordis.Failed)
		if err := waiting.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		if err := failed.Stop(ctx); !errors.Is(err, errBoom) {
			t.Fatalf("stop of failed instance = %v, want its recorded error", err)
		}
		if waiting.Status().State != cordis.Stopped || failed.Status().State != cordis.Stopped {
			t.Fatalf("states = %v, %v", waiting.Status().State, failed.Status().State)
		}
	})
}

// update.spec: "waits for dependencies and loads only the latest config" and
// "does not load a queued config after disposal".
func TestWaitingInstanceLoadsOnlyLatestConfig(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var seen []int
		p := cordis.Plugin[int]{Name: "c", Requires: []cordis.Requirement{dbKey}, Start: func(_ context.Context, _ *cordis.Scope, n int) error {
			seen = append(seen, n)
			return nil
		}}
		consumer, dropped := p.Load(app.Scope(), 1), p.Load(app.Scope(), 1)
		mustWait(t, ctx, consumer, cordis.Waiting)
		for _, n := range []int{2, 3} {
			if err := consumer.Update(ctx, n); err != nil {
				t.Fatal(err)
			}
		}
		if err := dropped.Update(ctx, 9); err != nil {
			t.Fatal(err)
		}
		if err := dropped.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		load(app.Scope(), provider("db", dbKey, database(sqlite("one"))))
		synctest.Wait()
		mustWait(t, ctx, consumer, cordis.Active)
		if len(seen) != 1 || seen[0] != 3 {
			t.Fatalf("started with %v, want only 3", seen)
		}
	})
}

// isolate.spec: "does not expose own provisions across isolation boundaries".
func TestOwnProvisionHiddenAcrossIsolation(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var inner error
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			if err := dbKey.Provide(s, sqlite("own")); err != nil {
				return err
			}
			_, inner = dbKey.Get(s.Isolate(dbKey))
			return nil
		}))
		mustWait(t, ctx, inst, cordis.Active)
		if !errors.Is(inner, cordis.ErrNotRequired) {
			t.Fatalf("get across the boundary = %v", inner)
		}
	})
}

// service-dispose.spec: "releases dependencies after a cleanup error".
func TestCleanupErrorStillReleasesProvider(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		provider := load(app.Scope(), provider("db", dbKey, database(sqlite("one"))))
		consumer := load(app.Scope(), plugin("c", []cordis.Requirement{dbKey}, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(func(context.Context) error { return errBoom })
			return nil
		}))
		synctest.Wait()
		mustWait(t, ctx, consumer, cordis.Active)
		if err := provider.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		status := consumer.Status()
		if status.State != cordis.Failed || !errors.Is(status.Err, errBoom) {
			t.Fatalf("consumer = %+v", status)
		}
	})
}

// service-dispose.spec: "retains an ancestor injection until its child
// finishes cleanup".
func TestAncestorBindingOutlivesChildCleanup(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		provider := load(app.Scope(), plugin("db", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(note(&log, "provider cleanup"))
			return dbKey.Provide(s, sqlite("one"))
		}))
		parent := load(app.Scope(), plugin("parent", []cordis.Requirement{dbKey}, func(_ context.Context, s *cordis.Scope) error {
			load(s, plugin("child", nil, func(_ context.Context, s *cordis.Scope) error {
				s.Defer(func(context.Context) error {
					db, err := dbKey.Get(s)
					if err != nil {
						return err
					}
					log.add("child cleanup uses " + db.Name())
					return nil
				})
				return nil
			}))
			return nil
		}))
		synctest.Wait()
		mustWait(t, ctx, parent, cordis.Active)
		synctest.Wait()
		if err := provider.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "child cleanup uses one", "provider cleanup")
	})
}
