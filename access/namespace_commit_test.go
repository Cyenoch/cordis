package access

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

func TestNamespaceDiskCommitDoesNotBlockOtherActivationRevocation(t *testing.T) {
	p := NewPolicy()
	first := contract.Identity{Package: "first", Entry: "first", Activation: "first"}
	second := contract.Identity{Package: "second", Entry: "second", Activation: "second"}
	if err := p.Activate(first, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Activate(second, nil); err != nil {
		t.Fatal(err)
	}
	s, err := OpenNamespaceStore(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Configure(first.Package, NamespaceConfig{Name: "first", QuotaBytes: 100}); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.commitObserver = func() { close(entered); <-release }
	done := make(chan error, 1)
	go func() { done <- s.Write(context.Background(), first, "first", "key", []byte("value")) }()
	<-entered
	revoked := make(chan error, 1)
	go func() { revoked <- p.Revoke(second) }()
	select {
	case err := <-revoked:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("unrelated revocation blocked by disk")
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestNamespaceCompactionPreservesDataAndFutureWrites(t *testing.T) {
	p := NewPolicy()
	owner := contract.Identity{Package: "pkg", Entry: "entry", Activation: "activation"}
	if err := p.Activate(owner, nil); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	s, err := OpenNamespaceStore(p, directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Configure(owner.Package, NamespaceConfig{Name: "state", QuotaBytes: 100}); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 20; n++ {
		if err = s.Write(t.Context(), owner, "state", "k", []byte("value")); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.Stat(filepath.Join(directory, "namespaces.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Compact(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(directory, "namespaces.log"))
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() >= before.Size() {
		t.Fatal("compaction retained obsolete history")
	}
	if err = s.Write(t.Context(), owner, "state", "after", []byte("compact")); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenNamespaceStore(p, directory)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"k": "value", "after": "compact"} {
		got, err := reopened.Read(t.Context(), owner, "state", key)
		if err != nil || string(got) != want {
			t.Fatal(key, string(got), err)
		}
	}
}

func TestNamespaceConfigureQuotaFollowsDurableCommit(t *testing.T) {
	for _, boundary := range []string{"before-commit", "after-commit"} {
		t.Run(boundary, func(t *testing.T) {
			if boundary == "after-commit" && runtime.GOOS == "windows" {
				t.Skip("Windows has no separate directory sync failure boundary")
			}
			p, store, owner, directory := namespaceFixture(t, 20)
			path := store.path
			if boundary == "before-commit" {
				store.path = directory // Opening a directory for a log write fails.
			} else {
				// Keep the real append and fsync, then fail the directory sync.
				store.commitObserver = func() { store.path = filepath.Join(directory, "missing", "log") }
			}
			err := store.Configure(owner.Package, NamespaceConfig{Name: "state", QuotaBytes: 30})
			if err == nil {
				t.Fatal("quota update did not report persistence failure")
			}
			var committed *CommittedError
			if errors.As(err, &committed) != (boundary == "after-commit") {
				t.Fatalf("wrong commit classification: %v", err)
			}
			store.path, store.commitObserver = path, nil
			for _, phase := range []string{"live", "reopened"} {
				if phase == "reopened" {
					store, err = OpenNamespaceStore(p, directory)
					if err != nil {
						t.Fatal(err)
					}
				}
				// This value fits only the increased quota.
				err := store.Write(t.Context(), owner, "state", "k", make([]byte, 25))
				if boundary == "after-commit" && err != nil {
					t.Fatalf("%s quota did not retain committed update: %v", phase, err)
				}
				if boundary == "before-commit" && !errors.Is(err, ErrQuota) {
					t.Fatalf("%s quota changed after failed update: %v", phase, err)
				}
			}
		})
	}
}
