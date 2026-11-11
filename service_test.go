package cordis_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis"
)

type database interface{ Name() string }

type sqlite string

func (s sqlite) Name() string { return string(s) }

var (
	dbKey    = cordis.NewService[database]("db")
	cacheKey = cordis.NewService[string]("cache")
)

// provider publishes value under key.
func provider[T any](name string, key cordis.Service[T], value T, requires ...cordis.Requirement) cordis.Plugin[struct{}] {
	return plugin(name, requires, func(_ context.Context, s *cordis.Scope) error { return key.Provide(s, value) })
}

func TestDuplicateProviderConflicts(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		first := load(app.Scope(), provider("first", dbKey, database(sqlite("one"))))
		mustWait(t, ctx, first, cordis.Active)
		second := load(app.Scope(), provider("second", dbKey, database(sqlite("two"))))
		err := second.Wait(ctx)
		if !errors.Is(err, cordis.ErrConflict) || !contains(err.Error(), "first") {
			t.Fatalf("second = %v, want conflict naming the first provider", err)
		}
		got, err := dbKey.Get(app.Scope())
		if err != nil || got.Name() != "one" {
			t.Fatalf("db = %v, %v", got, err)
		}
	})
}

func TestSameNameDifferentType(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		other := cordis.NewService[int]("db")
		mustWait(t, ctx, load(app.Scope(), provider("p", dbKey, database(sqlite("one")))), cordis.Active)
		consumer := load(app.Scope(), plugin("c", []cordis.Requirement{other}, func(context.Context, *cordis.Scope) error { return nil }))
		err := consumer.Wait(ctx)
		if !errors.Is(err, cordis.ErrTypeMismatch) || !contains(err.Error(), "int") || !contains(err.Error(), "database") {
			t.Fatalf("consumer = %v, want mismatch naming both types", err)
		}
		if got := consumer.Status().State; got != cordis.Failed {
			t.Fatalf("state = %v, want failed rather than waiting", got)
		}
	})
}

func TestProvideWhileActive(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		publish := make(chan struct{})
		load(app.Scope(), plugin("late", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Go(func(ctx context.Context) error {
				select {
				case <-publish:
					return cacheKey.Provide(s, "ready")
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			return nil
		}))
		consumer := load(app.Scope(), plugin("c", []cordis.Requirement{cacheKey}, func(context.Context, *cordis.Scope) error { return nil }))
		mustWait(t, ctx, consumer, cordis.Waiting)
		close(publish)
		synctest.Wait()
		mustWait(t, ctx, consumer, cordis.Active)
	})
}

func TestStartsWhenDependencyArrives(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		starts := 0
		consumer := load(app.Scope(), plugin("c", []cordis.Requirement{dbKey}, func(context.Context, *cordis.Scope) error { starts++; return nil }))
		mustWait(t, ctx, consumer, cordis.Waiting)
		missing := consumer.Status().Missing
		if len(missing) != 1 || missing[0].Service != "db" {
			t.Fatalf("missing = %+v", missing)
		}
		load(app.Scope(), provider("p", dbKey, database(sqlite("one"))))
		synctest.Wait()
		mustWait(t, ctx, consumer, cordis.Active)
		if starts != 1 {
			t.Fatalf("starts = %d", starts)
		}
	})
}

func TestUndeclaredGet(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		mustWait(t, ctx, load(app.Scope(), provider("p", dbKey, database(sqlite("one")))), cordis.Active)
		var got error
		consumer := load(app.Scope(), plugin("c", nil, func(_ context.Context, s *cordis.Scope) error {
			_, got = dbKey.Get(s)
			return nil
		}))
		mustWait(t, ctx, consumer, cordis.Active)
		if !errors.Is(got, cordis.ErrNotRequired) || !contains(got.Error(), `"db"`) {
			t.Fatalf("get = %v, want ErrNotRequired naming db", got)
		}
	})
}

