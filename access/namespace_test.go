package access_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Cyenoch/cordis/access"
	"github.com/Cyenoch/cordis/contract"
)

func TestNamespaceRejectsCrossPluginAndQuotaWithoutMutation(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	first := identity("first")
	second := identity("second")
	second.Package, second.Entry = "other.plugin", "other"
	activate(t, policy, first)
	activate(t, policy, second)
	store := access.NewNamespaceStore(policy)
	if err := store.Configure(first.Package, access.NamespaceConfig{Name: "first", QuotaBytes: 5, DataVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.Configure(second.Package, access.NamespaceConfig{Name: "second", QuotaBytes: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(t.Context(), first, "first", "k", []byte("1234")); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(t.Context(), second, "first", "k", []byte("evil")); err == nil {
		t.Fatal("cross-namespace write succeeded")
	}
	if _, err := store.Read(t.Context(), second, "first", "k"); err == nil {
		t.Fatal("cross-namespace read succeeded")
	}
	if err := store.Write(t.Context(), first, "first", "k", []byte("12345")); !errors.Is(err, access.ErrQuota) {
		t.Fatalf("quota error = %v", err)
	}
	got, err := store.Read(t.Context(), first, "first", "k")
	if err != nil || string(got) != "1234" {
		t.Fatalf("failed write mutated value: %q %v", got, err)
	}
	if err := policy.Revoke(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(t.Context(), first, "first", "k", nil); err == nil {
		t.Fatal("retired owner wrote data")
	}
}

func TestNamespaceDurabilityAndHostConfirmedMigration(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("first")
	activate(t, policy, owner)
	directory := t.TempDir()
	store, err := access.OpenNamespaceStore(policy, directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Configure(owner.Package, access.NamespaceConfig{Name: "state", QuotaBytes: 100, DataVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(t.Context(), owner, "state", "key", []byte("before")); err != nil {
		t.Fatal(err)
	}
	deny := errors.New("host confirmation denied")
	if err := store.Migrate(t.Context(), "state", 2, func(context.Context, string, string) error { return deny },
		func(context.Context, uint32, map[string][]byte) (map[string][]byte, error) {
			t.Fatal("migration ran without confirmation")
			return nil, nil
		},
	); !errors.Is(err, deny) {
		t.Fatalf("confirmation failure lost: %v", err)
	}
	approve := func(context.Context, string, string) error { return nil }
	if err := store.Migrate(t.Context(), "state", 2, approve,
		func(_ context.Context, version uint32, values map[string][]byte) (map[string][]byte, error) {
			if version != 1 {
				t.Fatalf("source version = %d", version)
			}
			values["key"] = []byte("after")
			return values, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	reopened, err := access.OpenNamespaceStore(policy, directory)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Read(t.Context(), owner, "state", "key")
	if err != nil || string(got) != "after" {
		t.Fatalf("persistent data = %q %v", got, err)
	}
	// Exercise replacement of an existing snapshot and deletion, including on Windows.
	if err := reopened.Write(t.Context(), owner, "state", "key", []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Delete(t.Context(), owner, "state", "key"); err != nil {
		t.Fatal(err)
	}
	afterDelete, err := access.OpenNamespaceStore(policy, directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := afterDelete.Read(t.Context(), owner, "state", "key"); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("deleted key reappeared after reopening: %v", err)
	}
	if err := reopened.DeleteNamespace(t.Context(), "state", nil); err == nil {
		t.Fatal("unconfirmed deletion succeeded")
	}
	if err := reopened.DeleteNamespace(t.Context(), "state", approve); err != nil {
		t.Fatal(err)
	}
	afterNamespaceDelete, err := access.OpenNamespaceStore(policy, directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := afterNamespaceDelete.Configure(owner.Package, access.NamespaceConfig{Name: "state", QuotaBytes: 100}); err != nil {
		t.Fatalf("namespace deletion did not persist: %v", err)
	}
}

func TestNamespaceConfigurePreservesMigratedVersionAfterReopen(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"log", "compact"} {
		t.Run(mode, func(t *testing.T) {
			policy := access.NewPolicy()
			owner := identity("migrated")
			activate(t, policy, owner)
			directory := t.TempDir()
			store, err := access.OpenNamespaceStore(policy, directory)
			if err != nil {
				t.Fatal(err)
			}
			config := access.NamespaceConfig{Name: "state", QuotaBytes: 100, DataVersion: 1}
			if err := store.Configure(owner.Package, config); err != nil {
				t.Fatal(err)
			}
			approve := func(context.Context, string, string) error { return nil }
			if err := store.Migrate(t.Context(), "state", 2, approve,
				func(_ context.Context, version uint32, values map[string][]byte) (map[string][]byte, error) {
					if version != 1 {
						t.Fatalf("initial data version = %d, want 1", version)
					}
					values["key"] = []byte("migrated")
					return values, nil
				}); err != nil {
				t.Fatal(err)
			}
			if mode == "compact" {
				if err := store.Compact(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			store, err = access.OpenNamespaceStore(policy, directory)
			if err != nil {
				t.Fatal(err)
			}
			for _, version := range []uint32{1, 99} {
				config.DataVersion = version
				if err := store.Configure(owner.Package, config); err != nil {
					t.Fatalf("reconfigure migrated namespace: %v", err)
				}
			}
			config.QuotaBytes = 200
			if err := store.Configure(owner.Package, config); err != nil {
				t.Fatal(err)
			}
			store, err = access.OpenNamespaceStore(policy, directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Migrate(t.Context(), "state", 3, approve,
				func(_ context.Context, version uint32, values map[string][]byte) (map[string][]byte, error) {
					if version != 2 || string(values["key"]) != "migrated" {
						t.Fatalf("reopened migration source = %d, %q; want 2, migrated", version, values["key"])
					}
					return values, nil
				}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNamespaceConfigureQuotaPersistsWithoutChangingBinding(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		quota int64
	}{{name: "increase", quota: 12}, {name: "decrease-to-usage", quota: 5}} {
		t.Run(test.name, func(t *testing.T) {
			policy := access.NewPolicy()
			owner := identity("quota")
			activate(t, policy, owner)
			directory := t.TempDir()
			store, err := access.OpenNamespaceStore(policy, directory)
			if err != nil {
				t.Fatal(err)
			}
			config := access.NamespaceConfig{Name: "state", QuotaBytes: 10, DataVersion: 1}
			if err := store.Configure(owner.Package, config); err != nil {
				t.Fatal(err)
			}
			if err := store.Write(t.Context(), owner, "state", "k", []byte("1234")); err != nil {
				t.Fatal(err)
			}
			config.QuotaBytes = test.quota
			if err := store.Configure(owner.Package, config); err != nil {
				t.Fatalf("update quota: %v", err)
			}
			config.QuotaBytes = 4
			requireAccessCode(t, store.Configure(owner.Package, config), contract.Conflict)
			requireAccessCode(t, store.Configure("other.package", config), contract.Conflict)
			config.Name = "other"
			requireAccessCode(t, store.Configure(owner.Package, config), contract.Conflict)
			if err := store.Write(t.Context(), owner, "state", "k", []byte("1234")); err != nil {
				t.Fatalf("rejected configuration changed live quota: %v", err)
			}
			store, err = access.OpenNamespaceStore(policy, directory)
			if err != nil {
				t.Fatal(err)
			}
			got, err := store.Read(t.Context(), owner, "state", "k")
			if err != nil || string(got) != "1234" {
				t.Fatalf("reconfiguration changed data: %q, %v", got, err)
			}
			if err := store.Write(t.Context(), owner, "state", "k", make([]byte, int(test.quota)-1)); err != nil {
				t.Fatalf("write up to updated quota: %v", err)
			}
			if err := store.Write(t.Context(), owner, "state", "k", make([]byte, int(test.quota))); !errors.Is(err, access.ErrQuota) {
				t.Fatalf("write exceeding updated quota = %v", err)
			}
		})
	}
}

func TestNamespaceMigrationRejectsConcurrentWrite(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("first")
	activate(t, policy, owner)
	store := access.NewNamespaceStore(policy)
	if err := store.Configure(owner.Package, access.NamespaceConfig{Name: "state", QuotaBytes: 100}); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(t.Context(), owner, "state", "key", []byte("original")); err != nil {
		t.Fatal(err)
	}
	err := store.Migrate(t.Context(), "state", 1,
		func(context.Context, string, string) error { return nil },
		func(_ context.Context, _ uint32, values map[string][]byte) (map[string][]byte, error) {
			if err := store.Write(t.Context(), owner, "state", "key", []byte("concurrent")); err != nil {
				return nil, err
			}
			values["key"] = []byte("migration")
			return values, nil
		},
	)
	if err == nil {
		t.Fatal("migration overwrote concurrent write")
	}
	got, readErr := store.Read(t.Context(), owner, "state", "key")
	if readErr != nil || string(got) != "concurrent" {
		t.Fatalf("concurrent value lost: %q %v", got, readErr)
	}
}

func TestNamespaceIncrementalLogRejectsCorruption(t *testing.T) {
	policy := access.NewPolicy()
	owner := identity("log")
	activate(t, policy, owner)
	directory := t.TempDir()
	s, err := access.OpenNamespaceStore(policy, directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Configure(owner.Package, access.NamespaceConfig{Name: "state", QuotaBytes: 2 << 20}); err != nil {
		t.Fatal(err)
	}
	if err = s.Write(t.Context(), owner, "state", "large", make([]byte, 1<<20)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "namespaces.log")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Write(t.Context(), owner, "state", "small", []byte("1")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size()-before.Size() > 1024 {
		t.Fatal("small write rewrote existing payload")
	}
	reopened, err := access.OpenNamespaceStore(policy, directory)
	if err != nil {
		t.Fatal(err)
	}
	value, err := reopened.Read(t.Context(), owner, "state", "small")
	if err != nil || string(value) != "1" {
		t.Fatal(value, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("{broken\n"); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = access.OpenNamespaceStore(policy, directory); err == nil {
		t.Fatal("corrupt log silently recovered")
	}
}
