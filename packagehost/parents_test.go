//go:build integration

package packagehost

import (
	"context"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestStoreExternalParentPersistsWithoutBuiltinEntry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	archive, p := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
	root := t.TempDir()
	s := openFixture(t, root, trust)
	if _, err := s.Install(ctx, archive); err != nil {
		t.Fatal(err)
	}
	if err := s.Select(ctx, p.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	entry := contract.Entry{ID: "child", Parent: "builtin", Package: "demo", Definition: "main", Config: []byte(`{}`)}
	if err := s.SaveEntry(ctx, entry); err == nil {
		t.Fatal("accepted unregistered parent")
	}
	if err := s.RegisterParent(ctx, "builtin"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEntry(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, root, trust)
	entries, err := s.Entries()
	if err != nil || len(entries) != 1 || entries[0].Parent != "builtin" {
		t.Fatalf("parent anchor not recovered independently: %+v, %v", entries, err)
	}
	if err := s.UnregisterParent(ctx, "builtin"); err == nil {
		t.Fatal("orphaned disabled child")
	}
	if err := s.RegisterParent(ctx, entry.ID); err == nil {
		t.Fatal("anchor replaced real entry")
	}
	conflict := entry
	conflict.ID, conflict.Parent = "builtin", ""
	if err := s.SaveEntry(ctx, conflict); err == nil {
		t.Fatal("real entry replaced anchor")
	}
	if err := s.DeleteEntry(ctx, entry.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UnregisterParent(ctx, "builtin"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEntry(ctx, entry); err == nil {
		t.Fatal("removed anchor still accepted")
	}
}
