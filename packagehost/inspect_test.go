//go:build integration

package packagehost

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestInspectDoesNotGrantTrust(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	archive := filepath.Join(t.TempDir(), "package.zip")
	source := sourceFixture(t, manifestFixture("demo", "1.0.0"))
	packed, err := Pack(ctx, source, archive)
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := Inspect(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Ref != packed.Ref || inspected.Directory != "" {
		t.Fatalf("wrong inspection identity: %+v", inspected)
	}
	store := openFixture(t, t.TempDir(), TrustPolicy{})
	if _, err = store.Install(ctx, archive); err == nil {
		t.Fatal("inspection implicitly trusted unsigned archive")
	}
}

func TestInspectRejectsIncompleteIntegrity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := sourceFixture(t, manifestFixture("demo", "1.0.0"))
	if err := generateIntegrity(ctx, dir, DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unlisted"), []byte("hidden"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "unlisted.zip")
	if err := writeZIP(ctx, dir, archive, DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(ctx, archive); err == nil {
		t.Fatal("unlisted file accepted")
	}
	if err := os.Remove(filepath.Join(dir, "unlisted")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "plugin.wasm")); err != nil {
		t.Fatal(err)
	}
	if err := writeZIP(ctx, dir, archive, DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(ctx, archive); err == nil {
		t.Fatal("missing listed file accepted")
	}
}

func TestStoreRejectsSigningKeyBindingAndConfigBeforeExecution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	archive, _ := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
	wrong := trust.Keys["test"]
	wrong.Publisher = "other"
	trust.Keys["test"] = wrong
	store := openFixture(t, t.TempDir(), trust)
	if _, err := store.Install(ctx, archive); err == nil {
		t.Fatal("key publisher mismatch accepted")
	}
	wrong.Publisher = "example"
	wrong.Revoked = true
	trust.Keys["test"] = wrong
	if err := store.UpdateTrust(trust); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Install(ctx, archive); err == nil {
		t.Fatal("revoked key accepted")
	}
	wrong.Revoked = false
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrong.PublicKey = pub
	trust.Keys["test"] = wrong
	if err = store.UpdateTrust(trust); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Install(ctx, archive); err == nil {
		t.Fatal("wrong signature accepted")
	}
	trust.Keys["test"] = TrustedKey{PublicKey: key.Public().(ed25519.PublicKey), Publisher: "example"}
	if err = store.UpdateTrust(trust); err != nil {
		t.Fatal(err)
	}
	m := manifestFixture("demo", "2.0.0")
	m.ConfigSchema = json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"],"additionalProperties":false}`)
	validArchive, _ := archiveFixture(t, m, key)
	p, err := store.Install(ctx, validArchive)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Select(ctx, p.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	entry := contract.Entry{ID: "entry", Package: "demo", Definition: "main", Realm: "root", Enabled: true, Config: json.RawMessage(`{"count":"bad"}`)}
	if err = store.SaveEntry(ctx, entry); err == nil {
		t.Fatal("invalid configuration accepted before startup")
	}
	entry.Config = json.RawMessage(`{"count":1}`)
	if err = store.SaveEntry(ctx, entry); err != nil {
		t.Fatal(err)
	}
}
