package access_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Cyenoch/cordis/access"
	"github.com/Cyenoch/cordis/contract"
)

func TestContributionBatchInvalidIsEntirelyInvisible(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("first")
	activate(t, policy, owner)
	store := access.NewContributionStore(policy)
	bad := errors.New("host schema rejected")
	if err := store.Register("menu", func(_ context.Context, _ contract.Identity, item contract.Contribution) error {
		if item.ID == "invalid" {
			return bad
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	valid := []contract.Contribution{{Slot: "menu", ID: "existing", Data: json.RawMessage(`{}`)}}
	batch, err := store.Stage(t.Context(), owner, valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatal(err)
	}
	invalid := []contract.Contribution{
		{Slot: "menu", ID: "one", Data: json.RawMessage(`{}`)},
		{Slot: "menu", ID: "invalid", Data: json.RawMessage(`{}`)},
		{Slot: "menu", ID: "three", Data: json.RawMessage(`{}`)},
	}
	if _, err := store.Stage(t.Context(), owner, invalid); !errors.Is(err, bad) {
		t.Fatalf("validator error lost: %v", err)
	}
	visible := store.Snapshot()
	if len(visible) != 1 || visible[0].Contribution.ID != "existing" {
		t.Fatalf("partial batch visible: %#v", visible)
	}
}

func TestContributionOldActivationCannotRegisterOrCommit(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	old, current := identity("old"), identity("new")
	activate(t, policy, old)
	store := access.NewContributionStore(policy)
	if err := store.Register("menu", func(context.Context, contract.Identity, contract.Contribution) error { return nil }); err != nil {
		t.Fatal(err)
	}
	items := []contract.Contribution{{Slot: "menu", ID: "one", Data: json.RawMessage(`{}`)}}
	batch, err := store.Stage(t.Context(), old, items)
	if err != nil {
		t.Fatal(err)
	}
	activate(t, policy, current)
	if err := batch.Commit(); err == nil {
		t.Fatal("old batch committed")
	}
	if _, err := store.Stage(t.Context(), old, items); err == nil {
		t.Fatal("old activation registered")
	}
	if len(store.Snapshot()) != 0 {
		t.Fatal("retired contribution remains")
	}
	if err := policy.Activate(old, nil); err == nil {
		t.Fatal("reused activation identity accepted")
	}
}

func TestContributionPublicationGateIsBatchWide(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("first")
	activate(t, policy, owner)
	store := access.NewContributionStore(policy)
	var active atomic.Bool
	if err := store.SetActiveGate(func(contract.Identity) bool { return active.Load() }); err != nil {
		t.Fatal(err)
	}
	if err := store.Register("menu", func(context.Context, contract.Identity, contract.Contribution) error { return nil }); err != nil {
		t.Fatal(err)
	}
	batch, err := store.Stage(t.Context(), owner, []contract.Contribution{
		{Slot: "menu", ID: "one", Data: json.RawMessage(`{}`)},
		{Slot: "menu", ID: "two", Data: json.RawMessage(`{}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(store.Snapshot()) != 0 {
		t.Fatal("snapshot visible before core commit")
	}
	active.Store(true)
	if len(store.Snapshot()) != 2 {
		t.Fatal("committed batch not visible")
	}
	if err := policy.Revoke(owner); err != nil {
		t.Fatal(err)
	}
	if len(store.Snapshot()) != 0 {
		t.Fatal("revoked batch visible")
	}
}
