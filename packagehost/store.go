package packagehost

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/Cyenoch/cordis/contract"
)

type catalog struct {
	Revision     uint64                         `json:"revision"`
	Transaction  string                         `json:"transaction"`
	Installed    map[string]Package             `json:"installed"`
	Selected     map[string]contract.PackageRef `json:"selected"`
	Entries      map[string]contract.Entry      `json:"entries"`
	DataVersions map[string]uint32              `json:"dataVersions"`
	Publishers   map[string]string              `json:"publishers"`
	Pending      []pendingRemoval               `json:"pendingRemoval"`
}
type pendingRemoval struct {
	Ref        contract.PackageRef `json:"ref"`
	Directory  string              `json:"directory"`
	DeleteData bool                `json:"deleteData"`
	Error      string              `json:"error,omitempty"`
}
type transaction struct {
	ID        string `json:"id"`
	Operation string `json:"operation"`
	Digest    string `json:"digest,omitempty"`
}

type commitStage string

const (
	commitJournalSynced  commitStage = "journal-synced"
	commitContentMoved   commitStage = "content-moved"
	commitCatalogSynced  commitStage = "catalog-next-synced"
	commitCatalogSwapped commitStage = "catalog-swapped"
	commitRootSynced     commitStage = "root-synced"
)

type Store struct {
	mu      sync.Mutex
	root    string
	trust   TrustPolicy
	limits  Limits
	decoder MetadataDecoder
	catalog catalog
	lock    *os.File
	closed  bool
	// commitObserver is set only by internal tests, before starting a mutation.
	// It runs under mu and must not call Store methods. Production leaves it nil.
	commitObserver func(commitStage)
}

// ChangeOptions conveys host-owned lifecycle/data facts; Store never stops code.
// Enabled contains enabled package IDs after any explicitly authorized cascade.
// InUse must contain every live package reference. A switch/removal cannot proceed
// while that reference is in use. MigrationCommitted records an already-committed
// host data migration before Select checks or cancellation, independently of the
// selection. Prefer ConfirmDataVersion immediately after the host migration.
type ChangeOptions struct {
	InUse              []contract.PackageRef
	Enabled            []string
	DataVersion        uint32
	MigrationCommitted bool
	DeleteData         bool
}

type Removal struct {
	Removed      bool
	Pending      bool
	DataRetained bool
	Reason       string
}

// CommitError means the atomic catalog root was replaced. Callers must not retry
// under the assumption that the mutation did not happen.
type CommitError struct{ Err error }

func (e *CommitError) Error() string {
	return "catalog committed but durability/cleanup failed: " + e.Err.Error()
}
func (e *CommitError) Unwrap() error { return e.Err }

func newCatalog() catalog {
	return catalog{Installed: map[string]Package{}, Selected: map[string]contract.PackageRef{}, Entries: map[string]contract.Entry{}, DataVersions: map[string]uint32{}, Publishers: map[string]string{}}
}

// Open exclusively locks root until Close. Existing roots are recovered before
// any package is exposed. Default trust is deny-by-default.
func Open(root string, trust TrustPolicy) (s *Store, err error) {
	return OpenWithLimits(root, trust, DefaultLimits())
}
func OpenWithLimits(root string, trust TrustPolicy, limits Limits) (s *Store, err error) {
	return OpenWithOptions(root, Options{Trust: trust, Limits: limits})
}

