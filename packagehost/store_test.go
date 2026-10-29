//go:build integration

package packagehost

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func sourceFixture(t *testing.T, m contract.Manifest) string {
	t.Helper()
	dir := t.TempDir()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, ManifestFile), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "plugin"), []byte("fixture artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
func signingFixture(t *testing.T) (ed25519.PrivateKey, TrustPolicy) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key, TrustPolicy{Keys: map[string]TrustedKey{"test": {PublicKey: pub, Publisher: "example"}}, Publishers: map[string][]string{"example": {"demo", "dep"}}}
}
func archiveFixture(t *testing.T, m contract.Manifest, key ed25519.PrivateKey) (string, Package) {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "plugin.zip")
	if _, err := Pack(context.Background(), sourceFixture(t, m), archive); err != nil {
		t.Fatal(err)
	}
	p, err := Sign(context.Background(), archive, "test", key)
	if err != nil {
		t.Fatal(err)
	}
	return archive, p
}
func openFixture(t *testing.T, root string, trust TrustPolicy) *Store {
	t.Helper()
	s, err := Open(root, trust)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestStoreInstallRecoveryAndSelection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	root := t.TempDir()
	s := openFixture(t, root, trust)
	archive1, _ := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
	p1, err := s.Install(ctx, archive1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Selected("demo"); !errors.Is(err, ErrNotSelected) {
		t.Fatalf("installed without selection: %v", err)
	}
	if err = s.Select(ctx, p1.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveEntry(ctx, contract.Entry{ID: "entry", Package: "demo", Definition: "main", Config: []byte(`{}`), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	archive2, _ := archiveFixture(t, manifestFixture("demo", "2.0.0"), key)
	p2, err := s.Install(ctx, archive2)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, root, trust)
	selected, err := s.Selected("demo")
	if err != nil || selected.Ref != p1.Ref {
		t.Fatalf("install changed selection: %v %v", selected, err)
	}
	installed, err := s.Installed()
	if err != nil || len(installed) != 2 {
		t.Fatalf("lost installed versions: %v %v", installed, err)
	}
	entries, err := s.Entries()
	if err != nil || len(entries) != 1 || !entries[0].Enabled {
		t.Fatalf("lost durable entry: %v %v", entries, err)
	}
	if err = s.CheckSelect(t.Context(), p2.Ref, ChangeOptions{InUse: []contract.PackageRef{p1.Ref}}); err != nil {
		t.Fatal("preflight incorrectly required stopping old activation", err)
	}
	unchanged, err := s.Selected("demo")
	if err != nil || unchanged.Ref != p1.Ref {
		t.Fatal("CheckSelect mutated selection", err)
	}
	if err = s.Select(ctx, p2.Ref, ChangeOptions{InUse: []contract.PackageRef{p1.Ref}}); err == nil {
		t.Fatal("switched running activation")
	}
	result, err := s.Uninstall(ctx, p1.Ref, ChangeOptions{})
	if err != nil || !result.Removed {
		t.Fatalf("uninstall: %+v %v", result, err)
	}
}

func TestStoreSelectedAbsenceAndExplicitRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	root := t.TempDir()
	s := openFixture(t, root, trust)
	if _, err := s.Selected("demo"); !errors.Is(err, ErrNotSelected) {
		t.Fatalf("empty store selection: %v", err)
	}
	archive, _ := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
	p, err := s.Install(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, root, trust)
	if _, err = s.Selected("demo"); !errors.Is(err, ErrNotSelected) || !strings.Contains(err.Error(), "demo") {
		t.Fatalf("reopened without selection: %v", err)
	}
	if installed, err := s.Get(p.Ref); err != nil || installed.Ref != p.Ref {
		t.Fatalf("lost installed package before selection: %+v %v", installed, err)
	}
	if err = s.Select(ctx, p.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	entry := contract.Entry{ID: "entry", Package: "demo", Definition: "main", Config: []byte(`{}`), Enabled: false}
	if err = s.SaveEntry(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, root, trust)
	selected, err := s.Selected("demo")
	if err != nil || selected.Ref != p.Ref {
		t.Fatalf("explicit selection not preserved: %+v %v", selected, err)
	}
	entries, err := s.Entries()
	if err != nil || !reflect.DeepEqual(entries, []contract.Entry{entry}) {
		t.Fatalf("selection query changed disabled entry: %+v %v", entries, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"demo", "unknown"} {
		if _, err = s.Selected(id); !errors.Is(err, os.ErrClosed) || errors.Is(err, ErrNotSelected) {
			t.Fatalf("closed store selection %q: %v", id, err)
		}
	}
}

func TestStoreOpenReadFailuresAreNotUnselected(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"catalog unreadable", "committed content missing"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			key, trust := signingFixture(t)
			root := t.TempDir()
			s := openFixture(t, root, trust)
			archive, _ := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
			p, err := s.Install(ctx, archive)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Select(ctx, p.Ref, ChangeOptions{}); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			if name == "catalog unreadable" {
				file := filepath.Join(root, "catalog.json")
				if err = os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err = os.Mkdir(file, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err = os.RemoveAll(p.Directory); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(root, trust)
			if reopened != nil {
				if closeErr := reopened.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				t.Fatal("opened store despite unreadable committed state")
			}
			var pathErr *os.PathError
			if !errors.As(err, &pathErr) || errors.Is(err, ErrNotSelected) {
				t.Fatalf("read error misclassified as absent selection: %v", err)
			}
		})
	}
}

func TestStoreManifestCopiesDoNotAliasCatalog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	m := manifestFixture("demo", "1.0.0")
	m.Protocol.RequiredFeatures = []string{"cancel"}
	archive, _ := archiveFixture(t, m, key)
	root := t.TempDir()
	s := openFixture(t, root, trust)
	p, err := s.Install(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(p Package) {
		p.Manifest.Protocol.RequiredFeatures[0] = "unsupported"
	}
	mutate(p)
	if err := s.Select(ctx, p.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, lookup := range []func() (Package, error){
		func() (Package, error) { return s.Get(p.Ref) },
		func() (Package, error) { return s.Selected(p.Ref.ID) },
		func() (Package, error) { return s.Install(ctx, archive) },
	} {
		returned, err := lookup()
		if err != nil {
			t.Fatal(err)
		}
		mutate(returned)
	}
	installed, err := s.Installed()
	if err != nil {
		t.Fatal(err)
	}
	mutate(installed[0])
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, root, trust)
	got, err := s.Get(p.Ref)
	if err != nil || !reflect.DeepEqual(got.Manifest, m) {
		t.Fatalf("catalog metadata changed: %+v, %v", got.Manifest, err)
	}
}

func TestStoreResignedDigestRefreshesTrustDurably(t *testing.T) {
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
	pub, rotated, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := trust.Keys["test"]
	old.Revoked = true
	trust.Keys["test"] = old
	trust.Keys["rotated"] = TrustedKey{PublicKey: pub, Publisher: "example"}
	if err = s.UpdateTrust(trust); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Snapshot(ctx, p.Ref); err == nil {
		t.Fatal("revoked original proof allowed execution")
	}
	if _, err = Sign(ctx, archive, "rotated", rotated); err != nil {
		t.Fatal(err)
	}
	if refreshed, err := s.Install(ctx, archive); err != nil || refreshed.Ref != p.Ref {
		t.Fatalf("refresh changed identity or failed: %+v, %v", refreshed.Ref, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, root, trust)
	if err = s.CheckSelect(t.Context(), p.Ref, ChangeOptions{}); err != nil {
		t.Fatal("preflight used revoked content proof", err)
	}
	snapshot, err := s.Snapshot(ctx, p.Ref)
	if err != nil {
		t.Fatal(err)
	}
	_, input, sig, err := inspect(ctx, snapshot.Directory, "snapshot", false, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err = trust.verify(p.Manifest, p.Ref.Digest, input, sig, false); err != nil {
		t.Fatal("private snapshot did not carry current proof", err)
	}
	if err = snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = Sign(ctx, archive, "rotated", key); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Install(ctx, archive); err == nil {
		t.Fatal("invalid refresh signature accepted")
	}
	if err = s.CheckSelect(t.Context(), p.Ref, ChangeOptions{}); err != nil {
		t.Fatal("failed refresh replaced verified proof", err)
	}
	trust.Keys["rotated"] = old
	if err = s.UpdateTrust(trust); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Snapshot(ctx, p.Ref); err == nil {
		t.Fatal("catalog proof bypassed current revocation")
	}
}

func TestStoreUnsignedDigestCanAcquireSignature(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	archive := filepath.Join(t.TempDir(), "unsigned.zip")
	p, err := Pack(ctx, sourceFixture(t, manifestFixture("demo", "1.0.0")), archive)
	if err != nil {
		t.Fatal(err)
	}
	trust.DevDigests = map[string]bool{p.Ref.Digest: true}
	root := t.TempDir()
	s := openFixture(t, root, trust)
	if _, err = s.Install(ctx, archive); err != nil {
		t.Fatal(err)
	}
	if err = s.Select(ctx, p.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	trust.DevDigests = nil
	if err = s.UpdateTrust(trust); err != nil {
		t.Fatal(err)
	}
	if _, err = Sign(ctx, archive, "test", key); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Install(ctx, archive); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, root, trust)
	snapshot, err := s.Snapshot(ctx, p.Ref)
	if err != nil {
		t.Fatal("signed promotion still required development trust", err)
	}
	if snapshot.Package.Development {
		t.Fatal("signed archive retained development status")
	}
	if err = snapshot.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreTrustTamperAndConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	s := openFixture(t, t.TempDir(), trust)
	archive, _ := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
	p, err := s.Install(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Select(ctx, p.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot(ctx, p.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Directory == p.Directory {
		t.Fatal("executing mutable installed source")
	}
	if err = snap.Close(); err != nil {
		t.Fatal(err)
	}
	changedSource := sourceFixture(t, manifestFixture("demo", "1.0.0"))
	if err = os.WriteFile(filepath.Join(changedSource, "extra"), []byte("change"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := filepath.Join(t.TempDir(), "changed.zip")
	if _, err = Pack(ctx, changedSource, changed); err != nil {
		t.Fatal(err)
	}
	if _, err = Sign(ctx, changed, "test", key); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Install(ctx, changed); err == nil {
		t.Fatal("same version different content accepted")
	}
	revoked := trust
	revoked.RevokedPublishers = map[string]bool{"example": true}
	if err = s.UpdateTrust(revoked); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Snapshot(ctx, p.Ref); err == nil {
		t.Fatal("revoked publisher executed")
	}
	if err = s.UpdateTrust(trust); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(p.Directory, "plugin"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Snapshot(ctx, p.Ref); err == nil {
		t.Fatal("tampered package executed")
	}
}

func TestStorePublisherAuthorizationAndDevDigest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	s := openFixture(t, t.TempDir(), trust)
	archive, _ := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
	p, err := s.Install(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, otherKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust.Keys["other"] = TrustedKey{PublicKey: otherPub, Publisher: "other"}
	trust.Publishers["other"] = []string{"demo"}
	if err = s.UpdateTrust(trust); err != nil {
		t.Fatal(err)
	}
	m := manifestFixture("demo", "2.0.0")
	m.Publisher = "other"
	other := filepath.Join(t.TempDir(), "other.zip")
	if _, err = Pack(ctx, sourceFixture(t, m), other); err != nil {
		t.Fatal(err)
	}
	if _, err = Sign(ctx, other, "other", otherKey); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Install(ctx, other); err == nil {
		t.Fatal("another trusted publisher inherited plugin identity")
	}
	if _, err = s.Uninstall(ctx, p.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Install(ctx, other); err == nil {
		t.Fatal("retained data publisher identity lost after uninstall")
	}
	devSource := sourceFixture(t, manifestFixture("dep", "1.0.0"))
	devArchive := filepath.Join(t.TempDir(), "dev.zip")
	dev, err := Pack(ctx, devSource, devArchive)
	if err != nil {
		t.Fatal(err)
	}
	trust.DevDigests = map[string]bool{dev.Ref.Digest: true}
	if err = s.UpdateTrust(trust); err != nil {
		t.Fatal(err)
	}
	devInstalled, err := s.Install(ctx, devArchive)
	if err != nil {
		t.Fatal(err)
	}
	if !devInstalled.Development {
		t.Fatal("exact digest development install not attributed")
	}
	if _, err = s.ImportDirectory(ctx, devSource); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(devSource, "new"), []byte("not permitted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportDirectory(ctx, devSource); err == nil {
		t.Fatal("digest permission leaked to changed developer directory")
	}
}

func TestStorePublisherApprovalCompletesMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	root := t.TempDir()
	s := openFixture(t, root, trust)
	archive, old := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
	if _, err := s.Install(ctx, archive); err != nil {
		t.Fatal(err)
	}
	pub, nextKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust.Keys["next"] = TrustedKey{PublicKey: pub, Publisher: "next"}
	trust.Publishers["next"] = []string{"demo"}
	trust.PublisherChanges = map[string]map[string]bool{"demo": {"next": true}}
	if err = s.UpdateTrust(trust); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"2.0.0", "3.0.0"} {
		m := manifestFixture("demo", version)
		m.Publisher = "next"
		archive := filepath.Join(t.TempDir(), "next.zip")
		if _, err = Pack(ctx, sourceFixture(t, m), archive); err != nil {
			t.Fatal(err)
		}
		if _, err = Sign(ctx, archive, "next", nextKey); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Install(ctx, archive); err != nil {
			t.Fatalf("install successor %s: %v", version, err)
		}
		if version == "2.0.0" {
			trust.PublisherChanges = nil
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openFixture(t, root, trust)
		}
	}
	if _, err = s.Get(old.Ref); err != nil {
		t.Fatal("migration removed the old publisher's installed version", err)
	}
	rollback, _ := archiveFixture(t, manifestFixture("demo", "4.0.0"), key)
	if _, err = s.Install(ctx, rollback); err == nil {
		t.Fatal("old publisher reclaimed the package without a new approval")
	}
	trust.PublisherChanges = map[string]map[string]bool{"demo": {"example": true}}
	if err = s.UpdateTrust(trust); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Install(ctx, archive); err != nil {
		t.Fatalf("approved migration to an already installed digest: %v", err)
	}
	trust.PublisherChanges = nil
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, root, trust)
	if _, err = s.Install(ctx, rollback); err != nil {
		t.Fatalf("reinstall did not persist the migrated publisher: %v", err)
	}
}

func TestStoreSelectionChecksPropagateCancellation(t *testing.T) {
	t.Parallel()
	key, trust := signingFixture(t)
	archive, p := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
	for _, operation := range []string{"select", "check-select"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			s := openFixture(t, t.TempDir(), trust)
			if _, err := s.Install(t.Context(), archive); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cancel()
			var err error
			if operation == "select" {
				err = s.Select(ctx, p.Ref, ChangeOptions{})
			} else {
				err = s.CheckSelect(ctx, p.Ref, ChangeOptions{})
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("selection detached caller cancellation: %v", err)
			}
			if _, err := s.Selected(p.Ref.ID); !errors.Is(err, ErrNotSelected) {
				t.Fatalf("canceled selection changed state: %v", err)
			}
		})
	}
}

func TestStoreDependenciesAndLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	root := t.TempDir()
	s := openFixture(t, root, trust)
	if second, err := Open(root, trust); err == nil {
		if e := second.Close(); e != nil {
			t.Fatal(e)
		}
		t.Fatal("concurrent storage owner accepted")
	}
	dep1Archive, _ := archiveFixture(t, manifestFixture("dep", "1.0.0"), key)
	dep1, err := s.Install(ctx, dep1Archive)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Select(ctx, dep1.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	m := manifestFixture("demo", "1.0.0")
	m.Dependencies = []contract.Dependency{{ID: "dep", Min: "1.0.0", Max: "2.0.0"}}
	archive, _ := archiveFixture(t, m, key)
	p, err := s.Install(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Select(ctx, p.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	m = manifestFixture("dep", "2.0.0")
	dep2Archive, _ := archiveFixture(t, m, key)
	dep2, err := s.Install(ctx, dep2Archive)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckSelect(t.Context(), dep2.Ref, ChangeOptions{Enabled: []string{"demo"}}); err == nil {
		t.Fatal("pre-quiesce check missed reverse dependency")
	}
	if err = s.Select(ctx, dep2.Ref, ChangeOptions{Enabled: []string{"demo"}}); err == nil {
		t.Fatal("enabled reverse dependency broken")
	}
	if _, err = s.Uninstall(ctx, dep1.Ref, ChangeOptions{Enabled: []string{"demo"}}); err == nil {
		t.Fatal("removed required selected dependency")
	}
	if err = s.Select(ctx, dep2.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.Install(canceled, dep2Archive); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled install: %v", err)
	}
}

func TestStoreNestedDocumentsRemainReopenable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	root := t.TempDir()
	s := openFixture(t, root, trust)
	m := manifestFixture("demo", "1.0.0")
	archive, _ := archiveFixture(t, m, key)
	p, err := s.Install(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Select(ctx, p.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	config := "{}"
	entry := contract.Entry{ID: "nested", Package: "demo", Definition: "main", Config: []byte(config), Enabled: true}
	if err = s.SaveEntry(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry.Config = []byte(strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33))
	if err = s.SaveEntry(ctx, entry); err == nil || !strings.Contains(err.Error(), "nesting limit") {
		t.Fatalf("catalog envelope budget relaxed the configuration leaf limit: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, root, trust)
	entries, err := s.Entries()
	if err != nil || len(entries) != 1 || string(entries[0].Config) != config {
		t.Fatalf("nested configuration not recovered: %+v, %v", entries, err)
	}
	selected, err := s.Selected("demo")
	if err != nil || selected.Ref != p.Ref {
		t.Fatalf("nested manifest not recovered: %+v, %v", selected, err)
	}
	snapshot, err := s.Snapshot(ctx, p.Ref)
	if err != nil {
		t.Fatal("recovered nested documents cannot execute", err)
	}
	if err = snapshot.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreSaveEntryPersistsOmittedObjectAndExplicitNull(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	root := t.TempDir()
	s := openFixture(t, root, trust)
	m := manifestFixture("demo", "1.0.0")
	m.Definitions = append(m.Definitions, contract.DefinitionSpec{
		ID: "nullable", ConfigSchema: json.RawMessage(`{"type":"object","nullable":true}`),
	})
	archive, _ := archiveFixture(t, m, key)
	p, err := s.Install(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Select(ctx, p.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []contract.Entry{
		{ID: "omitted", Package: "demo", Definition: "main"},
		{ID: "explicit-null", Package: "demo", Definition: "nullable", Config: json.RawMessage(`null`)},
	} {
		if err := s.SaveEntry(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveEntry(ctx, contract.Entry{
		ID: "invalid-null", Package: "demo", Definition: "main", Config: json.RawMessage(`null`),
	}); err == nil {
		t.Fatal("explicit null was silently converted to an object")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, root, trust)
	entries, err := s.Entries()
	if err != nil || len(entries) != 2 {
		t.Fatalf("persisted entries: %+v, %v", entries, err)
	}
	configs := map[string]string{}
	for _, entry := range entries {
		configs[entry.ID] = string(entry.Config)
	}
	if configs["omitted"] != "{}" || configs["explicit-null"] != "null" {
		t.Fatalf("configuration semantics changed after reopen: %v", configs)
	}
}

func TestStoreCandidateValidationPreservesCommittedRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := openFixture(t, root, TrustPolicy{})
	if err := s.mutate(context.Background(), s.cloneCatalog()); err != nil {
		t.Fatal(err)
	}
	c := s.cloneCatalog()
	// Model a future mutation accepting a leaf that recovery cannot read.
	c.Entries["invalid"] = contract.Entry{ID: "invalid", Package: "demo", Config: []byte(strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33))}
	if err := s.mutate(context.Background(), c); err == nil {
		t.Fatal("committed an unreadable encoded candidate")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, root, TrustPolicy{})
	entries, err := s.Entries()
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected candidate changed durable entries: %+v, %v", entries, err)
	}
}

func TestStoreRejectsMissingInstalledSignatureRecord(t *testing.T) {
	t.Parallel()
	key, trust := signingFixture(t)
	archive, pkg := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
	root := t.TempDir()
	s := openFixture(t, root, trust)
	if _, err := s.Install(t.Context(), archive); err != nil {
		t.Fatal(err)
	}
	c := s.cloneCatalog()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	delete(c.Signatures, pkg.Ref.Digest)
	if err := durableJSON(filepath.Join(root, "catalog.json"), c); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(root, trust)
	if err == nil {
		_ = opened.Close()
		t.Fatal("installed content was admitted without its committed signature record")
	}
}

func TestDefinitionSchemasRemainCoveredByPackageIntegrity(t *testing.T) {
	key, trust := signingFixture(t)
	m := manifestFixture("demo", "1.0.0")
	m.Definitions = append(m.Definitions, contract.DefinitionSpec{ID: "other", ConfigSchema: json.RawMessage(`{"type":"string"}`)})
	for _, index := range []int{0, 1} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			archive, _ := archiveFixture(t, m, key)
			s := openFixture(t, t.TempDir(), trust)
			p, err := s.Install(t.Context(), archive)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Select(t.Context(), p.Ref, ChangeOptions{}); err != nil {
				t.Fatal(err)
			}
			changed := p.Manifest
			changed.Definitions[index].ConfigSchema = json.RawMessage(`{"type":"boolean"}`)
			raw, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(p.Directory, "plugin.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if snapshot, err := s.Snapshot(t.Context(), p.Ref); err == nil {
				snapshot.Close()
				t.Fatal("changed definition config escaped manifest digest")
			}
		})
	}
}
