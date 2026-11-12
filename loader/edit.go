package loader

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// ErrNotFound reports an identifier that names no entry.
var ErrNotFound = errors.New("loader: no such entry")

// entryKeys are the keys of an entry other than its identifier and children.
var entryKeys = []string{"plugin", "config", "disabled", "requires", "settings", "isolate", "include", "initial", "patches"}

// EntryOptions describes an entry to create.
type EntryOptions struct {
	ID       string // generated when empty
	Plugin   string // name of a registered plugin; leave empty for a group
	Group    bool
	Config   json.RawMessage
	Disabled bool
	Requires []string
	Settings map[string]json.RawMessage
	Isolate  map[string]string // service name to label; "" isolates privately
}

// EntryPatch changes the fields that are set. To clear a field, point at
// its zero value.
type EntryPatch struct {
	Plugin   *string
	Config   *json.RawMessage // replaces the whole configuration
	Disabled *bool
	Requires *[]string
	Settings *map[string]json.RawMessage
	Isolate  *map[string]string
}

// Dropped reports an edit that had not been written yet and was discarded
// because the file changed the same thing; the file wins.
type Dropped struct {
	ID   string
	Kind ChangeKind
	Key  string // the key of a dropped update
}

// Error names the dropped change, its key if any, and the entry.
func (d Dropped) Error() string {
	what := string(d.Kind)
	if d.Key != "" {
		what += " of " + strconv.Quote(d.Key)
	}
	return fmt.Sprintf("loader: unwritten %s on entry %q dropped: the file changed it", what, d.ID)
}

func isolations(m map[string]string) map[string]isolation {
	if m == nil {
		return nil
	}
	out := make(map[string]isolation, len(m))
	for service, label := range m {
		out[service] = isolation{label: label}
	}
	return out
}

