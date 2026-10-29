package cordis_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

func TestHostManagesDynamicTreeWithoutPersistingChildren(t *testing.T) {
	h, err := cordis.Open(cordis.Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	created := make(chan contract.ChildInstance, 8)
	leaf := contract.Definition{ID: "leaf", Start: func(context.Context, contract.Scope) error { return nil }}
	feature := contract.Definition{ID: "feature", Children: []contract.Definition{leaf}, Start: func(ctx context.Context, s contract.Scope) error {
		child, err := contract.CreateChild(ctx, s, contract.Child{ID: "leaf", Definition: "leaf"})
		if err == nil {
			created <- child
		}
		return err
	}}
	parent, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent"}, contract.Definition{
		Children: []contract.Definition{feature, leaf},
		Start: func(ctx context.Context, s contract.Scope) error {
			for _, child := range []contract.Child{{ID: "feature", Definition: "feature"}, {ID: "sibling", Definition: "leaf"}} {
				instance, err := contract.CreateChild(ctx, s, child)
				if err != nil {
					return err
				}
				created <- instance
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitTree := func() {
		t.Helper()
		if err := parent.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if err := (<-created).Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
	}
	waitTree()
	old, err := h.Instance("parent.feature")
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range h.Instances() {
		if info.Entry.ID == "parent" {
			continue
		}
		if info.Origin != "dynamic" || info.Owner == nil || info.Owner.Activation == "" {
			t.Fatalf("missing dynamic ownership: %+v", info)
		}
	}
	if result, err := old.Update(t.Context(), json.RawMessage(`{"revision":2}`)); err != nil || !result.Applied {
		t.Fatalf("dynamic update: %+v %v", result, err)
	}
	if err := (<-created).Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := h.Store().Entries()
	if err != nil || len(entries) != 0 {
		t.Fatalf("dynamic update persisted intent: %+v %v", entries, err)
	}
	if err := old.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	sibling, err := h.Status("parent.sibling")
	if err != nil || sibling.State != contract.Active || parent.Status().State != contract.Active {
		t.Fatalf("stopping child damaged parent/sibling: %+v %v", sibling, err)
	}
	if _, err := parent.Update(t.Context(), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitTree()
	for _, operation := range []func() error{
		func() error { return old.Stop(t.Context()) },
		func() error { _, err := old.Update(t.Context(), json.RawMessage(`{}`)); return err },
		func() error { return h.Disable(t.Context(), "parent.feature") },
		func() error { return h.Remove(t.Context(), "parent.feature") },
		func() error { return h.Resume(t.Context(), "parent.feature") },
	} {
		if err := operation(); err == nil {
			t.Fatal("stale or persistent control accepted for dynamic child")
		}
	}
	current, err := h.Status("parent.feature")
	if err != nil || current.State != contract.Active || current.Identity.Activation == old.Status().Identity.Activation {
		t.Fatalf("replacement was affected by old handle: %+v %v", current, err)
	}
}

func TestQuiescingChildRemainsVisibleUntilCleanupEnds(t *testing.T) {
	h, err := cordis.Open(cordis.Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(context.Background()) })
	entered, release := make(chan struct{}), make(chan struct{})
	created := make(chan contract.ChildInstance, 1)
	parent, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent"}, contract.Definition{
		Children: []contract.Definition{{ID: "child", Start: func(_ context.Context, s contract.Scope) error {
			return s.Defer(func(context.Context) error { close(entered); <-release; return nil })
		}}},
		Start: func(ctx context.Context, s contract.Scope) error {
			child, err := contract.CreateChild(ctx, s, contract.Child{ID: "child", Definition: "child"})
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
	stopped := make(chan error, 1)
	go func() { stopped <- h.Disable(t.Context(), "parent") }()
	<-entered
	status, queryErr := h.Status("parent.child")
	listed := false
	for _, info := range h.Instances() {
		if info.Entry.ID == "parent.child" && info.Status.State == contract.Quiescing {
			listed = true
		}
	}
	close(release)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if queryErr != nil || status.State != contract.Quiescing || !listed {
		t.Fatalf("pending child was hidden: %+v %v listed=%v", status, queryErr, listed)
	}
	if _, err := h.Instance("parent.child"); err == nil {
		t.Fatal("disposed child remains registered")
	}
}

func TestHostDoneWaitsForCleanupAndStoreRelease(t *testing.T) {
	dir := t.TempDir()
	h, err := cordis.Open(cordis.Options{Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	t.Cleanup(func() { h.Close(context.Background()) })
	i, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "blocked"}, contract.Definition{Start: func(_ context.Context, s contract.Scope) error {
		return s.Defer(func(context.Context) error { <-release; return nil })
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	err = h.Close(ctx)
	cancel()
	select {
	case <-h.Done():
		close(release)
		t.Fatal("close completion preceded cleanup")
	default:
	}
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked cleanup: %v", err)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.Done():
	default:
		t.Fatal("drained host did not signal completion")
	}
	store, err := packagehost.Open(dir, packagehost.TrustPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}
