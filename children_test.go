package cordis_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

func TestAuthorChildWaitsForDependencyAndIsRemovedWithParent(t *testing.T) {
	h, err := cordis.Open(cordis.Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := h.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	service := contract.Service{Name: "late", Major: 1}
	var starts, stops atomic.Int32
	created := make(chan contract.Instance, 2)
	var old contract.Scope
	parent, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent"}, contract.Definition{Children: []contract.Definition{{ID: "optional", Requires: []contract.Requirement{{Name: "late", Major: 1}}, Start: func(_ context.Context, s contract.Scope) error {
		starts.Add(1)
		return s.Defer(func(context.Context) error { stops.Add(1); return nil })
	}}}, Start: func(ctx context.Context, s contract.Scope) error {
		old = s
		i, err := contract.CreateChild(ctx, s, contract.Child{ID: "optional", Definition: "optional"})
		if err == nil {
			created <- i
		}
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	child := <-created
	if child.Status().State != contract.Waiting || starts.Load() != 0 {
		t.Fatal("optional child started without dependency")
	}
	provider, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "provider"}, contract.Definition{Provides: []contract.Service{service}, Start: func(_ context.Context, s contract.Scope) error {
		return s.Provide(service, func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = provider.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = child.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = h.Disable(t.Context(), "parent"); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 1 || stops.Load() != 1 {
		t.Fatalf("starts=%d stops=%d", starts.Load(), stops.Load())
	}
	if _, err = contract.CreateChild(t.Context(), old, contract.Child{ID: "stale", Definition: "optional"}); err == nil {
		t.Fatal("stale owner created a child")
	}
}

func TestAuthorChildWaitDuringInitializeFailsInsteadOfDeadlocking(t *testing.T) {
	h, err := cordis.Open(cordis.Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := h.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	instance, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent"}, contract.Definition{Children: []contract.Definition{{ID: "child", Start: func(context.Context, contract.Scope) error { return nil }}}, Start: func(ctx context.Context, s contract.Scope) error {
		child, err := contract.CreateChild(ctx, s, contract.Child{ID: "optional", Definition: "child"})
		if err != nil {
			return err
		}
		if err = child.Wait(ctx); err == nil {
			return fmt.Errorf("initialize unexpectedly waited for owned child")
		}
		var coded *contract.Error
		if !errors.As(err, &coded) || coded.Code != contract.Reentrant {
			return err
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = instance.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateChildDefinitionDoesNotStartEitherImplementation(t *testing.T) {
	h, err := cordis.Open(cordis.Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := h.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	var starts atomic.Int32
	start := func(context.Context, contract.Scope) error {
		starts.Add(1)
		return nil
	}
	parent, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent"}, contract.Definition{
		Children: []contract.Definition{
			{ID: "feature", Start: start},
			{ID: "feature", Start: start},
		},
		Start: func(ctx context.Context, scope contract.Scope) error {
			_, err := contract.CreateChild(ctx, scope, contract.Child{ID: "child", Definition: "feature"})
			if !errors.Is(err, &contract.Error{Code: contract.Conflict}) {
				return fmt.Errorf("expected duplicate definition conflict, got %v", err)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 0 {
		t.Fatal("ambiguous child definition started")
	}
}