func OpenWithOptions(root string, options Options) (s *Store, err error) {
	limits := options.Limits
	if limits == (Limits{}) {
		limits = DefaultLimits()
	}
	trust := options.Trust
	if err = limits.validate(); err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("storage root is not a real directory")
	}
	lock, err := acquireLock(filepath.Join(root, "store.lock"))
	if err != nil {
		return nil, err
	}
	s = &Store{root: root, trust: copyTrust(trust), limits: limits, decoder: options.Decode, catalog: newCatalog(), lock: lock}
	defer func() {
		if err != nil {
			err = errors.Join(err, releaseLock(lock))
			s = nil
		}
	}()
	for _, name := range []string{"content", "staging", "snapshots", "trash", "data"} {
		full := filepath.Join(root, name)
		if err = os.MkdirAll(full, 0700); err != nil {
			return nil, err
		}
		st, e := os.Lstat(full)
		if e != nil {
			return nil, e
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("managed %s is not a real directory", name)
		}
	}
	raw, e := os.ReadFile(filepath.Join(root, "catalog.json"))
	if e == nil {
		if s.catalog, err = decodeCatalog(raw); err != nil {
			return nil, fmt.Errorf("read durable catalog: %w", err)
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	if err = s.recover(); err != nil {
		return nil, fmt.Errorf("recover storage: %w", err)
	}
	return s, nil
}

func copyTrust(t TrustPolicy) TrustPolicy {
	out := TrustPolicy{Keys: map[string]TrustedKey{}, Publishers: map[string][]string{}, RevokedPublishers: map[string]bool{}, DevDigests: map[string]bool{}, PublisherChanges: map[string]map[string]bool{}}
	for k, v := range t.Keys {
		v.PublicKey = append([]byte(nil), v.PublicKey...)
		out.Keys[k] = v
	}
	for k, v := range t.Publishers {
		out.Publishers[k] = append([]string(nil), v...)
	}
	for k, v := range t.RevokedPublishers {
		out.RevokedPublishers[k] = v
	}
	for k, v := range t.DevDigests {
		out.DevDigests[k] = v
	}
	for id, list := range t.PublisherChanges {
		out.PublisherChanges[id] = map[string]bool{}
		for p, ok := range list {
			out.PublisherChanges[id][p] = ok
		}
	}
	return out
}
func (s *Store) UpdateTrust(trust TrustPolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	s.trust = copyTrust(trust)
	return nil
}
func (s *Store) Limits() Limits { s.mu.Lock(); defer s.mu.Unlock(); return s.limits }
func (s *Store) check() error {
	if s.closed {
		return os.ErrClosed
	}
	return nil
}
func validDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for _, c := range digest {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (s *Store) content(digest string) string { return filepath.Join(s.root, "content", digest) }

// Catalog adds three levels around manifests and entry configurations. Their
// independent depth/array budgets remain unchanged when embedded in this root.
const catalogMaxDepth = 32 + 3

func decodeCatalog(raw []byte) (catalog, error) {
	var c catalog
	if err := contract.DecodeWithLimits(raw, &c, catalogMaxDepth, 10000); err != nil {
		return c, err
	}
	return c, validateCatalog(c)
}

func validateCatalog(c catalog) error {
	if c.Installed == nil || c.Selected == nil || c.Entries == nil || c.DataVersions == nil || c.Publishers == nil {
		return fmt.Errorf("catalog is missing required maps")
	}
	for digest, p := range c.Installed {
		if digest != p.Ref.Digest || !validDigest(digest) || p.Ref.ID != p.Manifest.ID || p.Ref.Version != p.Manifest.Version {
			return fmt.Errorf("invalid installed catalog identity")
		}
		raw, err := json.Marshal(p.Manifest)
		if err != nil {
			return err
		}
		if err = contract.CheckJSON(raw, 32, 10000); err != nil {
			return fmt.Errorf("catalog manifest %s: %w", p.Ref.ID, err)
		}
		if err := ValidateManifest(p.Manifest); err != nil {
			return err
		}
	}
	for id, ref := range c.Selected {
		p, ok := c.Installed[ref.Digest]
		if !ok || id != ref.ID || p.Ref != ref {
			return fmt.Errorf("selected package is not installed: %s", id)
		}
	}
	for id, e := range c.Entries {
		if id != e.ID || !validName(id) || !validName(e.Package) {
			return fmt.Errorf("invalid catalog entry identity")
		}
		if err := contract.CheckJSON(e.Config, 32, 10000); err != nil {
			return fmt.Errorf("catalog entry %s configuration: %w", id, err)
		}
		if ref, ok := c.Selected[e.Package]; ok {
			if err := validateEntry(c.Installed[ref.Digest].Manifest, e); err != nil {
				return fmt.Errorf("catalog entry %s: %w", id, err)
			}
		} else if e.Enabled {
			return fmt.Errorf("enabled catalog entry %s has no selected package", id)
		}
	}
	entries := make([]contract.Entry, 0, len(c.Entries))
	for _, entry := range c.Entries {
		entries = append(entries, entry)
	}
	if err := contract.CheckEntryGraph(entries); err != nil {
		return err
	}
	for _, p := range c.Pending {
		if !validDigest(p.Ref.Digest) || p.Directory != "trash/"+p.Ref.Digest && p.Directory != "content/"+p.Ref.Digest {
			return fmt.Errorf("invalid pending removal path")
		}
	}
	for id, publisher := range c.Publishers {
		if !validName(id) || !validName(publisher) {
			return fmt.Errorf("invalid retained publisher identity")
		}
	}
	for id := range c.DataVersions {
		if !validName(id) {
			return fmt.Errorf("invalid committed data version package id")
		}
	}
	return nil
}

func (s *Store) recover() error {
	// Only the durable catalog is authoritative. No inference from staged content.
	for _, name := range []string{"staging", "snapshots"} {
		entries, err := os.ReadDir(filepath.Join(s.root, name))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err = os.RemoveAll(filepath.Join(s.root, name, e.Name())); err != nil {
				return err
			}
		}
		if err = syncDirectory(filepath.Join(s.root, name)); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.root, "content"))
	if err != nil {
		return err
	}
	pending := map[string]bool{}
	for _, p := range s.catalog.Pending {
		pending[p.Ref.Digest] = true
	}
	for _, e := range entries {
		if _, ok := s.catalog.Installed[e.Name()]; !ok && !pending[e.Name()] {
			if err = os.RemoveAll(s.content(e.Name())); err != nil {
				return err
			}
		}
	}
	if err = syncDirectory(filepath.Join(s.root, "content")); err != nil {
		return err
	}
	for digest := range s.catalog.Installed {
		st, e := os.Lstat(s.content(digest))
		if e != nil {
			return fmt.Errorf("committed content %s missing: %w", digest, e)
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("invalid committed content directory %s", digest)
		}
	}
	if err = s.retryPending(); err != nil {
		return err
	}
	for _, name := range []string{"transaction.json", "catalog.next"} {
		if err = os.Remove(filepath.Join(s.root, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncDirectory(s.root)
}

func (s *Store) cloneCatalog() (catalog, error) {
	c := newCatalog()
	c.Revision = s.catalog.Revision
	c.Transaction = s.catalog.Transaction
	for k, v := range s.catalog.Installed {
		c.Installed[k] = v
	}
	for k, v := range s.catalog.Selected {
		c.Selected[k] = v
	}
	for k, v := range s.catalog.Entries {
		c.Entries[k] = v
	}
	for k, v := range s.catalog.DataVersions {
		c.DataVersions[k] = v
	}
	for k, v := range s.catalog.Publishers {
		c.Publishers[k] = v
	}
	c.Pending = append([]pendingRemoval(nil), s.catalog.Pending...)
	return c, nil
}
func durableJSON(file string, value any) (err error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return durableBytes(file, raw)
}
func durableBytes(file string, raw []byte) (err error) {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
func syncDirectory(dir string) (err error) { return syncDir(dir) }
func (s *Store) observeCommit(stage commitStage) {
	if s.commitObserver != nil {
		s.commitObserver(stage)
	}
}
func (s *Store) begin(op, digest string) (transaction, error) {
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return transaction{}, fmt.Errorf("create transaction identity: %w", err)
	}
	tx := transaction{ID: hex.EncodeToString(identity[:]), Operation: op, Digest: digest}
	if err := durableJSON(filepath.Join(s.root, "transaction.json"), tx); err != nil {
		return tx, err
	}
	if err := syncDirectory(s.root); err != nil {
		return tx, err
	}
	s.observeCommit(commitJournalSynced)
	return tx, nil
}
func (s *Store) commit(c catalog, tx transaction) error {
	c.Revision = s.catalog.Revision + 1
	c.Transaction = tx.ID
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if _, err = decodeCatalog(raw); err != nil {
		return fmt.Errorf("validate candidate catalog: %w", err)
	}
	if err = durableBytes(filepath.Join(s.root, "catalog.next"), raw); err != nil {
		return err
	}
	s.observeCommit(commitCatalogSynced)
	if err := replaceFile(filepath.Join(s.root, "catalog.next"), filepath.Join(s.root, "catalog.json")); err != nil {
		return err
	}
	s.observeCommit(commitCatalogSwapped)
	s.catalog = c
	err = syncDirectory(s.root)
	if err == nil {
		s.observeCommit(commitRootSynced)
	}
	if e := os.Remove(filepath.Join(s.root, "transaction.json")); e != nil && !errors.Is(e, os.ErrNotExist) {
		err = errors.Join(err, e)
	}
	if err != nil {
		return &CommitError{err}
	}
	return nil
}
func (s *Store) mutate(ctx context.Context, op string, c catalog) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := s.begin(op, "")
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return s.commit(c, tx)
}

func (s *Store) Install(ctx context.Context, archive string) (Package, error) {
	return s.install(ctx, archive, false)
}
func (s *Store) ImportDirectory(ctx context.Context, dir string) (Package, error) {
	return s.install(ctx, dir, true)
}
func (s *Store) install(ctx context.Context, source string, dev bool) (p Package, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.check(); err != nil {
		return p, err
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	stage, err := os.MkdirTemp(filepath.Join(s.root, "staging"), "install-")
	if err != nil {
		return p, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()
	if dev {
		if err = copyDirectory(ctx, source, stage, s.limits); err != nil {
			return p, err
		}
		if _, e := os.Stat(filepath.Join(stage, IntegrityFile)); errors.Is(e, os.ErrNotExist) {
			err = generateIntegrity(ctx, stage, s.limits)
		} else {
			err = e
		}
	} else {
		err = extractZIP(ctx, source, stage, s.limits)
	}
	if err != nil {
		return p, err
	}
	p, input, sig, err := inspect(ctx, stage, source, dev, s.limits, s.decoder)
	if err != nil {
		return p, err
	}
	if _, err = Artifact(p.Manifest, runtime.GOOS, runtime.GOARCH); err != nil {
		return p, err
	}
	if sig == nil && s.trust.DevDigests[p.Ref.Digest] {
		p.Development = true
	}
	if err = s.trust.verify(p.Manifest, p.Ref.Digest, input, sig, p.Development); err != nil {
		return p, err
	}
	if old := s.catalog.Publishers[p.Ref.ID]; old != "" && old != p.Manifest.Publisher && !s.trust.PublisherChanges[p.Ref.ID][p.Manifest.Publisher] {
		return p, fmt.Errorf("publisher change for %s from %s to %s requires explicit approval", p.Ref.ID, old, p.Manifest.Publisher)
	}
	for _, pending := range s.catalog.Pending {
		if pending.Ref.ID == p.Ref.ID {
			return p, fmt.Errorf("package %s still has pending removal", p.Ref.ID)
		}
	}
	for _, old := range s.catalog.Installed {
		if old.Ref.ID != p.Ref.ID {
			continue
		}
		if old.Ref.Version == p.Ref.Version && old.Ref.Digest != p.Ref.Digest {
			return p, fmt.Errorf("same-version content conflict: %s (%s) versus %s (%s)", old.Source, old.Ref.Digest, p.Source, p.Ref.Digest)
		}
		if old.Manifest.Publisher != p.Manifest.Publisher && !s.trust.PublisherChanges[p.Ref.ID][p.Manifest.Publisher] {
			return p, fmt.Errorf("publisher change for %s from %s to %s requires explicit approval", p.Ref.ID, old.Manifest.Publisher, p.Manifest.Publisher)
		}
	}
	if old, ok := s.catalog.Installed[p.Ref.Digest]; ok {
		old.Directory = s.content(old.Ref.Digest)
		return clonePackageChecked(old)
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	c, err := s.cloneCatalog()
	if err != nil {
		return p, err
	}
	tx, err := s.begin("install", p.Ref.Digest)
	if err != nil {
		return p, err
	}
	if _, e := os.Lstat(s.content(p.Ref.Digest)); e == nil {
		if err = os.RemoveAll(s.content(p.Ref.Digest)); err != nil {
			return p, err
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return p, e
	}
	if err = os.Rename(stage, s.content(p.Ref.Digest)); err != nil {
		return p, err
	}
	s.observeCommit(commitContentMoved)
	// The content rename precedes the sole commit point. Recovery removes orphans.
	if err = syncTree(s.content(p.Ref.Digest)); err != nil {
		return p, errors.Join(err, os.RemoveAll(s.content(p.Ref.Digest)))
	}
	if err = syncDirectory(filepath.Join(s.root, "content")); err != nil {
		return p, errors.Join(err, os.RemoveAll(s.content(p.Ref.Digest)))
	}
	p.Directory = s.content(p.Ref.Digest)
	stored, e := clonePackageChecked(p)
	if e != nil {
		return p, errors.Join(e, os.RemoveAll(p.Directory))
	}
	c.Installed[p.Ref.Digest] = stored
	c.Publishers[p.Ref.ID] = p.Manifest.Publisher
	if err = ctx.Err(); err != nil {
		return p, errors.Join(err, os.RemoveAll(p.Directory))
	}
	err = s.commit(c, tx)
	if err != nil {
		var committed *CommitError
		if !errors.As(err, &committed) {
			err = errors.Join(err, os.RemoveAll(p.Directory))
		}
	}
	// Cancellation after commit is intentionally not reported as a failed install.
	return p, err
}
func clonePackageChecked(p Package) (Package, error) {
	m := p.Manifest
	m.Definitions = append([]contract.DefinitionSpec(nil), m.Definitions...)
	for i := range m.Definitions {
		m.Definitions[i].Requires = append([]contract.Requirement(nil), m.Definitions[i].Requires...)
		m.Definitions[i].Provides = append([]contract.Service(nil), m.Definitions[i].Provides...)
	}
	m.Runtime.Artifacts = append([]contract.Artifact(nil), m.Runtime.Artifacts...)
	m.Dependencies = append([]contract.Dependency(nil), m.Dependencies...)
	m.OptionalDependencies = append([]contract.Dependency(nil), m.OptionalDependencies...)
	m.Capabilities = append([]contract.Capability(nil), m.Capabilities...)
	for i := range m.Capabilities {
		m.Capabilities[i].Scope = append([]byte(nil), m.Capabilities[i].Scope...)
	}
	m.Contributions = append([]contract.Contribution(nil), m.Contributions...)
	for i := range m.Contributions {
		m.Contributions[i].Data = append([]byte(nil), m.Contributions[i].Data...)
	}
	m.ConfigSchema = append([]byte(nil), m.ConfigSchema...)
	p.Manifest = m
	return p, nil
}
func (s *Store) get(ref contract.PackageRef) (Package, error) {
	p, ok := s.catalog.Installed[ref.Digest]
	if !ok || p.Ref != ref {
		return Package{}, fmt.Errorf("package reference is not installed: %s@%s#%s", ref.ID, ref.Version, ref.Digest)
	}
	p.Directory = s.content(ref.Digest)
	return clonePackageChecked(p)
}
func (s *Store) Get(ref contract.PackageRef) (Package, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return Package{}, err
	}
	return s.get(ref)
}
func (s *Store) Selected(id string) (Package, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return Package{}, err
	}
	ref, ok := s.catalog.Selected[id]
	if !ok {
		return Package{}, fmt.Errorf("package %s has no explicit selection", id)
	}
	return s.get(ref)
}
func (s *Store) Installed() ([]Package, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return nil, err
	}
	var out []Package
	for _, p := range s.catalog.Installed {
		q, err := s.get(p.Ref)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ref.ID != out[j].Ref.ID {
			return out[i].Ref.ID < out[j].Ref.ID
		}
		return out[i].Ref.Version < out[j].Ref.Version
	})
	return out, nil
}

func preflight(c catalog, ref contract.PackageRef) ([]string, error) {
	var diagnostics []string
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(contract.PackageRef) error
	visit = func(current contract.PackageRef) error {
		if visiting[current.ID] {
			return fmt.Errorf("package dependency cycle at %s", current.ID)
		}
		if visited[current.ID] {
			return nil
		}
		p, ok := c.Installed[current.Digest]
		if !ok || p.Ref != current {
			return fmt.Errorf("missing package %s", current.ID)
		}
		visiting[current.ID] = true
		for _, dep := range p.Manifest.Dependencies {
			selected, ok := c.Selected[dep.ID]
			if !ok {
				return fmt.Errorf("%s requires missing selected package %s", current.ID, dep.ID)
			}
			accepts, err := acceptsDependency(selected.Version, dep)
			if err != nil {
				return err
			}
			if !accepts {
				return fmt.Errorf("%s requires %s [%s,%s), selected %s", current.ID, dep.ID, dep.Min, dep.Max, selected.Version)
			}
			if err = visit(selected); err != nil {
				return err
			}
		}
		for _, dep := range p.Manifest.OptionalDependencies {
			selected, ok := c.Selected[dep.ID]
			if !ok {
				diagnostics = append(diagnostics, fmt.Sprintf("%s: optional dependency %s is not selected", current.ID, dep.ID))
				continue
			}
			accepts, err := acceptsDependency(selected.Version, dep)
			if err != nil {
				return err
			}
			if !accepts {
				diagnostics = append(diagnostics, fmt.Sprintf("%s: optional dependency %s version %s is incompatible", current.ID, dep.ID, selected.Version))
			}
		}
		visiting[current.ID] = false
		visited[current.ID] = true
		return nil
	}
	err := visit(ref)
	return diagnostics, err
}
func (s *Store) Preflight(ref contract.PackageRef) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return nil, err
	}
	return preflight(s.catalog, ref)
}
func inUse(ref contract.PackageRef, refs []contract.PackageRef) bool {
	for _, r := range refs {
		if r == ref {
			return true
		}
	}
	return false
}

