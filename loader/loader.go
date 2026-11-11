package loader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/config"
)

// Config configures Plugin.
type Config struct {
	// File is the configuration file. YAML files can be written back; JSON
	// and TOML files are read-only.
	File string
	// Registry resolves plugin names and decodes entry settings.
	Registry *Registry
	// Resolvers are consulted, in order, for names the registry does not know.
	Resolvers []Resolver
	// Watch, when positive, re-reads the files at this interval and applies
	// what changed. Zero disables it; call Loader.Reload instead.
	Watch time.Duration
	// Logger receives problems that have no caller to return to, such as a
	// failed write-back retry.
	Logger *slog.Logger
}

// Service is the key under which Plugin provides the loader.
var Service = cordis.NewService[*Loader]("loader")

// Ready is a requirement satisfied once the entries have settled for the
// first time: none is starting or stopping. It stays satisfied from then on,
// so an instance that started because of it is not stopped when a later
// edit makes entries busy again.
var Ready = Service.Where(func(l *Loader) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.reached {
		return errors.New("loader: entries are still starting or stopping")
	}
	return nil
})

// ChangeKind tells what happened to an entry.
type ChangeKind string

const (
	Created ChangeKind = "create"
	Updated ChangeKind = "update"
	Removed ChangeKind = "remove"
	Moved   ChangeKind = "move"
)

// Change describes one change of the entry tree.
type Change struct {
	ID   string // global identifier of the entry
	Kind ChangeKind
}

// Changed is emitted after an edit or a reload has changed the entry tree.
var Changed = cordis.NewEvent[Change]("loader/changed")

// current marks the scope of each entry with its identifier.
var current = cordis.NewSetting[string](Service)

// EntryOf returns the identifier of the entry that s was loaded for.
func EntryOf(s *cordis.Scope) (id string, ok bool) {
	ids := current.Values(s)
	if len(ids) == 0 {
		return "", false
	}
	return ids[len(ids)-1], true
}

// Plugin reads Config.File, loads its entries as instances and provides
// Service. Stopping it stops every entry and writes out pending edits.
var Plugin = cordis.Plugin[Config]{
	Name: "loader",
	Validate: func(c Config) error {
		if c.File == "" || c.Registry == nil {
			return errors.New("loader: File and Registry are required")
		}
		return nil
	},
	Start: func(ctx context.Context, s *cordis.Scope, c Config) error {
		l := &Loader{config: c, scope: s, ops: make(chan struct{}, 1), changed: make(chan struct{}),
			realms: map[string]*cordis.Realm{}, write: writeFile}
		src, err := read(c.File)
		if err != nil {
			return err
		}
		l.root = l.newTree(src, "", s)
		l.root.adopt(nil, src.document.Plugins, "")
		s.Defer(func(ctx context.Context) error { return l.flushAll(ctx) })
		cordis.InstanceChanged.On(s, func(context.Context, cordis.InstanceChange) error {
			l.settled()
			return nil
		})
		if err := Service.Provide(s, l); err != nil {
			return err
		}
		l.mu.Lock()
		l.kickLocked()
		l.mu.Unlock()
		if c.Watch > 0 {
			s.Go(func(ctx context.Context) error { return l.watch(ctx, c.Watch) })
		}
		return nil
	},
}

// Loader is the service that owns the entry tree.
type Loader struct {
	config Config
	scope  *cordis.Scope
	ops    chan struct{} // serializes edits, reloads and writes
	write  func(path string, data []byte) error

	mu      sync.Mutex
	changed chan struct{} // closed and replaced whenever stability may have changed
	root    *tree
	realms  map[string]*cordis.Realm
	driving bool
	dirty   bool

	activity   uint64 // bumped whenever something may have started or stopped
	steady     bool   // stability has been announced to Ready consumers
	reached    bool   // the entries have settled at least once; never reset
	confirming bool   // a confirmation of stability is under way
}

