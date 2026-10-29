//go:build integration

package packagehost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestOfflineIntentSubtreeCommitAndValidation(t *testing.T) {
	key, trust := signingFixture(t)
	archive, pkg := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
	s := openFixture(t, t.TempDir(), trust)
	if _, err := s.Install(t.Context(), archive); err != nil {
		t.Fatal(err)
	}
	if err := s.Select(t.Context(), pkg.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []contract.Entry{
		{ID: "root"}, {ID: "child", Parent: "root"}, {ID: "leaf", Parent: "child"}, {ID: "other"},
	} {
		entry.Package = "demo"
		if _, err := s.EnableEntry(t.Context(), entry); err != nil {
			t.Fatal(err)
		}
	}
	for _, invalid := range []contract.Entry{
		{ID: "invalid", Package: "demo", Config: json.RawMessage(`[]`)},
		{ID: "orphan", Package: "demo", Parent: "absent"},
		{ID: "root", Package: "demo", Parent: "leaf"},
		{ID: "invalid", Package: "demo", Isolate: []string{"db"}},
	} {
		if _, err := s.EnableEntry(t.Context(), invalid); err == nil {
			t.Fatalf("invalid intent accepted: %+v", invalid)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.commitObserver = func(stage commitStage) {
		if stage == commitCatalogSynced {
			cancel()
		}
	}
	if err := s.DisableEntry(ctx, "root"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled disable: %v", err)
	}
	s.commitObserver = nil
	entries, err := s.Entries()
	if err != nil || len(entries) != 4 {
		t.Fatalf("invalid/canceled intent changed catalog: %+v %v", entries, err)
	}
	for _, entry := range entries {
		if !entry.Enabled {
			t.Fatal("partial subtree disable committed")
		}
	}
	if err := s.DeleteEntry(t.Context(), "root"); err == nil {
		t.Fatal("deleted enabled entry")
	}
	if err := s.DisableEntry(t.Context(), "root"); err != nil {
		t.Fatal(err)
	}
	entries, err = s.Entries()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Enabled != (entry.ID == "other") {
			t.Fatalf("subtree disable affected wrong intent: %+v", entry)
		}
	}
	if err := s.DeleteEntry(t.Context(), "root"); err == nil {
		t.Fatal("deleted parent of retained children")
	}
	for _, id := range []string{"leaf", "child", "root"} {
		if err := s.DeleteEntry(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpdateTrust(TrustPolicy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnableEntry(t.Context(), contract.Entry{ID: "untrusted", Package: "demo"}); err == nil {
		t.Fatal("enabled selected code without current trust")
	}
	if runs, err := s.Runs(); err != nil || len(runs) != 0 {
		t.Fatalf("intent operations executed code: %+v %v", runs, err)
	}
}