// ConfirmDataVersion durably records an already-committed host data migration,
// independently of package selection, trust, dependencies or installed code.
// It cannot lower the data-version floor; repeating the same version is a no-op.
// Cancellation is intentionally ignored: it cannot undo the host's data change.
// A CommitError means the confirmation passed the catalog commit point.
func (s *Store) ConfirmDataVersion(ctx context.Context, id string, version uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	return s.confirmDataVersion(ctx, id, version)
}

func (s *Store) confirmDataVersion(ctx context.Context, id string, version uint32) error {
	if !validName(id) {
		return fmt.Errorf("invalid data version package id")
	}
	if version < s.catalog.DataVersions[id] {
		return fmt.Errorf("cannot lower committed data version for %s", id)
	}
	if current, ok := s.catalog.DataVersions[id]; ok && current == version {
		return nil
	}
	c, err := s.cloneCatalog()
	if err != nil {
		return err
	}
	c.DataVersions[id] = version
	return s.mutate(context.WithoutCancel(ctx), "confirm-data-version", c)
}

// CheckSelect performs the same compatibility/configuration checks as Select,
// without mutation and without requiring the old activation to have stopped.
func (s *Store) CheckSelect(ref contract.PackageRef, opt ChangeOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	_, err := s.prepareSelection(ref, opt)
	return err
}
func (s *Store) prepareSelection(ref contract.PackageRef, opt ChangeOptions) (catalog, error) {
	p, err := s.get(ref)
	if err != nil {
		return catalog{}, err
	}
	// Reject corrupt/revoked replacement code before the facade quiesces the old
	// activation. The eventual execution Snapshot independently verifies again.
	checked, input, sig, err := inspect(context.Background(), p.Directory, p.Source, p.Development, s.limits, s.decoder)
	if err != nil {
		return catalog{}, err
	}
	if checked.Ref != ref {
		return catalog{}, fmt.Errorf("managed package digest changed")
	}
	if _, err = Artifact(checked.Manifest, runtime.GOOS, runtime.GOARCH); err != nil {
		return catalog{}, err
	}
	if err = s.trust.verify(checked.Manifest, ref.Digest, input, sig, p.Development); err != nil {
		return catalog{}, err
	}
	dataVersion := max(s.catalog.DataVersions[ref.ID], opt.DataVersion)
	if p.Manifest.DataVersion < dataVersion {
		return catalog{}, fmt.Errorf("data recovery required: package data version %d is below committed data version %d", p.Manifest.DataVersion, dataVersion)
	}
	c, err := s.cloneCatalog()
	if err != nil {
		return c, err
	}
	c.Selected[ref.ID] = ref
	if _, err = preflight(c, ref); err != nil {
		return c, err
	}
	var blocked []string
	for _, id := range opt.Enabled {
		if selected, ok := c.Selected[id]; ok {
			if _, e := preflight(c, selected); e != nil {
				blocked = append(blocked, id+": "+e.Error())
			}
		}
	}
	if len(blocked) > 0 {
		return c, fmt.Errorf("enabled reverse dependencies block selection: %s", strings.Join(blocked, "; "))
	}
	for _, entry := range c.Entries {
		if entry.Package == ref.ID {
			if err = validateEntry(p.Manifest, entry); err != nil {
				return c, fmt.Errorf("entry %s prevents selection: %w", entry.ID, err)
			}
		}
	}
	if opt.MigrationCommitted && opt.DataVersion != p.Manifest.DataVersion {
		return c, fmt.Errorf("committed migration version must match selected package data version")
	}
	return c, nil
}

