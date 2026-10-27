package cordis

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

func TestControlChildUpdateWaitGraphSurvivesRestart(t *testing.T) {
	h, err := Open(Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := h.Close(context.Background()); err != nil {
			var coded *contract.Error
			if !errors.As(err, &coded) || coded.Code != contract.Reentrant {
				t.Error(err)
			}
		}
	}()
	svc := contract.Service{Name: "parent.api", Major: 1}
	var child contract.ChildInstance
	if err := h.RegisterDefinition(t.Context(), "builtin", contract.Definition{ID: "child", Requires: []contract.Requirement{{Name: svc.Name, Major: 1}}, Start: func(ctx context.Context, s contract.Scope) error {
		_, err := s.Call(ctx, contract.Requirement{Name: svc.Name, Major: 1}, json.RawMessage(`"initialize"`))
		return err
	}}); err != nil {
		t.Fatal(err)
	}
	parent, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent"}, contract.Definition{Provides: []contract.Service{svc}, Start: func(ctx context.Context, s contract.Scope) error {
		var err error
		child, err = contract.CreateChild(ctx, s, contract.Child{ID: "child", Definition: "child"})
		if err != nil {
			return err
		}
		return s.Provide(svc, func(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
			if string(in) == `"update"` {
				_, err := child.Update(ctx, json.RawMessage(`{"version":2}`))
				return nil, err
			}
			return json.RawMessage(`{}`), nil
		})
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err = h.Call(ctx, "", contract.Requirement{Name: svc.Name, Major: 1}, json.RawMessage(`"update"`))
	var coded *contract.Error
	if !errors.As(err, &coded) || coded.Code != contract.Reentrant {
		t.Fatalf("restart wait cycle was not rejected: %v", err)
	}
}

// Stopping a child is valid even while its parent is still initializing.
func TestControlInitializeCanStopUnpublishedChild(t *testing.T) {
	h, err := Open(Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	if err := h.RegisterDefinition(t.Context(), "builtin", contract.Definition{ID: "child", Start: func(context.Context, contract.Scope) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	parent, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent"}, contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
		child, err := contract.CreateChild(ctx, s, contract.Child{ID: "child", Definition: "child"})
		if err != nil {
			return err
		}
		return child.Stop(ctx)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(t.Context()); err != nil {
		t.Fatalf("stopping unpublished optional child failed parent: %v", err)
	}
}
