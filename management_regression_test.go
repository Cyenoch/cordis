package cordis

import (
	"context"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestDynamicIDReservedUntilMetadataRemoval(t *testing.T) {
	h, err := Open(Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	created := make(chan contract.ChildInstance, 1)
	leaf := contract.Definition{ID: "child", Start: func(context.Context, contract.Scope) error { return nil }}
	parent, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent"}, contract.Definition{
		Children: []contract.Definition{leaf},
		Start: func(ctx context.Context, scope contract.Scope) error {
			child, err := contract.CreateChild(ctx, scope, contract.Child{ID: "child", Definition: "child"})
			if err == nil {
				created <- child
			}
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := (<-created).Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Reproduce the gap between core removal and Host metadata removal without
	// a timing-dependent scheduler race or a production-only test hook.
	if err := h.engine.Remove(t.Context(), "parent.child"); err != nil {
		t.Fatal(err)
	}
	_, conflict := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent.child"}, leaf)
	h.mu.Lock()
	delete(h.dynamic, "parent.child")
	h.mu.Unlock()
	if conflict == nil {
		if err := h.Disable(t.Context(), "parent.child"); err != nil {
			t.Error(err)
		}
		t.Fatal("registration reused a child ID before ownership metadata removal")
	}
	if contract.ErrorCode(conflict) != contract.Conflict {
		t.Fatalf("reserved ID: %v", conflict)
	}
	replacement, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent.child"}, leaf)
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if entries := h.Instances(); len(entries) != 2 {
		t.Fatalf("stale child metadata produced duplicate instances: %+v", entries)
	}
	if err := h.Remove(t.Context(), "parent.child"); err != nil {
		t.Fatalf("new registration misidentified as old dynamic child: %v", err)
	}
}

func TestRemoveUsesBuiltinOriginInsteadOfPackageName(t *testing.T) {
	for _, parentID := range []string{"", "parent"} {
		t.Run(map[bool]string{false: "root", true: "persistent-parent"}[parentID != ""], func(t *testing.T) {
			h, pkg, _ := lifecycleHost(t)
			if parentID != "" {
				parent, err := h.Enable(t.Context(), contract.Entry{ID: parentID, Package: pkg.ID})
				if err != nil {
					t.Fatal(err)
				}
				if err := parent.Wait(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			instance, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "helper", Package: "app.helpers", Parent: parentID},
				contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }})
			if err != nil {
				t.Fatal(err)
			}
			if err := instance.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
			target := parentID
			if target == "" {
				target = "helper"
			}
			if err := h.Remove(t.Context(), target); err != nil {
				t.Fatalf("remove custom-package builtin: %v", err)
			}
			if _, err := h.Instance("helper"); err == nil {
				t.Fatal("removed builtin remained registered")
			}
			if entries, err := h.Store().Entries(); err != nil || len(entries) != 0 {
				t.Fatalf("remove left persistent parent intent: %+v %v", entries, err)
			}
		})
	}
}