// tree is the set of entries that come from one file.
type tree struct {
	loader  *Loader
	source  *source
	prefix  string // "" for the root file, "<include id>:" below it
	scope   *cordis.Scope
	entries []*entry
	byID    map[string]*entry
	nextID  int
	tombs   []*entry // entries that left the tree while the file still has them

	// Set for the tree of an included file.
	owner     *entry           // the entry that includes it
	patches   []patch          // the owner's patches
	inserted  map[int][]*entry // what each inserting patch has inserted
	unapplied []error          // patches that could not be applied
}

// entry is one node of the tree together with what currently runs for it.
type entry struct {
	tree      *tree
	id        string
	anonymous bool
	spec      spec // own fields; children live in the children slice
	parent    *entry
	children  []*entry

	// What the file has, as last read or written. Comparing it with the
	// fields above tells which edits the file does not have yet.
	written    bool   // the file has the entry
	read       spec   // its own fields in the file
	readParent *entry // its group in the file
	path       string // its JSON Pointer in the file
	moved      bool   // an edit moved it since
	gone       bool   // removed, and the file does not have it either

	patched map[string]int             // keys whose value comes from a patch, by patch index
	shadow  map[string]json.RawMessage // what an unwritten entry has under those keys
	origin  *origin                    // set when the entry itself comes from a patch

	generation uint64 // bumped by changes that need a reload
	revision   uint64 // bumped by configuration changes
	removed    bool

	running bool
	ran     uint64 // generation that is running
	applied uint64 // configuration revision that is running
	scope   *cordis.Scope
	release func(context.Context) error
	subtree *tree // include: the entries of the included file
	handle  Handle
	wrapper *cordis.Instance[struct{}]
	err     error
}

func (l *Loader) newTree(src *source, prefix string, scope *cordis.Scope) *tree {
	return &tree{loader: l, source: src, prefix: prefix, scope: scope, byID: map[string]*entry{}, inserted: map[int][]*entry{}}
}

// adopt builds entries for specs under parent and returns them.
func (t *tree) adopt(parent *entry, specs []spec, path string) []*entry {
	list := make([]*entry, 0, len(specs))
	for index, s := range specs {
		e := t.newEntry(parent, s, true)
		e.path = path + "/plugins/" + strconv.Itoa(index)
		e.children = t.adopt(e, s.Plugins, e.path)
		list = append(list, e)
	}
	if parent == nil {
		t.entries = list
	}
	return list
}

func (t *tree) newEntry(parent *entry, s spec, fromFile bool) *entry {
	e := &entry{tree: t, parent: parent, spec: s.own(), id: s.ID, generation: 1, revision: 1}
	if fromFile {
		e.read, e.readParent, e.written = s.own(), parent, true
	}
	if e.id == "" {
		t.nextID++
		e.id, e.anonymous = "~"+strconv.Itoa(t.nextID), true
	}
	t.byID[e.id] = e
	return e
}

func (e *entry) globalID() string { return e.tree.prefix + e.id }

func (e *entry) siblings() *[]*entry {
	if e.parent == nil {
		return &e.tree.entries
	}
	return &e.parent.children
}

// up returns the entry above this one, crossing into the including file.
func (e *entry) up() *entry {
	if e.parent != nil {
		return e.parent
	}
	return e.tree.owner
}

// below returns the entries that run under this one.
func (e *entry) below() []*entry {
	if e.subtree != nil {
		return e.subtree.entries
	}
	return e.children
}

// blocked reports whether the entry or one of its ancestors is disabled or removed.
func (e *entry) blocked() bool {
	for ; e != nil; e = e.up() {
		if e.spec.Disabled || e.removed {
			return true
		}
	}
	return false
}

// kickLocked makes sure the reconciler runs.
func (l *Loader) kickLocked() {
	l.dirty = true
	l.activity++
	l.steady = false
	if l.driving {
		return
	}
	l.driving = true
	l.scope.Go(func(ctx context.Context) error {
		l.reconcile(ctx)
		return nil
	})
}

