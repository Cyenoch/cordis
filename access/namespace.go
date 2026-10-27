package access

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"github.com/Cyenoch/cordis/contract"
)

var ErrQuota = errors.New("namespace quota exceeded")
var ErrNotFound = errors.New("namespace key not found")

type NamespaceConfig struct {
	Name        string `json:"name"`
	QuotaBytes  int64  `json:"quotaBytes,string"`
	DataVersion uint32 `json:"dataVersion"`
}

type namespaceData struct {
	Package  string            `json:"package"`
	Config   NamespaceConfig   `json:"config"`
	Values   map[string][]byte `json:"values"`
	Used     int64             `json:"used"`
	revision uint64
}

// NamespaceStore is a host-owned key/value proxy, not a guest filesystem.
// The default constructor is explicitly in-memory. OpenNamespaceStore adds durable
// checksummed incremental records; opening errors never fall back to volatile storage.
type NamespaceStore struct {
	policy   *Policy
	mu       sync.Mutex
	spaces   map[string]*namespaceData
	path     string
	sequence uint64
	failure  error
	// Test-only pause at the durable write boundary; nil in production.
	commitObserver func()
}

func NewNamespaceStore(policy *Policy) *NamespaceStore {
	return &NamespaceStore{policy: policy, spaces: make(map[string]*namespaceData)}
}

func OpenNamespaceStore(policy *Policy, directory string) (*NamespaceStore, error) {
	if policy == nil || directory == "" {
		return nil, contract.Fail(contract.Invalid, "invalid namespace store")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create namespace directory: %w", err)
	}
	store := NewNamespaceStore(policy)
	store.path = filepath.Join(directory, "namespaces.log")
	if err := store.recoverLog(); err != nil {
		return nil, err
	}
	return store, nil
}

// Configure creates a namespace binding once. Version changes must use Migrate.
func (s *NamespaceStore) Configure(packageID string, config NamespaceConfig) error {
	if s.policy == nil || packageID == "" || config.Name == "" || !utf8.ValidString(packageID) || !utf8.ValidString(config.Name) || config.QuotaBytes < 0 {
		return contract.Fail(contract.Invalid, "invalid namespace configuration")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return &RecoveryError{Cause: s.failure}
	}
	for _, space := range s.spaces {
		if space.Package == packageID || space.Config.Name == config.Name {
			if space.Package == packageID && space.Config == config {
				return nil
			}
			return contract.Fail(contract.Conflict, "namespace already bound")
		}
	}
	s.spaces[config.Name] = &namespaceData{
		Package: packageID, Config: config, Values: make(map[string][]byte),
	}
	if err := s.appendLog(namespaceRecord{Operation: "configure", Namespace: config.Name, Package: packageID, Config: config}); err != nil {
		if !isCommitted(err) {
			delete(s.spaces, config.Name)
		}
		return err
	}
	return nil
}