// lock serializes edits, reloads and writes.
func (l *Loader) lock(ctx context.Context) error {
	select {
	case l.ops <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Loader) unlock() { <-l.ops }

// find resolves a global identifier.
func (l *Loader) find(id string) (*entry, error) {
	t := l.root
	for rest := id; ; {
		head, tail, nested := strings.Cut(rest, ":")
		e := t.byID[head]
		if e == nil || e.removed {
			return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
		}
		if !nested {
			return e, nil
		}
		if e.subtree == nil {
			return nil, fmt.Errorf("%w: %q (the included file of %q is not loaded)", ErrNotFound, id, head)
		}
		t, rest = e.subtree, tail
	}
}

// group resolves the group that new children of id go into. The empty
// identifier is the top level of the root file.
func (l *Loader) group(id string) (*tree, *entry, error) {
	if id == "" {
		return l.root, nil, nil
	}
	e, err := l.find(id)
	if err != nil {
		return nil, nil, err
	}
	switch e.spec.kind() {
	case "include":
		if e.subtree == nil {
			return nil, nil, fmt.Errorf("%w: the included file of %q is not loaded", ErrNotFound, id)
		}
		return e.subtree, nil, nil
	case "group":
		return e.tree, e, nil
	}
	return nil, nil, fmt.Errorf("loader: entry %q is not a group", id)
}

// commit applies change under the lock, writes the affected files and
// announces what changed. The edit returns once it is applied and written;
// it does not wait for instances to settle. When a write fails the edit
// stays applied and is written by a later edit, reload or stop.
func (l *Loader) commit(ctx context.Context, change func() ([]Change, error)) error {
	if err := l.lock(ctx); err != nil {
		return err
	}
	l.mu.Lock()
	changes, err := change()
	var pending []*tree
	if err == nil && len(changes) > 0 {
		l.kickLocked()
		for _, t := range l.treesLocked() {
			t.applyPatches()
			if t.pending() {
				pending = append(pending, t)
			}
		}
	}
	l.mu.Unlock()
	var errs []error
	for _, t := range pending {
		more, err := t.flush(ctx)
		changes = append(changes, more...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	l.unlock()
	if err != nil {
		return err
	}
	l.announce(ctx, changes)
	return errors.Join(errs...)
}

func (l *Loader) announce(ctx context.Context, changes []Change) {
	for _, change := range changes {
		if err := Changed.Emit(context.WithoutCancel(ctx), l.scope, change); err != nil && l.config.Logger != nil {
			l.config.Logger.Warn("change listener failed", "error", err)
		}
	}
}

// Create adds an entry at the end of the group parent ("" for the top level)
// and returns its identifier.
func (l *Loader) Create(ctx context.Context, parent string, options EntryOptions) (string, error) {
	var id string
	err := l.commit(ctx, func() ([]Change, error) {
		t, group, err := l.group(parent)
		if err != nil {
			return nil, err
		}
		if group != nil && group.origin != nil {
			return nil, fmt.Errorf("%w: create in %q", ErrPatched, parent)
		}
		if !t.source.writable {
			return nil, ErrReadOnly
		}
		s := spec{ID: options.ID, Plugin: options.Plugin, Config: options.Config, Disabled: options.Disabled,
			Requires: options.Requires, Settings: options.Settings, Isolate: isolations(options.Isolate)}
		if options.Group {
			s.Plugins = []spec{}
		}
		if s.ID == "" {
			var random [4]byte
			rand.Read(random[:])
			s.ID = "entry-" + hex.EncodeToString(random[:])
		}
		// The identifier of a removed entry is free again once the file no
		// longer has it; until then a reload must be able to tell the two
		// apart.
		if existing := t.byID[s.ID]; existing != nil && !existing.gone {
			return nil, fmt.Errorf("loader: entry id %q is already used", s.ID)
		}
		if err := s.validate(map[string]bool{}); err != nil {
			return nil, fmt.Errorf("loader: %w", err)
		}
		if err := writable(s); err != nil {
			return nil, err
		}
		e := t.newEntry(group, s, false)
		// What patches insert comes after what the file has.
		list := e.siblings()
		at := len(*list)
		for at > 0 && (*list)[at-1].origin != nil {
			at--
		}
		*list = slices.Insert(*list, at, e)
		id = e.globalID()
		return []Change{{ID: id, Kind: Created}}, nil
	})
	return id, err
}

// Update applies patch to an entry. A changed configuration is handed to the
// running instance, which may apply it in place; a changed plugin, entry
// requirement, setting or isolation reloads the entry and what is below it.
//
// A key that a patch of the including file provides is written back to that
// patch; every other key is written to the file the entry comes from.
func (l *Loader) Update(ctx context.Context, id string, patch EntryPatch) error {
	return l.commit(ctx, func() ([]Change, error) {
		e, err := l.find(id)
		if err != nil {
			return nil, err
		}
		fields := map[string]json.RawMessage{}
		set := func(key string, value any) {
			data, _ := json.Marshal(value)
			fields[key] = data
		}
		if patch.Plugin != nil {
			set("plugin", *patch.Plugin)
		}
		if patch.Config != nil {
			fields["config"] = *patch.Config
		}
		if patch.Disabled != nil {
			set("disabled", *patch.Disabled)
		}
		if patch.Requires != nil {
			set("requires", *patch.Requires)
		}
		if patch.Settings != nil {
			set("settings", *patch.Settings)
		}
		if patch.Isolate != nil {
			set("isolate", isolations(*patch.Isolate))
		}
		next := e.spec
		for key, value := range fields {
			if next, err = next.with(key, value); err != nil {
				return nil, fmt.Errorf("loader: %w", under("/"+key, err))
			}
		}
		check := next
		if e.spec.kind() == "group" {
			check.Plugins = []spec{}
		}
		if err := check.validate(map[string]bool{}); err != nil {
			return nil, fmt.Errorf("loader: %w", err)
		}
		if err := writable(next); err != nil {
			return nil, err
		}
		// Decide where each changed key is written before anything takes
		// effect: every file involved must be writable.
		own := false
		var patches []patchEdit
		for _, key := range sortedKeys(fields) {
			value := next.key(key)
			if bytes.Equal(value, e.spec.key(key)) {
				continue
			}
			index, patched := e.patched[key]
			switch {
			case patched: // also when a later patch overrides an inserted entry
				patches = append(patches, patchEdit{origin: origin{patch: index}, key: key, value: value})
			case e.origin != nil:
				patches = append(patches, patchEdit{origin: *e.origin, key: key, value: value})
			default:
				own = true
			}
		}
		if !own && len(patches) == 0 {
			return nil, nil
		}
		if own && !e.tree.source.writable || len(patches) > 0 && !e.tree.owner.tree.source.writable {
			return nil, ErrReadOnly
		}
		if len(patches) > 0 {
			owner := e.tree.owner
			updated, err := setPatches(owner.spec.Patches, patches)
			if err != nil {
				return nil, err
			}
			owner.spec.Patches, e.tree.patches = updated, updated
		}
		e.apply(next)
		return []Change{{ID: id, Kind: Updated}}, nil
	})
}

// apply replaces the entry's fields and schedules what the difference needs:
// a configuration update for a changed config, a reload for anything else
// that affects how the entry runs.
func (e *entry) apply(next spec) {
	previous := e.spec
	e.spec = next.own()
	if !bytes.Equal(previous.key("config"), next.key("config")) {
		e.revision++
	}
	previous.Config, next.Config = nil, nil
	previous.Disabled, next.Disabled = false, false
	if !previous.own().equal(next.own()) {
		e.generation++
	}
}

// Remove deletes an entry and everything below it.
func (l *Loader) Remove(ctx context.Context, id string) error {
	return l.commit(ctx, func() ([]Change, error) {
		e, err := l.find(id)
		if err != nil {
			return nil, err
		}
		if e.origin != nil {
			return nil, fmt.Errorf("%w: remove %q", ErrPatched, id)
		}
		if !e.tree.source.writable {
			return nil, ErrReadOnly
		}
		e.discard(false)
		return []Change{{ID: id, Kind: Removed}}, nil
	})
}

// discard marks the entry and everything below it as removed. gone tells
// that the file does not have them either.
func (e *entry) discard(gone bool) {
	e.removed = true
	e.gone = gone || !e.written
	for _, child := range e.children {
		child.discard(gone)
	}
}

// Move puts an entry at index among the children of the group parent ("" for
// the top level) of the same file, counting the entries that Entries lists
// there. Moving within its group does not restart it; moving to another
// group reloads it there.
func (l *Loader) Move(ctx context.Context, id, parent string, index int) error {
	return l.commit(ctx, func() ([]Change, error) {
		e, err := l.find(id)
		if err != nil {
			return nil, err
		}
		t, group, err := l.group(parent)
		if err != nil {
			return nil, err
		}
		if t != e.tree {
			return nil, errors.New("loader: an entry cannot move to another file")
		}
		if e.origin != nil || group != nil && group.origin != nil {
			return nil, fmt.Errorf("%w: move %q", ErrPatched, id)
		}
		if !t.source.writable {
			return nil, ErrReadOnly
		}
		for ancestor := group; ancestor != nil; ancestor = ancestor.parent {
			if ancestor == e {
				return nil, errors.New("loader: an entry cannot move into itself")
			}
		}
		if !e.place(group, index) {
			return nil, nil
		}
		return []Change{{ID: id, Kind: Moved}}, nil
	})
}

// place moves the entry to index among the visible children of group and
// reports whether that changed anything.
func (e *entry) place(group *entry, index int) bool {
	visible := func(list []*entry) []*entry {
		return slices.DeleteFunc(slices.Clone(list), func(other *entry) bool { return other.removed })
	}
	target := &e.tree.entries
	if group != nil {
		target = &group.children
	}
	rest := slices.DeleteFunc(visible(*target), func(other *entry) bool { return other == e })
	index = min(max(index, 0), len(rest))
	if e.parent == group && slices.Index(visible(*target), e) == index {
		return false
	}
	list := e.siblings()
	*list = slices.DeleteFunc(*list, func(other *entry) bool { return other == e })
	if e.parent != group {
		e.generation++
	}
	e.parent = group
	at := len(*target)
	if index < len(rest) {
		at = slices.Index(*target, rest[index])
	}
	*target = slices.Insert(*target, at, e)
	e.moved = true
	return true
}

// Reload re-reads the configuration files and applies what changed. Entries
// that did not change keep running. A file that cannot be parsed leaves the
// tree as it is and is never overwritten. Edits that could not be written
// yet are kept unless the file changed the same thing; those are reported as
// Dropped errors, and the file wins. Included files that could not be loaded
// are tried again.
func (l *Loader) Reload(ctx context.Context) error {
	if err := l.lock(ctx); err != nil {
		return err
	}
	l.mu.Lock()
	trees := l.treesLocked()
	for _, t := range trees {
		t.each(func(e *entry) {
			if e.spec.kind() == "include" && !e.running && e.err != nil {
				e.generation++
				l.kickLocked()
			}
		})
	}
	l.mu.Unlock()
	var changes []Change
	var errs []error
	for _, t := range trees {
		more, err := t.flush(ctx)
		changes = append(changes, more...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	l.unlock()
	l.announce(ctx, changes)
	return errors.Join(errs...)
}

// treesLocked lists the root tree and every loaded included tree.
func (l *Loader) treesLocked() []*tree {
	var list []*tree
	var collect func(*tree)
	collect = func(t *tree) {
		list = append(list, t)
		t.each(func(e *entry) {
			if e.subtree != nil {
				collect(e.subtree)
			}
		})
	}
	collect(l.root)
	return list
}

// flushAll writes what is still unwritten when the loader stops.
func (l *Loader) flushAll(ctx context.Context) error {
	ctx = context.WithoutCancel(ctx)
	l.lock(ctx)
	defer l.unlock()
	l.mu.Lock()
	trees := l.treesLocked()
	l.mu.Unlock()
	var errs []error
	for _, t := range trees {
		if _, err := t.flush(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// watch polls the files and reloads when one of them changed.
func (l *Loader) watch(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		l.mu.Lock()
		read := map[string][]byte{}
		for _, t := range l.treesLocked() {
			read[t.source.path] = t.source.data
		}
		l.mu.Unlock()
		stale := false
		for path, last := range read {
			data, err := os.ReadFile(path)
			if err == nil && !bytes.Equal(data, last) {
				stale = true
			}
		}
		if !stale {
			continue
		}
		if err := l.Reload(ctx); err != nil && l.config.Logger != nil {
			l.config.Logger.Warn("reload failed", "error", err)
		}
	}
}

// pending reports whether the tree holds edits that its file does not have.
func (t *tree) pending() bool {
	if len(t.tombs) > 0 {
		return true
	}
	pending := false
	t.each(func(e *entry) {
		switch {
		case pending || e.origin != nil:
		case e.removed:
			pending = !e.gone
		case !e.written || e.moved:
			pending = true
		default:
			for _, key := range entryKeys {
				if _, patched := e.patched[key]; !patched && !bytes.Equal(e.spec.key(key), e.read.key(key)) {
					pending = true
				}
			}
		}
	})
	return pending
}

// flush brings the tree and its file in line: it merges what changed in the
// file since it was last read, then writes the edits the file does not have.
// The caller holds the operation lock.
func (t *tree) flush(ctx context.Context) ([]Change, error) {
	l := t.loader
	l.mu.Lock()
	path, last := t.source.path, t.source.data
	l.mu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var changes []Change
	var dropped []error
	if !bytes.Equal(data, last) {
		next, err := parse(path, data)
		if err != nil {
			return nil, err // the running tree stays; the file is not touched
		}
		l.mu.Lock()
		changes, dropped = t.merge(next)
		t.applyPatches()
		l.kickLocked()
		l.mu.Unlock()
	}
	l.mu.Lock()
	var out []byte
	var next *source
	pending := t.pending()
	if pending {
		out, next, err = t.render()
	}
	write := l.write
	l.mu.Unlock()
	if !pending {
		return changes, errors.Join(dropped...)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = write(path, out)
	}
	if err != nil {
		return changes, errors.Join(append(dropped, err)...)
	}
	l.mu.Lock()
	t.source = next
	t.settle()
	l.mu.Unlock()
	return changes, errors.Join(dropped...)
}

// settle records that the file now holds exactly what the tree holds.
func (t *tree) settle() {
	for _, e := range t.tombs {
		e.gone = true
		t.release(e)
	}
	t.tombs = nil
	var walk func(parent *entry, list []*entry, path string)
	walk = func(parent *entry, list []*entry, path string) {
		index := 0
		for _, e := range list {
			if e.removed {
				e.discard(true)
				e.each(t.release)
				continue
			}
			if e.origin != nil {
				continue
			}
			e.path = path + "/plugins/" + strconv.Itoa(index)
			e.read = e.unpatched()
			e.readParent, e.written, e.moved = parent, true, false
			index++
			walk(e, e.children, e.path)
		}
	}
	walk(nil, t.entries, "")
}

// release frees the identifier of an entry the file no longer has.
func (t *tree) release(e *entry) {
	if t.byID[e.id] == e {
		delete(t.byID, e.id)
	}
}

// each visits the entry and everything below it in its file.
func (e *entry) each(visit func(*entry)) {
	visit(e)
	for _, child := range e.children {
		child.each(visit)
	}
}

// unpatched returns the entry's fields as its own file has them: the keys a
// patch provides keep the value last read from the file.
func (e *entry) unpatched() spec {
	s := e.spec
	for key := range e.patched {
		own := e.shadow[key]
		if e.written {
			own = e.read.key(key)
		}
		s, _ = s.with(key, own)
	}
	return s
}

// writeFile replaces path atomically.
func writeFile(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, err = temp.Write(data)
	if err == nil {
		err = temp.Chmod(mode)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// merge applies a newly read file to the tree. For every key it compares
// three versions: what was last read, the new file, and the tree with its
// unwritten edits. A key changed by one side takes that side; changed by
// both, the file wins and the edit is reported as dropped. The same holds
// for creating, removing and moving entries.
func (t *tree) merge(next *source) (changes []Change, dropped []error) {
	drop := func(e *entry, kind ChangeKind, key string) {
		dropped = append(dropped, Dropped{ID: e.globalID(), Kind: kind, Key: key})
	}
	edited := func(e *entry) (keys []string) {
		for _, key := range entryKeys {
			if _, patched := e.patched[key]; !patched && !bytes.Equal(e.spec.key(key), e.read.key(key)) {
				keys = append(keys, key)
			}
		}
		return keys
	}

	// Where every entry is before the merge.
	type position struct {
		parent *entry
		index  int
	}
	before := map[*entry]position{}
	var known []*entry
	var record func(parent *entry, list []*entry)
	record = func(parent *entry, list []*entry) {
		for index, e := range list {
			before[e] = position{parent, index}
			known = append(known, e)
			record(e, e.children)
		}
	}
	record(nil, t.entries)
	known = append(known, t.tombs...)

	matched := map[*entry]bool{}
	touched := map[*entry]bool{}   // entries the file created or changed
	order := map[*entry][]*entry{} // entries in the order of the file, by their group in the file
	var walk func(parent *entry, theirs []spec, path string)
	walk = func(parent *entry, theirs []spec, path string) {
		for index, s := range theirs {
			own := s.own()
			var e *entry
			if s.ID != "" {
				e = t.byID[s.ID]
			} else {
				// An anonymous entry is the one that was last read with the
				// same content under the same parent.
				for _, candidate := range known {
					if candidate.anonymous && candidate.written && !matched[candidate] &&
						candidate.readParent == parent && candidate.read.equal(own) {
						e = candidate
						break
					}
				}
			}
			if e != nil && !e.written {
				// The file now defines an identifier that an unwritten entry
				// also uses; the file's entry replaces it.
				if e.origin == nil && !e.removed {
					drop(e, Created, "")
				}
				e.removed, e.gone = true, true
				e = nil
			}
			if e == nil {
				e = t.newEntry(parent, s, true)
				known = append(known, e)
				matched[e], touched[e] = true, true
				changes = append(changes, Change{ID: e.globalID(), Kind: Created})
			} else {
				matched[e] = true
				changed := false
				merged := e.spec
				for _, key := range entryKeys {
					base, their, our := e.read.key(key), own.key(key), e.spec.key(key)
					if _, patched := e.patched[key]; patched || bytes.Equal(their, base) {
						continue // the file left it alone; an unwritten edit survives
					}
					touched[e] = true
					if bytes.Equal(their, our) {
						continue
					}
					if !bytes.Equal(our, base) {
						drop(e, Updated, key)
					}
					merged, _ = merged.with(key, their)
					changed = true
				}
				if e.removed && e.gone {
					// The file has again what it had removed.
					e.removed, e.gone = false, false
					delete(before, e)
					changed = true
				}
				if e.readParent != parent {
					touched[e] = true
					if e.moved {
						drop(e, Moved, "")
					}
					if e.parent != parent {
						e.parent = parent
						e.generation++
						changed = true
					}
					e.moved = false
				}
				if changed {
					e.apply(merged)
					changes = append(changes, Change{ID: e.globalID(), Kind: Updated})
				}
				e.read = own
			}
			e.readParent, e.path = parent, path+"/plugins/"+strconv.Itoa(index)
			order[parent] = append(order[parent], e)
			walk(e, s.Plugins, e.path)
		}
	}
	walk(nil, next.document.Plugins, "")

	// An unwritten removal is dropped when the file created or changed
	// anything it would delete: the whole removed subtree comes back.
	for _, e := range known {
		if !touched[e] {
			continue
		}
		var root *entry
		for ancestor := e; ancestor != nil; ancestor = ancestor.parent {
			if ancestor.removed && !ancestor.gone {
				root = ancestor
			}
		}
		if root == nil {
			continue
		}
		drop(root, Removed, "")
		changes = append(changes, Change{ID: root.globalID(), Kind: Updated})
		// Entries that already left the tree are no longer among the
		// children of their group, so go by the parent links.
		for _, back := range known {
			for ancestor := back; ancestor != nil && back.removed && !back.gone; ancestor = ancestor.parent {
				if ancestor == root {
					back.removed = false
				}
			}
		}
	}

	// What the file no longer has is removed, and so is what was created
	// below it.
	for _, e := range known {
		if matched[e] || !e.written || e.gone {
			continue
		}
		if !e.removed {
			changes = append(changes, Change{ID: e.globalID(), Kind: Removed})
			for _, key := range edited(e) {
				drop(e, Updated, key)
			}
			if e.moved {
				drop(e, Moved, "")
			}
		}
		e.removed, e.gone = true, true
	}
	// Whatever is left below something removed, or below what is no longer
	// a group, cannot stay there. Unwritten entries go; entries the file has
	// return to where the file has them, known being in top-down order.
	for _, e := range known {
		if e.removed || e.parent == nil || !e.parent.removed && e.parent.spec.kind() == "group" {
			continue
		}
		if e.written {
			if e.moved {
				drop(e, Moved, "")
			}
			e.parent, e.moved = e.readParent, false
			e.generation++
			delete(before, e)
			continue
		}
		if e.origin == nil {
			drop(e, Created, "")
		}
		e.removed, e.gone = true, true
	}
	t.tombs = slices.DeleteFunc(t.tombs, func(e *entry) bool {
		if e.gone {
			t.release(e)
		}
		return e.gone || !e.removed
	})

	// The file decides the order. Entries it does not place keep their
	// position: unwritten ones, moved ones, and removed ones that still have
	// to stop.
	placed := map[*entry][]*entry{}
	for _, group := range append([]*entry{nil}, known...) {
		for _, e := range order[group] {
			_, listed := before[e]
			switch {
			case listed && (e.removed || e.moved): // keeps its position
			case e.removed: // left the tree already
			default:
				placed[e.parent] = append(placed[e.parent], e)
				delete(before, e)
			}
		}
	}
	var inserted []*entry
	for _, e := range known {
		at, stays := before[e]
		switch {
		case !stays:
		case e.origin != nil:
			inserted = append(inserted, e) // what patches insert comes last
		default:
			list := placed[at.parent]
			placed[at.parent] = slices.Insert(list, min(at.index, len(list)), e)
		}
	}
	for _, e := range inserted {
		placed[e.parent] = append(placed[e.parent], e)
	}
	t.entries = placed[nil]
	for _, e := range known {
		e.children = placed[e]
	}
	t.source = next
	return changes, dropped
}

// document returns what the file has to contain for the tree as it is.
func (t *tree) document() document {
	var specs func(list []*entry) []spec
	specs = func(list []*entry) []spec {
		out := []spec{}
		for _, e := range list {
			if e.removed || e.origin != nil {
				continue
			}
			s := e.unpatched()
			if e.anonymous {
				s.ID = ""
			}
			if s.kind() == "group" {
				s.Plugins = specs(e.children)
			}
			out = append(out, s)
		}
		return out
	}
	return document{Plugins: specs(t.entries)}
}

// render brings a fresh copy of the file's YAML in line with the tree and
// encodes it. Only what differs from the file is touched, so everything else
// keeps its comments, order and quoting. The result is parsed back and
// compared with the tree before it is returned.
func (t *tree) render() ([]byte, *source, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(t.source.data, &root); err != nil {
		return nil, nil, err
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, nil, errors.New("loader: configuration file is not a mapping")
	}
	byPath := map[string]*yaml.Node{}
	var index func(mapping *yaml.Node, path string)
	index = func(mapping *yaml.Node, path string) {
		list := value(mapping, "plugins")
		if list == nil {
			return
		}
		for i, item := range list.Content {
			itemPath := path + "/plugins/" + strconv.Itoa(i)
			byPath[itemPath] = item
			index(item, itemPath)
		}
	}
	index(root.Content[0], "")

	var build func(mapping *yaml.Node, list []*entry, required bool) error
	build = func(mapping *yaml.Node, list []*entry, required bool) error {
		var content []*yaml.Node
		for _, e := range list {
			if e.removed || e.origin != nil {
				continue
			}
			var node *yaml.Node
			if e.written {
				if node = byPath[e.path]; node == nil {
					return fmt.Errorf("loader: entry %q is missing from the file", e.globalID())
				}
				for _, key := range entryKeys {
					now, was := e.spec.key(key), e.read.key(key)
					if _, patched := e.patched[key]; patched || bytes.Equal(now, was) {
						continue
					}
					if now == nil {
						deleteKey(node, key)
						continue
					}
					item, err := encodeJSON(now)
					if err != nil {
						return err
					}
					setKey(node, key, item)
				}
			} else {
				var err error
				if node, err = encode(e.unpatched()); err != nil {
					return err
				}
			}
			if e.spec.kind() == "group" {
				if err := build(node, e.children, true); err != nil {
					return err
				}
			}
			content = append(content, node)
		}
		list2 := value(mapping, "plugins")
		if list2 == nil || list2.Kind != yaml.SequenceNode { // absent, or "plugins:" without a value
			if list2 == nil && len(content) == 0 && !required {
				return nil
			}
			list2 = &yaml.Node{Kind: yaml.SequenceNode}
			deleteKey(mapping, "plugins")
			setKey(mapping, "plugins", list2)
		}
		if !slices.Equal(list2.Content, content) {
			list2.Content = content
		}
		return nil
	}
	if err := build(root.Content[0], t.entries, true); err != nil {
		return nil, nil, err
	}
	out, err := encodeYAML(&root)
	if err != nil {
		return nil, nil, err
	}
	next, err := parse(t.source.path, out)
	if err != nil {
		return nil, nil, fmt.Errorf("loader: the file to write does not parse: %w", err)
	}
	want, _ := json.Marshal(t.document())
	got, _ := json.Marshal(next.document)
	if !sameData(want, got) {
		return nil, nil, fmt.Errorf("loader: %s cannot hold the entries as they are; nothing was written", t.source.path)
	}
	return out, next, nil
}

func encodeYAML(node *yaml.Node) ([]byte, error) {
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(node); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// keyRank orders the keys of a new entry: id, plugin, the rest by name, and
// config and plugins last.
func keyRank(key string) string {
	switch key {
	case "id":
		return "0"
	case "plugin", "include":
		return "1"
	case "config":
		return "8"
	case "plugins":
		return "9"
	}
	return "5" + key
}

func value(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// graft returns the node to write where old was: next, with the parts of
// old that did not change kept as they are, so that their comments, order
// and quoting survive.
func graft(old, next *yaml.Node) *yaml.Node {
	switch {
	case old.Kind == yaml.MappingNode && next.Kind == yaml.MappingNode:
		kept := old.Content[:0:0]
		for i := 0; i+1 < len(old.Content); i += 2 {
			if item := value(next, old.Content[i].Value); item != nil {
				kept = append(kept, old.Content[i], graft(old.Content[i+1], item))
			}
		}
		for i := 0; i+1 < len(next.Content); i += 2 {
			if value(old, next.Content[i].Value) == nil {
				kept = append(kept, next.Content[i], next.Content[i+1])
			}
		}
		old.Content = kept
		return old
	case old.Kind == yaml.SequenceNode && next.Kind == yaml.SequenceNode:
		for i := range min(len(old.Content), len(next.Content)) {
			next.Content[i] = graft(old.Content[i], next.Content[i])
		}
		old.Content = next.Content
		return old
	case old.Kind == yaml.ScalarNode && next.Kind == yaml.ScalarNode && old.Value == next.Value && old.ShortTag() == next.ShortTag():
		return old
	}
	// A comment on the line of a scalar has no place on a collection.
	next.HeadComment = old.HeadComment
	if next.Kind == yaml.ScalarNode {
		next.LineComment, next.FootComment = old.LineComment, old.FootComment
	}
	return next
}

func setKey(mapping *yaml.Node, key string, item *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = graft(mapping.Content[i+1], item)
			return
		}
	}
	at := len(mapping.Content)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if keyRank(mapping.Content[i].Value) > keyRank(key) {
			at = i
			break
		}
	}
	name := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	mapping.Content = slices.Insert(mapping.Content, at, name, item)
}

func deleteKey(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = slices.Delete(mapping.Content, i, i+2)
			return
		}
	}
}

// encode turns an entry into a YAML mapping with its keys in canonical order.
func encode(s spec) (*yaml.Node, error) {
	data, err := json.Marshal(s.own())
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	keys := sortedKeys(fields)
	slices.SortFunc(keys, func(a, b string) int { return strings.Compare(keyRank(a), keyRank(b)) })
	mapping := &yaml.Node{Kind: yaml.MappingNode}
	for _, key := range keys {
		item, err := encodeJSON(fields[key])
		if err != nil {
			return nil, err
		}
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, item)
	}
	return mapping, nil
}

// encodeJSON converts JSON to a YAML node in block style, keeping the order
// of object keys and the exact text of numbers.
func encodeJSON(data json.RawMessage) (*yaml.Node, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decodeNode(decoder)
}

func decodeNode(decoder *json.Decoder) (*yaml.Node, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	scalar := func(tag, text string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: text}
	}
	switch value := token.(type) {
	case json.Delim:
		if value == '[' {
			node := &yaml.Node{Kind: yaml.SequenceNode}
			for decoder.More() {
				item, err := decodeNode(decoder)
				if err != nil {
					return nil, err
				}
				node.Content = append(node.Content, item)
			}
			_, err := decoder.Token()
			return node, err
		}
		node := &yaml.Node{Kind: yaml.MappingNode}
		for decoder.More() {
			key, err := decodeNode(decoder)
			if err != nil {
				return nil, err
			}
			item, err := decodeNode(decoder)
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content, key, item)
		}
		_, err := decoder.Token()
		return node, err
	case string:
		node := scalar("!!str", value)
		if !plainString(value) {
			node.Style = yaml.DoubleQuotedStyle
		}
		return node, nil
	case json.Number:
		if strings.ContainsAny(value.String(), ".eE") {
			return scalar("!!float", value.String()), nil
		}
		return scalar("!!int", value.String()), nil
	case bool:
		return scalar("!!bool", strconv.FormatBool(value)), nil
	}
	return scalar("!!null", "null"), nil
}

// writable checks that the values of an entry survive being written to a
// YAML file and read back, so that an edit YAML cannot carry is refused
// before it takes effect.
func writable(s spec) error {
	fail := func(err error) error {
		return fmt.Errorf("loader: entry cannot be written to a YAML file: %w", err)
	}
	s = s.own()
	node, err := encode(s)
	if err != nil {
		return fail(err)
	}
	if s.kind() == "group" {
		s.Plugins = []spec{}
		setKey(node, "plugins", &yaml.Node{Kind: yaml.SequenceNode})
	}
	name := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "plugins"}
	list := &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{node}}
	out, err := encodeYAML(&yaml.Node{Kind: yaml.DocumentNode,
		Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{name, list}}}})
	if err != nil {
		return fail(err)
	}
	back, err := parse("entry.yaml", out)
	if err != nil {
		return fail(err)
	}
	want, _ := json.Marshal(s)
	got, _ := json.Marshal(back.document.Plugins[0])
	if !sameData(want, got) {
		return fail(errors.New("a value does not read back as it was written"))
	}
	return nil
}

// plainString reports whether every YAML reader takes text as a string when
// it is written without quotes. Anything else is quoted: parsers disagree on
// which bare words are numbers, booleans or null.
func plainString(text string) bool {
	switch strings.ToLower(text) {
	case "y", "n", "yes", "no", "on", "off", "true", "false", "null", "nan", "inf":
		return false
	}
	return plainWord.MatchString(text)
}

var plainWord = regexp.MustCompile(`^[A-Za-z]([A-Za-z0-9_./-]*[A-Za-z0-9_])?$`)