// reconcile brings what runs in line with the tree: everything that must go
// is stopped first, deepest entries first, then what should run is started
// from the top down. Stops and configuration updates wait for the instance;
// starts do not.
func (l *Loader) reconcile(ctx context.Context) {
	for {
		l.mu.Lock()
		if ctx.Err() != nil {
			l.driving = false
			l.mu.Unlock()
			return
		}
		l.dirty = false
		action := l.root.next()
		if action == nil {
			if l.dirty {
				l.mu.Unlock()
				continue
			}
			l.driving = false
			l.mu.Unlock()
			l.settled()
			return
		}
		l.mu.Unlock()
		action(ctx)
	}
}

// next returns the next step for this tree, or nil when it is in line.
func (t *tree) next() func(context.Context) {
	if step := stopStep(t.entries); step != nil {
		return step
	}
	return startStep(t.entries, t.scope)
}

func (e *entry) wanted() bool { return !e.blocked() }

// stopStep finds an entry to stop. An entry is stopped only after everything
// running below it has stopped.
func stopStep(list []*entry) func(context.Context) {
	for _, e := range list {
		stale := e.running && (!e.wanted() || e.ran != e.generation)
		if stale {
			if step := stopBelow(e.below()); step != nil {
				return step
			}
			return e.stop
		}
		if step := stopStep(e.below()); step != nil {
			return step
		}
		if e.removed && !e.running {
			return e.forget
		}
	}
	return nil
}

func stopBelow(list []*entry) func(context.Context) {
	for _, e := range list {
		if step := stopBelow(e.below()); step != nil {
			return step
		}
		if e.running {
			return e.stop
		}
	}
	return nil
}

func startStep(list []*entry, parent *cordis.Scope) func(context.Context) {
	for _, e := range list {
		if !e.wanted() {
			continue
		}
		if !e.running && (e.ran != e.generation || e.applied != e.revision) {
			return func(context.Context) { e.start(parent) }
		}
		if e.running && e.handle != nil && e.applied != e.revision {
			return e.update
		}
		if e.running && e.wrapper != nil && e.handle == nil && e.applied != e.revision {
			return e.rewrap
		}
		if e.running && e.scope != nil {
			if step := startStep(e.below(), e.scope); step != nil {
				return step
			}
		}
	}
	return nil
}

// start runs the entry on a scope derived from parent.
func (e *entry) start(parent *cordis.Scope) {
	l := e.tree.loader
	l.mu.Lock()
	s, generation, revision := e.spec, e.generation, e.revision
	var files []string
	for owner := e; owner != nil; owner = owner.tree.owner {
		files = append(files, owner.tree.source.path)
	}
	l.mu.Unlock()

	result := entry{}
	scope, err := l.derive(parent, e.globalID(), s)
	if err == nil {
		switch s.kind() {
		case "group":
			result.scope, result.release = scope.Sub()
		case "plugin":
			err = e.launch(scope, s, &result)
		case "include":
			err = e.include(scope, s, files, &result)
		}
	}
	l.mu.Lock()
	e.ran, e.applied = generation, revision
	e.err = err
	if err == nil {
		e.running = true
		e.scope, e.release, e.wrapper = result.scope, result.release, result.wrapper
		if result.handle != nil {
			e.handle = result.handle
		}
		if e.subtree = result.subtree; e.subtree != nil {
			e.subtree.applyPatches()
		}
	}
	l.mu.Unlock()
}

