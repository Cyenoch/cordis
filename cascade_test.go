package cordis

import (
	"context"
	"errors"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
	"sync/atomic"
	"testing"
)

func TestUninstallCascadeConfirmsTargetAndPreflightsDataBeforeStop(t *testing.T) {
	h, p1, p2 := lifecycleHost(t)
	instance, err := h.Enable(t.Context(), contract.Entry{ID: "target", Package: p1.ID, Config: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err = instance.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	called := false
	confirm := func(_ context.Context, plan CascadePlan) error {
		called = true
		if plan.Action != packagehost.RemovePackage || len(plan.Entries) != 1 || plan.Entries[0].ID != "target" {
			t.Fatalf("bad plan: %+v", plan)
		}
		plan.Entries[0].ID = "mutated"
		plan.Entries[0].Config[0] = '!'
		return nil
	}
	if _, err = h.UninstallCascade(t.Context(), p1, true, confirm); err == nil {
		t.Fatal("shared data deletion accepted with another installed version")
	}
	if called || instance.Status().State != contract.Active {
		t.Fatal("preflight failure retired target")
	}
	if _, err = h.UninstallCascade(t.Context(), p2, false, nil); err == nil {
		t.Fatal("missing confirmation accepted")
	}
	if instance.Status().State != contract.Active {
		t.Fatal("unrelated version removal stopped selected code")
	}
	if _, err = h.UninstallCascade(t.Context(), p1, false, confirm); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("no explicit confirmation")
	}
	if _, err = h.Status("target"); err == nil {
		t.Fatal("removed target retained in facade")
	}
	if _, err = h.Store().Selected(p1.ID); err == nil {
		t.Fatal("selection retained")
	}
}

func TestCascadeDeniedHasNoSideEffects(t *testing.T) {
	h, p1, _ := lifecycleHost(t)
	instance, err := h.Enable(t.Context(), contract.Entry{ID: "target", Package: p1.ID, Config: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err = instance.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	denied := errors.New("denied")
	if _, err = h.UninstallCascade(t.Context(), p1, false, func(context.Context, CascadePlan) error { return denied }); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	if instance.Status().State != contract.Active {
		t.Fatal("denied cascade retired target")
	}
	plan, err := h.PlanCascade(t.Context(), p1, packagehost.SelectPackage)
	if err != nil || len(plan.Entries) != 0 {
		t.Fatal("same-version replace planned removal", plan, err)
	}
}

func TestReplaceCascadeReservesTargetTreeWhileDisposingChild(t *testing.T) {
	h, p1, p2 := lifecycleHost(t)
	parent, err := h.Enable(t.Context(), contract.Entry{ID: "target", Package: p1.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var starts atomic.Int32
	child, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "child", Parent: "target", Config: []byte(`1`)}, contract.Definition{Start: func(_ context.Context, s contract.Scope) error {
		if starts.Add(1) == 1 {
			return s.Defer(func(context.Context) error { close(entered); <-release; return nil })
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- h.ReplaceCascade(t.Context(), p2, packagehost.ChangeOptions{}, func(context.Context, CascadePlan) error { return nil })
	}()
	<-entered
	if _, err := parent.Update(t.Context(), []byte(`{}`)); err == nil {
		close(release)
		t.Fatal("cascade admitted an update to snapshotted target")
	}
	if err := h.Resume(t.Context(), "target"); err == nil {
		close(release)
		t.Fatal("cascade admitted conflicting target control")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	current, err := h.Instance("target")
	if err != nil || current == parent {
		t.Fatal("replacement did not install new handle", err)
	}
	if _, err := parent.Update(t.Context(), []byte(`{}`)); err == nil {
		t.Fatal("stale handle updated replacement")
	}
}