func TestAncestorAdmission(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		mustWait(t, ctx, load(app.Scope(), provider("p", dbKey, database(sqlite("one")))), cordis.Active)
		rootKey := cordis.NewService[int]("root-service")
		if err := rootKey.Provide(app.Scope(), 7); err != nil {
			t.Fatal(err)
		}
		var db database
		var cache string
		var fromRoot int
		var errs []error
		child := plugin("child", nil, func(_ context.Context, s *cordis.Scope) (err error) {
			db, err = dbKey.Get(s)
			errs = append(errs, err)
			cache, err = cacheKey.Get(s)
			errs = append(errs, err)
			fromRoot, err = rootKey.Get(s)
			errs = append(errs, err)
			return nil
		})
		parent := load(app.Scope(), plugin("parent", []cordis.Requirement{dbKey}, func(_ context.Context, s *cordis.Scope) error {
			if err := cacheKey.Provide(s, "parent cache"); err != nil {
				return err
			}
			load(s, child)
			return nil
		}))
		mustWait(t, ctx, parent, cordis.Active)
		synctest.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatal(err)
		}
		if db.Name() != "one" || cache != "parent cache" || fromRoot != 7 {
			t.Fatalf("child saw db=%v cache=%q root=%d", db, cache, fromRoot)
		}
	})
}

func TestRootScopeGet(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		if _, err := dbKey.Get(app.Scope()); !errors.Is(err, cordis.ErrUnavailable) {
			t.Fatalf("get before provider = %v", err)
		}
		mustWait(t, ctx, load(app.Scope(), provider("p", dbKey, database(sqlite("one")))), cordis.Active)
		got, err := dbKey.Get(app.Scope())
		if err != nil || got.Name() != "one" {
			t.Fatalf("get = %v, %v", got, err)
		}
	})
}

func TestConditionalRequirement(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		version := cordis.NewService[*int]("version")
		current := 1
		var providerScope *cordis.Scope
		load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			providerScope = s
			return version.Provide(s, &current)
		}))
		atLeast2 := version.Where(func(v *int) error {
			if *v < 2 {
				return fmt.Errorf("version %d is below 2", *v)
			}
			return nil
		})
		starts := 0
		consumer := load(app.Scope(), plugin("c", []cordis.Requirement{atLeast2}, func(context.Context, *cordis.Scope) error { starts++; return nil }))
		synctest.Wait()
		status := consumer.Status()
		if status.State != cordis.Waiting || len(status.Missing) != 1 || !contains(status.Missing[0].Reason.Error(), "below 2") {
			t.Fatalf("status = %+v", status)
		}

		current = 2
		version.Changed(providerScope)
		synctest.Wait()
		mustWait(t, ctx, consumer, cordis.Active)

		// Not announced: a restart keeps using the cached outcome.
		current = 1
		if err := consumer.Restart(ctx); err != nil {
			t.Fatal(err)
		}
		mustWait(t, ctx, consumer, cordis.Active)
		if starts != 2 {
			t.Fatalf("starts = %d, want 2", starts)
		}

		version.Changed(providerScope)
		synctest.Wait()
		mustWait(t, ctx, consumer, cordis.Waiting)
	})
}

func TestChainedShutdownAndRecovery(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		aKey, bKey := cordis.NewService[string]("a"), cordis.NewService[string]("b")
		layer := func(name string, provide *cordis.Service[string], requires ...cordis.Requirement) cordis.Plugin[struct{}] {
			return plugin(name, requires, func(_ context.Context, s *cordis.Scope) error {
				log.add("start " + name)
				s.Defer(note(&log, "clean "+name))
				if provide != nil {
					return provide.Provide(s, name)
				}
				return nil
			})
		}
		a := load(app.Scope(), layer("a", &aKey))
		b := load(app.Scope(), layer("b", &bKey, aKey))
		c := load(app.Scope(), layer("c", nil, bKey))
		synctest.Wait()
		mustWait(t, ctx, c, cordis.Active)
		log.expect(t, "start a", "start b", "start c")

		if err := a.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "start a", "start b", "start c", "clean c", "clean b", "clean a")
		mustWait(t, ctx, b, cordis.Waiting)
		mustWait(t, ctx, c, cordis.Waiting)

		load(app.Scope(), layer("a", &aKey))
		synctest.Wait()
		mustWait(t, ctx, b, cordis.Active)
		mustWait(t, ctx, c, cordis.Active)
		if got := c.Status().Activations; got != 2 {
			t.Fatalf("c activations = %d", got)
		}
	})
}