// derive applies the entry's isolation and settings to the parent scope.
func (l *Loader) derive(parent *cordis.Scope, id string, s spec) (*cordis.Scope, error) {
	scope := current.With(parent, id)
	for _, service := range sortedKeys(s.Isolate) {
		name := cordis.Name(service)
		label := s.Isolate[service].label
		if label == "" {
			scope = scope.Isolate(name)
			continue
		}
		l.mu.Lock()
		realm := l.realms[label]
		if realm == nil {
			realm = cordis.NewRealm(label)
			l.realms[label] = realm
		}
		l.mu.Unlock()
		scope = scope.IsolateIn(realm, name)
	}
	for _, service := range sortedKeys(s.Settings) {
		apply := l.config.Registry.settings[service]
		if apply == nil {
			return nil, under("/settings/"+service, fmt.Errorf("no setting is registered for service %q", service))
		}
		next, err := apply(scope, s.Settings[service])
		if err != nil {
			return nil, under("/settings/"+service, err)
		}
		scope = next
	}
	return scope, nil
}

func (l *Loader) resolve(name string) (Loadable, bool) {
	if loadable, ok := l.config.Registry.Resolve(name); ok {
		return loadable, true
	}
	for _, resolver := range l.config.Resolvers {
		if loadable, ok := resolver.Resolve(name); ok {
			return loadable, true
		}
	}
	return nil, false
}

// launch starts the plugin of an entry. With entry-level requirements it is
// loaded inside a wrapper instance that waits for them.
func (e *entry) launch(scope *cordis.Scope, s spec, result *entry) error {
	l := e.tree.loader
	loadable, ok := l.resolve(s.Plugin)
	if !ok {
		return under("/plugin", fmt.Errorf("unknown plugin %q", s.Plugin))
	}
	if len(s.Requires) == 0 {
		handle, err := loadable.Load(scope, s.Config)
		if err != nil {
			return under("/config", err)
		}
		result.handle = handle
		return nil
	}
	requires := make([]cordis.Requirement, len(s.Requires))
	for i, service := range s.Requires {
		requires[i] = cordis.Name(service)
	}
	result.wrapper = cordis.Plugin[struct{}]{Name: e.globalID(), Requires: requires,
		Start: func(_ context.Context, inner *cordis.Scope, _ struct{}) error {
			// The requirements may arrive long after the entry was started;
			// use the configuration of that moment.
			l.mu.Lock()
			raw, revision := e.spec.Config, e.revision
			l.mu.Unlock()
			handle, err := loadable.Load(inner, raw)
			l.mu.Lock()
			defer l.mu.Unlock()
			if err != nil {
				return e.locate(under("/config", err))
			}
			e.handle, e.applied, e.err = handle, revision, nil
			// The handle dies with this activation of the wrapper.
			inner.Defer(func(context.Context) error {
				l.mu.Lock()
				if e.handle == handle {
					e.handle = nil
				}
				l.mu.Unlock()
				return nil
			})
			return nil
		}}.Load(scope, struct{}{})
	return nil
}

// update applies a changed configuration to the running instance.
func (e *entry) update(ctx context.Context) {
	l := e.tree.loader
	l.mu.Lock()
	handle, raw, revision := e.handle, e.spec.Config, e.revision
	l.mu.Unlock()
	err := handle.Update(ctx, raw)
	l.mu.Lock()
	e.applied = revision
	if err != nil {
		e.err = under("/config", err)
	} else {
		e.err = nil
	}
	l.mu.Unlock()
}

// rewrap handles a changed configuration while the plugin of an entry with
// requirements is not running. A wrapper that waits picks the configuration
// up when it starts; one that failed is started again.
func (e *entry) rewrap(ctx context.Context) {
	l := e.tree.loader
	l.mu.Lock()
	wrapper := e.wrapper
	e.applied = e.revision
	l.mu.Unlock()
	if wrapper.Status().State == cordis.Failed {
		wrapper.Restart(ctx)
	}
}