// Storage serialization is independent of the global policy lock.
func (s *NamespaceStore) lock(ctx context.Context, owner contract.Identity, namespace string) (*namespaceData, error) {
	if s.policy == nil || ctx == nil {
		return nil, contract.Fail(contract.Invalid, "namespace store not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if s.failure != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("namespace needs recovery: %w", s.failure)
	}
	if err := s.policy.Check(owner); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	space := s.spaces[namespace]
	if space == nil || space.Package != owner.Package {
		s.mu.Unlock()
		return nil, contract.Fail(contract.Denied, "namespace access denied")
	}
	return space, nil
}

func (s *NamespaceStore) unlock() { s.mu.Unlock() }

func (s *NamespaceStore) commitOwner(ctx context.Context, owner contract.Identity) (func(), error) {
	s.policy.mu.Lock()
	a, err := s.policy.activeLocked(owner)
	s.policy.mu.Unlock()
	if err != nil {
		return nil, err
	}
	a.commit.Lock()
	if err := ctx.Err(); err != nil {
		a.commit.Unlock()
		return nil, err
	}
	if err := s.policy.Check(owner); err != nil {
		a.commit.Unlock()
		return nil, err
	}
	return a.commit.Unlock, nil
}

func (s *NamespaceStore) Read(
	ctx context.Context, owner contract.Identity, namespace string, key string,
) ([]byte, error) {
	space, err := s.lock(ctx, owner, namespace)
	if err != nil {
		return nil, err
	}
	defer s.unlock()
	value, exists := space.Values[key]
	if !exists {
		return nil, ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *NamespaceStore) Write(
	ctx context.Context, owner contract.Identity, namespace string, key string, value []byte,
) error {
	if key == "" || !utf8.ValidString(key) {
		return contract.Fail(contract.Invalid, "namespace key must be nonempty UTF-8")
	}
	space, err := s.lock(ctx, owner, namespace)
	if err != nil {
		return err
	}
	defer s.unlock()
	previous, exists := space.Values[key]
	oldCost := int64(0)
	if exists {
		oldCost = int64(len(key)) + int64(len(previous))
	}
	newCost := int64(len(key)) + int64(len(value))
	available := space.Config.QuotaBytes - (space.Used - oldCost)
	if newCost > available {
		return ErrQuota
	}
	release, err := s.commitOwner(ctx, owner)
	if err != nil {
		return err
	}
	defer release()
	space.Values[key] = append([]byte(nil), value...)
	space.Used += newCost - oldCost
	if err := s.appendLog(namespaceRecord{Operation: "write", Namespace: namespace, Key: key, Value: value}); err != nil {
		if isCommitted(err) {
			space.revision++
			return err
		}
		if exists {
			space.Values[key] = previous
		} else {
			delete(space.Values, key)
		}
		space.Used -= newCost - oldCost
		return err
	}
	space.revision++
	return nil
}

func (s *NamespaceStore) Delete(
	ctx context.Context, owner contract.Identity, namespace string, key string,
) error {
	space, err := s.lock(ctx, owner, namespace)
	if err != nil {
		return err
	}
	defer s.unlock()
	previous, exists := space.Values[key]
	if !exists {
		return ErrNotFound
	}
	cost := int64(len(key)) + int64(len(previous))
	release, err := s.commitOwner(ctx, owner)
	if err != nil {
		return err
	}
	defer release()
	delete(space.Values, key)
	space.Used -= cost
	if err := s.appendLog(namespaceRecord{Operation: "delete", Namespace: namespace, Key: key}); err != nil {
		if isCommitted(err) {
			space.revision++
			return err
		}
		space.Values[key] = previous
		space.Used += cost
		return err
	}
	space.revision++
	return nil
}

// Confirmation is a trusted host decision, never a guest-controlled boolean.
type Confirmation func(context.Context, string, string) error

type Migration func(context.Context, uint32, map[string][]byte) (map[string][]byte, error)

func (s *NamespaceStore) Migrate(
	ctx context.Context, namespace string, version uint32, confirm Confirmation, migrate Migration,
) error {
	if ctx == nil || confirm == nil || migrate == nil {
		return contract.Fail(contract.Invalid, "migration needs host approval")
	}
	if err := confirm(ctx, namespace, "migrate"); err != nil {
		return fmt.Errorf("approve namespace migration: %w", err)
	}
	s.mu.Lock()
	space := s.spaces[namespace]
	if space == nil {
		s.mu.Unlock()
		return ErrNotFound
	}
	oldVersion := space.Config.DataVersion
	revision := space.revision
	values := cloneValues(space.Values)
	s.mu.Unlock()
	values, err := migrate(ctx, oldVersion, values)
	if err != nil {
		return fmt.Errorf("migrate namespace: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("migrate namespace: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("migrate namespace: %w", err)
	}
	if s.failure != nil {
		return &RecoveryError{Cause: s.failure}
	}
	if s.spaces[namespace] != space || space.Config.DataVersion != oldVersion {
		return contract.Fail(contract.Conflict, "namespace changed during migration")
	}
	// Reject concurrent writes instead of silently overwriting them.
	if space.revision != revision {
		return contract.Fail(contract.Conflict, "namespace changed during migration")
	}
	var used int64
	for key, value := range values {
		if key == "" || !utf8.ValidString(key) {
			return contract.Fail(contract.Invalid, "namespace key must be nonempty UTF-8")
		}
		cost := int64(len(key)) + int64(len(value))
		if cost > space.Config.QuotaBytes-used {
			return ErrQuota
		}
		used += cost
	}
	previous, previousUsed := space.Values, space.Used
	space.Values, space.Used, space.Config.DataVersion = cloneValues(values), used, version
	if err := s.appendLog(namespaceRecord{Operation: "migrate", Namespace: namespace, Config: space.Config, Values: space.Values}); err != nil {
		if isCommitted(err) {
			space.revision++
			return err
		}
		space.Values, space.Used, space.Config.DataVersion = previous, previousUsed, oldVersion
		return err
	}
	space.revision++
	return nil
}

func (s *NamespaceStore) DeleteNamespace(ctx context.Context, namespace string, confirm Confirmation) error {
	if ctx == nil || confirm == nil {
		return contract.Fail(contract.Invalid, "deletion needs host approval")
	}
	if err := confirm(ctx, namespace, "delete"); err != nil {
		return fmt.Errorf("approve namespace deletion: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete namespace: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete namespace: %w", err)
	}
	if s.failure != nil {
		return &RecoveryError{Cause: s.failure}
	}
	space := s.spaces[namespace]
	if space == nil {
		return ErrNotFound
	}
	delete(s.spaces, namespace)
	if err := s.appendLog(namespaceRecord{Operation: "remove", Namespace: namespace}); err != nil {
		if !isCommitted(err) {
			s.spaces[namespace] = space
		}
		return err
	}
	return nil
}

func cloneValues(values map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(values))
	for key, value := range values {
		result[key] = append([]byte(nil), value...)
	}
	return result
}

// A complete synchronized record is committed. A subsequent directory/close
// failure must not roll back the in-memory state.
type CommittedError struct{ Cause error }

func (e *CommittedError) Error() string { return "namespace committed but durability sync failed" }
func (e *CommittedError) Unwrap() error { return e.Cause }

func isCommitted(err error) bool {
	var committed *CommittedError
	return errors.As(err, &committed)
}
