package cordis

import (
	"context"
	"errors"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
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