// stop takes down what runs for the entry and waits for it.
func (e *entry) stop(ctx context.Context) {
	l := e.tree.loader
	l.mu.Lock()
	handle, wrapper, release, subtree := e.handle, e.wrapper, e.release, e.subtree
	l.mu.Unlock()
	stopCtx := context.WithoutCancel(ctx)
	var err error
	if subtree != nil {
		// The included tree goes away with the entry: write what it still
		// holds, because a restart reads the file again.
		l.lock(stopCtx)
		_, err = subtree.flush(stopCtx)
		l.unlock()
		if err != nil && l.config.Logger != nil {
			l.config.Logger.Error("pending edits of an included file were lost", "entry", e.globalID(), "error", err)
		}
	}
	switch {
	case wrapper != nil:
		err = wrapper.Stop(stopCtx)
	case handle != nil:
		err = handle.Stop(stopCtx)
	case release != nil:
		err = release(stopCtx)
	}
	if err != nil && l.config.Logger != nil {
		l.config.Logger.Error("entry stopped with an error", "entry", e.globalID(), "error", err)
	}
	l.mu.Lock()
	e.running = false
	e.scope, e.release, e.handle, e.wrapper, e.subtree = nil, nil, nil, nil, nil
	e.ran = 0
	l.mu.Unlock()
}

// forget drops a removed entry that no longer runs.
func (e *entry) forget(context.Context) {
	l := e.tree.loader
	l.mu.Lock()
	defer l.mu.Unlock()
	list := e.siblings()
	*list = slices.DeleteFunc(*list, func(other *entry) bool { return other == e })
	// An entry the file still has is kept aside until its removal is
	// written, so that a reload can tell it from a new entry.
	e.each(func(gone *entry) {
		if gone.gone {
			e.tree.release(gone)
		} else {
			e.tree.tombs = append(e.tree.tombs, gone)
		}
	})
}

// stable reports whether nothing is starting or stopping. Entries that wait
// for a missing service count as stable.
func (l *Loader) stableLocked() bool {
	if l.driving {
		return false
	}
	busy := false
	for _, t := range l.treesLocked() {
		t.each(func(e *entry) {
			for _, status := range e.statuses() {
				switch {
				case status.State == cordis.Starting, status.State == cordis.Quiescing:
					busy = true
				case status.State == cordis.Waiting && len(status.Missing) == 0:
					busy = true // about to start
				}
			}
		})
	}
	return !busy
}

// statuses returns the kernel status of what runs for the entry.
func (e *entry) statuses() []cordis.Status {
	var list []cordis.Status
	if e.wrapper != nil {
		list = append(list, e.wrapper.Status())
	}
	if e.handle != nil {
		list = append(list, e.handle.Status())
	}
	return list
}

func (t *tree) each(visit func(*entry)) {
	var walk func([]*entry)
	walk = func(list []*entry) {
		for _, e := range list {
			visit(e)
			walk(e.children)
		}
	}
	walk(t.entries)
}

// settled notes that something started or stopped. It wakes Wait and makes
// sure that reaching stability is confirmed and announced.
func (l *Loader) settled() {
	l.mu.Lock()
	l.activity++
	close(l.changed)
	l.changed = make(chan struct{})
	if !l.stableLocked() {
		l.steady = false
	}
	confirm := !l.steady && !l.confirming
	if confirm {
		l.confirming = true
	}
	l.mu.Unlock()
	if confirm {
		l.scope.Go(func(ctx context.Context) error {
			l.confirm(ctx)
			return nil
		})
	}
}

// confirm waits for the entries to settle and then lets instances that
// require Ready start. Only reaching stability is announced: announcing its
// loss would stop the instances that have just started because of it.
func (l *Loader) confirm(ctx context.Context) {
	for {
		l.mu.Lock()
		seen := l.activity
		l.mu.Unlock()
		err := l.Wait(ctx)
		l.mu.Lock()
		if err != nil {
			l.confirming = false
			l.mu.Unlock()
			return
		}
		if seen == l.activity {
			first := !l.reached
			l.confirming, l.steady, l.reached = false, true, true
			l.mu.Unlock()
			// Ready is decided by reached, which only changes once: there is
			// nothing to tell its consumers after that.
			if first {
				Service.Changed(l.scope)
			}
			return
		}
		l.mu.Unlock()
	}
}

