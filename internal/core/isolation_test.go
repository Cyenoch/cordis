package core_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func scoped(t *testing.T, e *core.Engine, entry contract.Entry, definition contract.Definition) contract.Instance {
	t.Helper()
	entry.Enabled = true
	instance, err := e.Add(entry, definition, contract.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func TestIsolationGroupsShareOnlyUnisolatedKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		empty := contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }}
		log := contract.Service{Name: "log", Major: 1}
		logReq := contract.Requirement{Name: log.Name, Major: log.Major}
		for _, entry := range []contract.Entry{
			{ID: "a", Isolate: []string{service.Key()}},
			{ID: "b", Isolate: []string{service.Key()}},
			{ID: "nested", Parent: "a", Isolate: []string{service.Key()}},
		} {
			if err := scoped(t, e, entry, empty).Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		global := add(t, e, "global-log", contract.Definition{Provides: []contract.Service{log},
			Start: func(_ context.Context, scope contract.Scope) error {
				return scope.Provide(log, func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`true`), nil })
			}})
		if err := global.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := add(t, e, "root-store", provider(`0`)).Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		providers := map[string]contract.Instance{}
		for _, group := range []string{"a", "b"} {
			p := scoped(t, e, contract.Entry{ID: group + "-store", Parent: group, Config: json.RawMessage(`1`)},
				contract.Definition{Provides: []contract.Service{service}, Start: func(_ context.Context, scope contract.Scope) error {
					return scope.Provide(service, func(context.Context, json.RawMessage) (json.RawMessage, error) { return scope.Config(), nil })
				}})
			if err := p.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
			providers[group] = p
		}
		var startsA, startsB atomic.Int32
		cleaned := make(chan json.RawMessage, 8)
		consumer := func(starts *atomic.Int32) contract.Definition {
			return contract.Definition{Requires: []contract.Requirement{requirement, logReq},
				Start: func(ctx context.Context, scope contract.Scope) error {
					if _, err := scope.Call(ctx, logReq, nil); err != nil {
						return err
					}
					starts.Add(1)
					return scope.Defer(func(ctx context.Context) error {
						value, err := scope.Call(ctx, requirement, nil)
						cleaned <- value
						return err
					})
				}}
		}
		a := scoped(t, e, contract.Entry{ID: "a-consumer", Parent: "a"}, consumer(&startsA))
		b := scoped(t, e, contract.Entry{ID: "b-consumer", Parent: "b"}, consumer(&startsB))
		for _, i := range []contract.Instance{a, b} {
			if err := i.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		view, err := e.Caller("a-consumer")
		if err != nil {
			t.Fatal(err)
		}
		if value, err := e.Call(t.Context(), requirement, nil); err != nil || string(value) != `0` {
			t.Fatalf("root call escaped root space: %s %v", value, err)
		}
		if _, err := providers["a"].Update(t.Context(), json.RawMessage(`2`)); err != nil {
			t.Fatal(err)
		}
		if err := a.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if startsA.Load() != 2 || startsB.Load() != 1 || string(<-cleaned) != `1` {
			t.Fatal("update retired wrong group or discarded original cleanup binding")
		}
		if value, err := view.Call(t.Context(), requirement, nil); err != nil || string(value) != `2` {
			t.Fatalf("registered view did not resolve new provider: %s %v", value, err)
		}
		nested := scoped(t, e, contract.Entry{ID: "nested-consumer", Parent: "nested"},
			contract.Definition{Requires: []contract.Requirement{requirement}, Start: empty.Start})
		synctest.Wait()
		status := nested.Status()
		if status.State != contract.Waiting || len(status.Waiting) != 1 || len(status.Waiting[0].Visible) != 0 {
			t.Fatalf("nested boundary leaked outer provider: %+v", status)
		}
		if err := scoped(t, e, contract.Entry{ID: "nested-store", Parent: "nested"}, provider(`3`)).Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := nested.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		nestedView, err := e.Caller("nested-consumer")
		if err != nil {
			t.Fatal(err)
		}
		if value, err := nestedView.Call(t.Context(), requirement, nil); err != nil || string(value) != `3` {
			t.Fatalf("nested resolution: %s %v", value, err)
		}
		major2 := service
		major2.Major = 2
		if err := add(t, e, "root-major2", contract.Definition{Provides: []contract.Service{major2},
			Start: func(_ context.Context, scope contract.Scope) error {
				return scope.Provide(major2, func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`4`), nil })
			}}).Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		req2 := requirement
		req2.Major = 2
		if value, err := view.Call(t.Context(), req2, nil); err != nil || string(value) != `4` {
			t.Fatalf("isolating major 1 also hid major 2: %s %v", value, err)
		}
		if err := e.Remove(t.Context(), "a-consumer"); err != nil {
			t.Fatal(err)
		}
		if err := scoped(t, e, contract.Entry{ID: "a-consumer", Parent: "b"}, empty).Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := view.Call(t.Context(), requirement, nil); contract.ErrorCode(err) != contract.NotReady {
			t.Fatalf("old view controlled recreated registration: %v", err)
		}
	})
}

func TestIsolationCycleDetectionUsesResolvedSpaces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		x, y := contract.Service{Name: "x", Major: 1}, contract.Service{Name: "y", Major: 1}
		xr, yr := contract.Requirement{Name: "x", Major: 1}, contract.Requirement{Name: "y", Major: 1}
		empty := contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }}
		for _, group := range []string{"a", "b"} {
			if err := scoped(t, e, contract.Entry{ID: group, Isolate: []string{x.Key(), y.Key()}}, empty).Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		serve := func(service contract.Service, requirements ...contract.Requirement) contract.Definition {
			return contract.Definition{Provides: []contract.Service{service}, Requires: requirements,
				Start: func(_ context.Context, scope contract.Scope) error {
					return scope.Provide(service, func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`null`), nil })
				}}
		}
		a := scoped(t, e, contract.Entry{ID: "a-x", Parent: "a"}, serve(x, yr))
		b := scoped(t, e, contract.Entry{ID: "b-y", Parent: "b"}, serve(y, xr))
		synctest.Wait()
		if a.Status().State != contract.Waiting || b.Status().State != contract.Waiting {
			t.Fatal("unrelated spaces incorrectly formed a dependency cycle")
		}
		if err := scoped(t, e, contract.Entry{ID: "a-y", Parent: "a"}, serve(y)).Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := a.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if b.Status().State != contract.Waiting {
			t.Fatal("provider from sibling space satisfied dependency")
		}
	})
}

func TestIsolationRejectsDuplicateWithinOneSpace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer e.Close(t.Context())
		parent := scoped(t, e, contract.Entry{ID: "group", Isolate: []string{service.Key()}},
			contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }})
		if err := parent.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := scoped(t, e, contract.Entry{ID: "first", Parent: "group"}, provider(`1`)).Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		second := scoped(t, e, contract.Entry{ID: "second", Parent: "group"}, provider(`2`))
		if err := second.Wait(t.Context()); contract.ErrorCode(err) != contract.Conflict {
			t.Fatalf("duplicate isolated provider accepted: %v", err)
		}
	})
}
