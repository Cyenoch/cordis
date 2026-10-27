package access

import (
	"context"
	"fmt"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func namespaceFixture(t *testing.T, quota int64) (*Policy, *NamespaceStore, contract.Identity, string) {
	t.Helper()
	p := NewPolicy()
	owner := contract.Identity{Package: "pkg", Entry: "entry", Activation: "activation"}
	if err := p.Activate(owner, nil); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s, err := OpenNamespaceStore(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(owner.Package, NamespaceConfig{Name: "state", QuotaBytes: quota}); err != nil {
		t.Fatal(err)
	}
	return p, s, owner, dir
}

func TestNamespaceLargeQuotaReopens(t *testing.T) {
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprint(compact), func(t *testing.T) {
			p, s, owner, dir := namespaceFixture(t, 1<<53)
			if err := s.Write(t.Context(), owner, "state", "key", []byte("value")); err != nil {
				t.Fatal(err)
			}
			if compact {
				if err := s.Compact(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := OpenNamespaceStore(p, dir); err != nil {
				t.Fatalf("acknowledged log cannot reopen: %v", err)
			}
		})
	}
}

func TestNamespaceMigrateManyKeysReopens(t *testing.T) {
	p, s, _, dir := namespaceFixture(t, 1<<20)
	err := s.Migrate(t.Context(), "state", 1,
		func(context.Context, string, string) error { return nil },
		func(context.Context, uint32, map[string][]byte) (map[string][]byte, error) {
			values := make(map[string][]byte, 12000)
			for n := range 12000 {
				values[fmt.Sprint(n)] = make([]byte, 8)
			}
			return values, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenNamespaceStore(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.spaces["state"].Values) != 12000 {
		t.Fatal("lost entries")
	}
}

func TestNamespaceRejectsLossyUTF8BeforePersistence(t *testing.T) {
	p, s, owner, dir := namespaceFixture(t, 100)
	for _, key := range []string{string([]byte{0xff}), string([]byte{0xfe})} {
		if err := s.Write(t.Context(), owner, "state", key, []byte("value")); err == nil {
			t.Fatal("accepted invalid UTF-8")
		}
	}
	if err := s.Migrate(t.Context(), "state", 1, func(context.Context, string, string) error { return nil }, func(context.Context, uint32, map[string][]byte) (map[string][]byte, error) {
		return map[string][]byte{string([]byte{0xff}): nil}, nil
	}); err == nil {
		t.Fatal("accepted lossy migration key")
	}
	if _, err := OpenNamespaceStore(p, dir); err != nil {
		t.Fatal(err)
	}
}

type checkedContext struct {
	context.Context
	checked chan struct{}
}

func (c checkedContext) Err() error {
	err := c.Context.Err()
	select {
	case c.checked <- struct{}{}:
	default:
	}
	return err
}

func TestNamespaceCanceledNamespaceMaintenanceDoesNotCommit(t *testing.T) {
	for _, action := range []string{"migrate", "delete"} {
		t.Run(action, func(t *testing.T) {
			p, s, owner, dir := namespaceFixture(t, 100)
			if err := s.Write(t.Context(), owner, "state", "key", []byte("before")); err != nil {
				t.Fatal(err)
			}
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			checked := make(chan struct{}, 1)
			ctx := checkedContext{Context: parent, checked: checked}
			done := make(chan error, 1)
			if action == "delete" {
				go func() {
					done <- s.DeleteNamespace(ctx, "state", func(context.Context, string, string) error { s.mu.Lock(); return nil })
				}()
			} else {
				go func() {
					done <- s.Migrate(ctx, "state", 1,
						func(context.Context, string, string) error { return nil },
						func(context.Context, uint32, map[string][]byte) (map[string][]byte, error) {
							s.mu.Lock()
							return map[string][]byte{"key": []byte("after")}, nil
						})
				}()
			}
			<-checked
			cancel()
			s.mu.Unlock()
			if err := <-done; err == nil {
				reopened, openErr := OpenNamespaceStore(p, dir)
				if openErr != nil {
					t.Fatal(openErr)
				}
				got, readErr := reopened.Read(t.Context(), owner, "state", "key")
				t.Fatalf("canceled maintenance committed: value %q, read error %v", got, readErr)
			}
		})
	}
}

func TestNamespaceCanceledWriteWhileQueuedHasNoEffect(t *testing.T) {
	p, s, owner, dir := namespaceFixture(t, 100)
	entered, release := make(chan struct{}), make(chan struct{})
	s.commitObserver = func() { close(entered); <-release }
	firstDone := make(chan error, 1)
	go func() { firstDone <- s.Write(t.Context(), owner, "state", "first", []byte("value")) }()
	<-entered
	parent, cancel := context.WithCancel(t.Context())
	checked := make(chan struct{}, 1)
	ctx := checkedContext{Context: parent, checked: checked}
	secondDone := make(chan error, 1)
	go func() { secondDone <- s.Write(ctx, owner, "state", "second", []byte("late")) }()
	<-checked
	cancel()
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err == nil {
		t.Fatal("canceled queued write succeeded")
	}
	reopened, err := OpenNamespaceStore(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := reopened.spaces["state"].Values["second"]; exists {
		t.Fatal("canceled queued write persisted")
	}
}

func TestNamespaceSameOwnerRevokeWaitsForCommittedWrite(t *testing.T) {
	p, s, owner, dir := namespaceFixture(t, 100)
	entered, release := make(chan struct{}), make(chan struct{})
	s.commitObserver = func() { close(entered); <-release }
	done := make(chan error, 1)
	go func() { done <- s.Write(t.Context(), owner, "state", "key", []byte("value")) }()
	<-entered
	revoked := make(chan error, 1)
	go func() { revoked <- p.Revoke(owner) }()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	if err := s.Write(t.Context(), owner, "state", "key", []byte("late")); err == nil {
		t.Fatal("revoked owner wrote")
	}
	reopened, err := OpenNamespaceStore(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(reopened.spaces["state"].Values["key"]) != "value" {
		t.Fatal("committed value missing")
	}
}