// Wait blocks until no entry is starting or stopping. Entries that wait for
// a missing service do not hold it up. Do not call it from the Start of a
// plugin the loader runs, which would wait for itself; require Ready instead.
func (l *Loader) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		if l.driving {
			changed := l.changed
			l.mu.Unlock()
			select {
			case <-changed:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		seen := l.activity
		var waits []func(context.Context) error
		for _, t := range l.treesLocked() {
			t.each(func(e *entry) {
				if e.wrapper != nil {
					waits = append(waits, e.wrapper.Wait)
				}
				if e.handle != nil {
					waits = append(waits, e.handle.Wait)
				}
			})
		}
		l.mu.Unlock()
		for _, wait := range waits {
			wait(ctx) // a failed instance has settled
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		l.mu.Lock()
		done := seen == l.activity && l.stableLocked()
		l.mu.Unlock()
		if done {
			return nil
		}
	}
}

// Entry is a snapshot of one entry.
type Entry struct {
	ID       string
	Plugin   string // empty for a group or an included file
	Include  string // path of the included file, as written
	Group    bool   // the entry is a group or an included file
	Disabled bool
	Config   json.RawMessage            // as written; nil when omitted
	Requires []string                   // entry-level requirements
	Settings map[string]json.RawMessage // by service name
	Isolate  map[string]string          // service name to label; "" is a private space
	// State is "disabled", "unresolved" when the entry could not be started
	// (see Err), "group", or the state of its instance.
	State   string
	Err     error
	Missing []cordis.Missing
	Source  string // file the entry comes from
	Line    int    // zero for an entry not yet written
	Column  int
	// Unapplied lists the patches of an included file that could not be applied.
	Unapplied []error
	Children  []Entry
}

// Entries returns a snapshot of the tree.
func (l *Loader) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return snapshot(l.root.entries)
}

func snapshot(list []*entry) []Entry {
	out := make([]Entry, 0, len(list))
	for _, e := range list {
		if e.removed {
			continue
		}
		item := Entry{ID: e.globalID(), Plugin: e.spec.Plugin, Include: e.spec.Include, Group: e.spec.kind() != "plugin",
			Disabled: e.spec.Disabled, Source: e.tree.source.path, Children: snapshot(e.below())}
		item.Config, item.Requires, item.Settings = slices.Clone(e.spec.Config), slices.Clone(e.spec.Requires), maps.Clone(e.spec.Settings)
		for service, isolation := range e.spec.Isolate {
			if item.Isolate == nil {
				item.Isolate = map[string]string{}
			}
			item.Isolate[service] = isolation.label
		}
		if e.subtree != nil {
			for _, err := range e.subtree.unapplied {
				item.Unapplied = append(item.Unapplied, e.locate(err))
			}
		}
		if e.written {
			if position, ok := e.tree.source.parsed.Positions[e.path]; ok {
				item.Line, item.Column = position.Line, position.Column
			}
		}
		statuses := e.statuses()
		switch {
		case e.blocked():
			item.State = "disabled"
		case e.err != nil:
			item.State, item.Err = "unresolved", e.locate(e.err)
		case len(statuses) > 0:
			// With entry-level requirements the wrapper is listed first; it is
			// the one that waits.
			status := statuses[len(statuses)-1]
			if statuses[0].State != cordis.Active {
				status = statuses[0]
			}
			item.State, item.Err, item.Missing = status.State.String(), status.Err, status.Missing
		case item.Group:
			item.State = "group"
		default:
			item.State = cordis.Waiting.String()
		}
		out = append(out, item)
	}
	return out
}

// locate attaches the file position to an error of this entry.
func (e *entry) locate(err error) error {
	var located *config.Error
	if errors.As(err, &located) || !e.written {
		return err
	}
	return e.tree.source.locate(e.path, err)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