// Select changes the selected code only after all checks pass. A reported
// MigrationCommitted fact is persisted first, even if selection is canceled or
// rejected; that confirmation can block the still-selected old code.
func (s *Store) Select(ctx context.Context, ref contract.PackageRef, opt ChangeOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if opt.MigrationCommitted {
		if err := s.confirmDataVersion(ctx, ref.ID, opt.DataVersion); err != nil {
			return fmt.Errorf("confirm committed migration: %w", err)
		}
	}
	c, err := s.prepareSelection(ref, opt)
	if err != nil {
		return err
	}
	old, exists := s.catalog.Selected[ref.ID]
	if exists && old != ref && inUse(old, opt.InUse) {
		return fmt.Errorf("old activation %s must be stopped before selection", ref.ID)
	}
	return s.mutate(ctx, "select", c)
}

func validateEntry(m contract.Manifest, e contract.Entry) error {
	if !validName(e.ID) || e.Realm != "" && !validName(e.Realm) || e.Parent != "" && !validName(e.Parent) || e.Package != m.ID {
		return fmt.Errorf("invalid entry identity or realm")
	}
	found := false
	for _, d := range m.Definitions {
		if d.ID == e.Definition {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("unknown definition %s", e.Definition)
	}
	return validateConfig(m.ConfigSchema, e.Config)
}
func (s *Store) Entries() ([]contract.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return nil, err
	}
	var out []contract.Entry
	for _, e := range s.catalog.Entries {
		e.Config = append([]byte(nil), e.Config...)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (s *Store) SaveEntry(ctx context.Context, e contract.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	ref, ok := s.catalog.Selected[e.Package]
	if !ok {
		return fmt.Errorf("entry package %s is not selected", e.Package)
	}
	p, err := s.get(ref)
	if err != nil {
		return err
	}
	if err = validateEntry(p.Manifest, e); err != nil {
		return err
	}
	c, err := s.cloneCatalog()
	if err != nil {
		return err
	}
	e.Config = append([]byte(nil), e.Config...)
	c.Entries[e.ID] = e
	return s.mutate(ctx, "save-entry", c)
}
func (s *Store) DeleteEntry(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	c, err := s.cloneCatalog()
	if err != nil {
		return err
	}
	delete(c.Entries, id)
	return s.mutate(ctx, "delete-entry", c)
}

// Snapshot is the only supported execution source. Close after the activation
// and all runtime handles have stopped. Revocation is checked anew each time.
type Snapshot struct {
	Package   Package
	Directory string
	mu        sync.Mutex
	closed    bool
}

func (p *Snapshot) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	if err := os.RemoveAll(p.Directory); err != nil {
		return err
	}
	p.closed = true
	return nil
}
func (s *Store) Snapshot(ctx context.Context, ref contract.PackageRef) (snapshot *Snapshot, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.check(); err != nil {
		return nil, err
	}
	p, err := s.get(ref)
	if err != nil {
		return nil, err
	}
	if s.catalog.Selected[ref.ID] != ref {
		return nil, fmt.Errorf("package must be explicitly selected before execution")
	}
	if p.Manifest.DataVersion < s.catalog.DataVersions[ref.ID] {
		return nil, fmt.Errorf("data recovery required before executing older code")
	}
	dir, err := os.MkdirTemp(filepath.Join(s.root, "snapshots"), "activation-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(dir))
		}
	}()
	if err = copyDirectory(ctx, p.Directory, dir, s.limits); err != nil {
		return nil, err
	}
	checked, input, sig, err := inspect(ctx, dir, p.Source, p.Development, s.limits, s.decoder)
	if err != nil {
		return nil, err
	}
	if checked.Ref != ref {
		return nil, fmt.Errorf("managed package digest changed")
	}
	if _, err = Artifact(checked.Manifest, runtime.GOOS, runtime.GOARCH); err != nil {
		return nil, err
	}
	if err = s.trust.verify(checked.Manifest, ref.Digest, input, sig, p.Development); err != nil {
		return nil, err
	}
	if _, err = preflight(s.catalog, ref); err != nil {
		return nil, err
	}
	return &Snapshot{Package: checked, Directory: dir}, nil
}

