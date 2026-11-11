package cordis_test

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis"
)

// reader records the database it is bound to.
func reader(name string, seen *journal) cordis.Plugin[struct{}] {
	return plugin(name, []cordis.Requirement{dbKey}, func(_ context.Context, s *cordis.Scope) error {
		db, err := dbKey.Get(s)
		if err != nil {
			return err
		}
		seen.add(name + " -> " + db.Name())
		return nil
	})
}

func TestTwoGroupsWithTheSameService(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var seen journal
		logKey := cordis.NewService[string]("log")
		if err := logKey.Provide(app.Scope(), "shared"); err != nil {
			t.Fatal(err)
		}
		for _, group := range []string{"one", "two"} {
			scope := app.Scope().Isolate(dbKey)
			load(scope, provider("db-"+group, dbKey, database(sqlite(group))))
			load(scope, plugin("reader-"+group, []cordis.Requirement{dbKey, logKey}, func(_ context.Context, s *cordis.Scope) error {
				db, err := dbKey.Get(s)
				if err != nil {
					return err
				}
				log, err := logKey.Get(s)
				if err != nil {
					return err
				}
				seen.add(group + " -> " + db.Name() + ", " + log)
				return nil
			}))
		}
		synctest.Wait()
		got := seen.list()
		slices.Sort(got)
		if !slices.Equal(got, []string{"one -> one, shared", "two -> two, shared"}) {
			t.Fatalf("bindings = %v", got)
		}
	})
}

func TestIsolationDoesNotFallBack(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var seen journal
		load(app.Scope(), provider("root-db", dbKey, database(sqlite("root"))))
		inside := load(app.Scope().Isolate(dbKey), reader("inside", &seen))
		synctest.Wait()
		status := inside.Status()
		if status.State != cordis.Waiting || len(status.Missing) != 1 || status.Missing[0].Service != "db" {
			t.Fatalf("status = %+v", status)
		}
	})
}

func TestSharedRealm(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var seen journal
		realm := cordis.NewRealm("tenant")
		left, right := app.Scope().IsolateIn(realm, dbKey), app.Scope().IsolateIn(realm, dbKey)
		load(left, provider("db", dbKey, database(sqlite("tenant"))))
		inRight := load(right, reader("right", &seen))
		outside := load(app.Scope(), reader("outside", &seen))
		synctest.Wait()
		mustWait(t, ctx, inRight, cordis.Active)
		mustWait(t, ctx, outside, cordis.Waiting)
		seen.expect(t, "right -> tenant")
	})
}

func TestIsolatedGroupsRestartIndependently(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var seen journal
		one, two := app.Scope().Isolate(dbKey), app.Scope().Isolate(dbKey)
		dbOne := load(one, provider("db-one", dbKey, database(sqlite("one"))))
		load(two, provider("db-two", dbKey, database(sqlite("two"))))
		readerOne, readerTwo := load(one, reader("r1", &seen)), load(two, reader("r2", &seen))
		synctest.Wait()
		if err := dbOne.Restart(ctx); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		mustWait(t, ctx, readerOne, cordis.Active)
		if one, two := readerOne.Status().Activations, readerTwo.Status().Activations; one != 2 || two != 1 {
			t.Fatalf("activations = %d and %d, want 2 and 1", one, two)
		}
	})
}

func TestIsolateByName(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var seen journal
		load(app.Scope(), provider("root-db", dbKey, database(sqlite("root"))))
		scope := app.Scope().Isolate(cordis.Name("db"))
		load(scope, provider("inner-db", dbKey, database(sqlite("inner"))))
		inner := load(scope, reader("inner", &seen))
		synctest.Wait()
		mustWait(t, ctx, inner, cordis.Active)
		seen.expect(t, "inner -> inner")
	})
}

var level = cordis.NewSetting[string](dbKey)

func TestSettingsOuterToInner(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		inner := level.With(level.With(app.Scope(), "outer"), "inner")
		if got := level.Values(inner); !slices.Equal(got, []string{"outer", "inner"}) {
			t.Fatalf("values = %v", got)
		}
	})
}

func TestDefaultSettingComesFromNearestDeclarer(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		load(app.Scope(), provider("db", dbKey, database(sqlite("one"))))
		var plain, own []string
		outer := load(app.Scope(), plugin("outer", []cordis.Requirement{level.Default("a")}, func(_ context.Context, s *cordis.Scope) error {
			load(s, plugin("plain", nil, func(_ context.Context, s *cordis.Scope) error {
				plain = level.Values(s)
				return nil
			}))
			load(s, plugin("own", []cordis.Requirement{level.Default("b")}, func(_ context.Context, s *cordis.Scope) error {
				own = level.Values(s)
				return nil
			}))
			return nil
		}))
		synctest.Wait()
		mustWait(t, ctx, outer, cordis.Active)
		synctest.Wait()
		if !slices.Equal(plain, []string{"a"}) || !slices.Equal(own, []string{"b"}) {
			t.Fatalf("plain = %v, own = %v", plain, own)
		}
	})
}

func TestIsolationCutsOnlyDefaultSettings(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		load(app.Scope(), provider("db", dbKey, database(sqlite("one"))))
		var values []string
		outer := load(app.Scope(), plugin("outer", []cordis.Requirement{level.Default("default")}, func(_ context.Context, s *cordis.Scope) error {
			inner := level.With(s, "attached").Isolate(dbKey)
			load(inner, plugin("inner", nil, func(_ context.Context, s *cordis.Scope) error {
				values = level.Values(s)
				return nil
			}))
			return nil
		}))
		synctest.Wait()
		mustWait(t, ctx, outer, cordis.Active)
		synctest.Wait()
		if !slices.Equal(values, []string{"attached"}) {
			t.Fatalf("values = %v, want only the attached setting", values)
		}
	})
}
