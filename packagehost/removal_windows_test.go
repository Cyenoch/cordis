//go:build windows && integration

package packagehost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestStoreUninstallRecoversWindowsHeldArtifact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	root := t.TempDir()

	// Track the current store, rather than deferring Close on stores closed
	// explicitly at each recovery boundary.
	var store *Store
	t.Cleanup(func() {
		if store != nil {
			if err := store.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	openStore := func() {
		t.Helper()
		var err error
		store, err = Open(root, trust)
		if err != nil {
			t.Fatal(err)
		}
	}
	closeStore := func() {
		t.Helper()
		err := store.Close()
		store = nil
		if err != nil {
			t.Fatal(err)
		}
	}
	openStore()

	archive, _ := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
	removed, err := store.Install(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	otherArchive, _ := archiveFixture(t, manifestFixture("dep", "1.0.0"), key)
	other, err := store.Install(ctx, otherArchive)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Select(ctx, removed.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := store.Select(ctx, other.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}

	artifact := filepath.Join(root, "content", removed.Ref.Digest, removed.Manifest.Runtime.Artifacts[0].Path)
	path, err := windows.UTF16PtrFromString(artifact)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately omit FILE_SHARE_DELETE: a real OS handle, not an injected
	// cleanup failure, must keep the installed artifact from being deleted.
	handle, err := windows.CreateFile(
		path,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	closeArtifact := func() error {
		if handle == windows.InvalidHandle {
			return nil
		}
		err := windows.CloseHandle(handle)
		handle = windows.InvalidHandle
		return err
	}
	t.Cleanup(func() {
		if err := closeArtifact(); err != nil {
			t.Error(err)
		}
	})

	assertState := func(wantPending bool) {
		t.Helper()
		pending, err := store.PendingRemovals()
		if err != nil {
			t.Fatal(err)
		}
		durable := readRecoveryCatalog(t, root)
		if wantPending {
			if len(pending) != 1 || pending[0] != removed.Ref {
				t.Fatalf("pending removals = %+v, want only %+v", pending, removed.Ref)
			}
			if len(durable.Pending) != 1 {
				t.Fatalf("durable pending removals = %+v, want one", durable.Pending)
			}
			p := durable.Pending[0]
			if p.Ref != removed.Ref {
				t.Fatalf("durable pending removal = %+v, want removal for %+v", p, removed.Ref)
			}
			if strings.TrimSpace(p.Error) == "" {
				t.Fatal("durable pending removal omitted the OS failure reason")
			}
			contentDir := "content/" + removed.Ref.Digest
			trashDir := "trash/" + removed.Ref.Digest
			if p.Directory != contentDir && p.Directory != trashDir {
				t.Fatalf("unexpected durable pending directory %q", p.Directory)
			}
			heldArtifact := filepath.Join(root, filepath.FromSlash(p.Directory), removed.Manifest.Runtime.Artifacts[0].Path)
			raw, err := os.ReadFile(heldArtifact)
			if err != nil || string(raw) != "fixture artifact" {
				t.Fatalf("held artifact = %q, %v; want original code", raw, err)
			}
		} else {
			if len(pending) != 0 || len(durable.Pending) != 0 {
				t.Fatalf("removal still pending after releasing handle: public=%+v durable=%+v", pending, durable.Pending)
			}
			assertRecoveryPath(t, filepath.Join(root, "content", removed.Ref.Digest), false)
			assertRecoveryPath(t, filepath.Join(root, "trash", removed.Ref.Digest), false)
		}
		if _, err := store.Get(removed.Ref); err == nil {
			t.Fatal("uninstalled package is still exposed as installed")
		}
		if _, err := store.Selected(removed.Ref.ID); err == nil {
			t.Fatal("uninstalled package is still selected")
		}
		if _, ok := durable.Installed[removed.Ref.Digest]; ok {
			t.Fatal("durable installed catalog still exposes uninstalled package")
		}
		if _, ok := durable.Selected[removed.Ref.ID]; ok {
			t.Fatal("durable catalog still selects uninstalled package")
		}
		installed, err := store.Installed()
		if err != nil {
			t.Fatal(err)
		}
		if len(installed) != 1 || installed[0].Ref != other.Ref {
			t.Fatalf("installed packages = %+v, want only unaffected package %+v", installed, other.Ref)
		}
		selected, err := store.Selected(other.Ref.ID)
		if err != nil || selected.Ref != other.Ref {
			t.Fatalf("other package selection = %+v, %v", selected.Ref, err)
		}
		if durable.Installed[other.Ref.Digest].Ref != other.Ref || durable.Selected[other.Ref.ID] != other.Ref {
			t.Fatal("durable catalog changed the other package")
		}
		otherArtifact := filepath.Join(root, "content", other.Ref.Digest, other.Manifest.Runtime.Artifacts[0].Path)
		raw, err := os.ReadFile(otherArtifact)
		if err != nil || string(raw) != "fixture artifact" {
			t.Fatalf("other package artifact = %q, %v; want original code", raw, err)
		}

	}

	// No live package references or enabled dependents are declared.
	result, err := store.Uninstall(ctx, removed.Ref, ChangeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Pending || result.Removed {
		t.Fatalf("held artifact removal = %+v, want pending and not removed", result)
	}
	if strings.TrimSpace(result.Reason) == "" {
		t.Fatal("pending removal omitted the OS failure reason")
	}
	assertState(true)

	closeStore()
	openStore()
	assertState(true)

	closeStore()
	if err := closeArtifact(); err != nil {
		t.Fatal(err)
	}
	// Open must finish durable cleanup without an explicit RetryRemovals call.
	openStore()
	assertState(false)
}