func TestBindingUsableDuringCleanup(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		p := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(note(&log, "provider cleanup"))
			return dbKey.Provide(s, sqlite("one"))
		}))
		c := load(app.Scope(), plugin("c", []cordis.Requirement{dbKey}, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(func(context.Context) error {
				db, err := dbKey.Get(s)
				if err != nil {
					return err
				}
				log.add("consumer cleanup uses " + db.Name())
				return nil
			})
			return nil
		}))
		synctest.Wait()
		mustWait(t, ctx, c, cordis.Active)
		if err := p.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "consumer cleanup uses one", "provider cleanup")
	})
}

func TestDependencyLostDuringStart(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		entered := make(chan struct{})
		p := load(app.Scope(), provider("p", dbKey, database(sqlite("one"))))
		c := load(app.Scope(), plugin("c", []cordis.Requirement{dbKey}, func(ctx context.Context, s *cordis.Scope) error {
			s.Defer(note(&log, "cleanup"))
			close(entered)
			<-ctx.Done()
			log.add("start canceled")
			return nil
		}))
		<-entered
		if err := p.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		mustWait(t, ctx, c, cordis.Waiting)
		log.expect(t, "start canceled", "cleanup")
	})
}

func TestWithdrawSingleService(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		var withdraw func(context.Context) error
		p := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(note(&log, "provider cleanup"))
			sub, release := s.Sub()
			withdraw = release
			sub.Defer(note(&log, "service withdrawn"))
			return cacheKey.Provide(sub, "v")
		}))
		c := load(app.Scope(), plugin("c", []cordis.Requirement{cacheKey}, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(note(&log, "consumer cleanup"))
			return nil
		}))
		synctest.Wait()
		mustWait(t, ctx, c, cordis.Active)
		if err := withdraw(ctx); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "consumer cleanup", "service withdrawn")
		mustWait(t, ctx, c, cordis.Waiting)
		mustWait(t, ctx, p, cordis.Active)
	})
}

func TestConditionalSubScope(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		host := load(app.Scope(), plugin("host", nil, func(_ context.Context, s *cordis.Scope) error {
			s.When([]cordis.Requirement{cacheKey}, func(_ context.Context, s *cordis.Scope) error {
				value, err := cacheKey.Get(s)
				if err != nil {
					return err
				}
				log.add("cache " + value)
				s.Defer(note(&log, "cache gone"))
				return nil
			})
			return nil
		}))
		mustWait(t, ctx, host, cordis.Active)
		synctest.Wait()
		log.expect(t)
		cache := load(app.Scope(), provider("cache", cacheKey, "v1"))
		synctest.Wait()
		log.expect(t, "cache v1")
		if err := cache.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "cache v1", "cache gone")
		if got := host.Status(); got.State != cordis.Active || got.Activations != 1 {
			t.Fatalf("host = %+v", got)
		}
	})
}

func TestConsumersFollowProviderFailure(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		fail := make(chan struct{})
		p := cordis.Plugin[bool]{Name: "p", Start: func(_ context.Context, s *cordis.Scope, ok bool) error {
			if !ok {
				return errBoom
			}
			s.Go(func(ctx context.Context) error {
				select {
				case <-fail:
					return errBoom
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			return dbKey.Provide(s, sqlite("one"))
		}}
		prov := p.Load(app.Scope(), false)
		c := load(app.Scope(), plugin("c", []cordis.Requirement{dbKey}, func(context.Context, *cordis.Scope) error { return nil }))
		synctest.Wait()
		mustWait(t, ctx, c, cordis.Waiting)
		if err := prov.Update(ctx, true); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		mustWait(t, ctx, c, cordis.Active)
		close(fail)
		synctest.Wait()
		mustWait(t, ctx, c, cordis.Waiting)
		if got := prov.Status().State; got != cordis.Failed {
			t.Fatalf("provider = %v", got)
		}
	})
}