func (s *Store) Uninstall(ctx context.Context, ref contract.PackageRef, opt ChangeOptions) (result Removal, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result.DataRetained = !opt.DeleteData
	if err = s.check(); err != nil {
		return result, err
	}
	if _, err = s.get(ref); err != nil {
		return result, err
	}
	if inUse(ref, opt.InUse) {
		return result, fmt.Errorf("package is still in use")
	}
	selected := s.catalog.Selected[ref.ID] == ref
	if selected {
		candidate, cloneErr := s.cloneCatalog()
		if cloneErr != nil {
			return result, cloneErr
		}
		delete(candidate.Selected, ref.ID)
		var blocked []string
		for _, id := range opt.Enabled {
			if id == ref.ID {
				blocked = append(blocked, id+": package must be disabled before uninstall")
				continue
			}
			if r, ok := candidate.Selected[id]; ok {
				if _, e := preflight(candidate, r); e != nil {
					blocked = append(blocked, id+": "+e.Error())
				}
			}
		}
		if len(blocked) > 0 {
			return result, fmt.Errorf("enabled reverse dependencies block uninstall: %s", strings.Join(blocked, "; "))
		}
	}
	if opt.DeleteData {
		for _, p := range s.catalog.Installed {
			if p.Ref.ID == ref.ID && p.Ref != ref {
				return result, fmt.Errorf("cannot delete shared data while another version is installed")
			}
		}
	}
	c, err := s.cloneCatalog()
	if err != nil {
		return result, err
	}
	delete(c.Installed, ref.Digest)
	if selected {
		delete(c.Selected, ref.ID)
		for id, e := range c.Entries {
			if e.Package == ref.ID {
				e.Enabled = false
				c.Entries[id] = e
			}
		}
	}
	c.Pending = append(c.Pending, pendingRemoval{Ref: ref, Directory: "content/" + ref.Digest, DeleteData: opt.DeleteData})
	if err = s.mutate(ctx, "uninstall", c); err != nil {
		var committed *CommitError
		if errors.As(err, &committed) {
			result.Pending = true
		}
		return result, err
	}
	result.Pending = true
	if err = s.retryPending(); err != nil {
		return result, err
	}
	for _, p := range s.catalog.Pending {
		if p.Ref == ref {
			result.Reason = p.Error
			return result, nil
		}
	}
	result.Pending = false
	result.Removed = true
	return result, nil
}
func (s *Store) retryPending() error {
	if len(s.catalog.Pending) == 0 {
		return nil
	}
	c, err := s.cloneCatalog()
	if err != nil {
		return err
	}
	remaining := make([]pendingRemoval, 0, len(c.Pending))
	for _, p := range c.Pending {
		source := filepath.Join(s.root, filepath.FromSlash(p.Directory))
		target := filepath.Join(s.root, "trash", p.Ref.Digest)
		if p.Directory != "trash/"+p.Ref.Digest {
			if err = os.Rename(source, target); err != nil && !errors.Is(err, os.ErrNotExist) {
				p.Error = err.Error()
				remaining = append(remaining, p)
				continue
			}
			p.Directory = "trash/" + p.Ref.Digest
		}
		if err = os.RemoveAll(target); err != nil {
			p.Error = err.Error()
			remaining = append(remaining, p)
			continue
		}
		if p.DeleteData {
			if err = os.RemoveAll(filepath.Join(s.root, "data", dataNamespace(p.Ref.ID))); err != nil {
				p.Error = err.Error()
				remaining = append(remaining, p)
				continue
			}
			delete(c.DataVersions, p.Ref.ID)
			delete(c.Publishers, p.Ref.ID)
		}
	}
	c.Pending = remaining
	for _, name := range []string{"content", "trash", "data"} {
		if err = syncDirectory(filepath.Join(s.root, name)); err != nil {
			return err
		}
	}
	// Pending-removal failures remain durable and observable, not success claims.
	tx, err := s.begin("cleanup", "")
	if err != nil {
		return err
	}
	return s.commit(c, tx)
}

// PendingRemovals returns references still held by the OS or external programs.
func (s *Store) PendingRemovals() ([]contract.PackageRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return nil, err
	}
	var out []contract.PackageRef
	for _, p := range s.catalog.Pending {
		out = append(out, p.Ref)
	}
	return out, nil
}
func (s *Store) RetryRemovals() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if err := s.retryPending(); err != nil {
		return err
	}
	if len(s.catalog.Pending) > 0 {
		return fmt.Errorf("pending code removal: %s", s.catalog.Pending[0].Error)
	}
	return nil
}
func (s *Store) DataDirectory(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return "", err
	}
	if !validName(id) {
		return "", fmt.Errorf("invalid plugin id")
	}
	dir := filepath.Join(s.root, "data", dataNamespace(id))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return releaseLock(s.lock)
}

func dataNamespace(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}
func syncTree(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(name string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			dirs = append(dirs, name)
			return nil
		}
		file, e := os.OpenFile(name, os.O_RDWR, 0)
		if e != nil {
			return e
		}
		return errors.Join(file.Sync(), file.Close())
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err = syncDirectory(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}
