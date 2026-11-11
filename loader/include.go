package loader

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/config"
	"github.com/Cyenoch/cordis/contract"
)

// ErrPatched reports an edit that would change the structure of entries a
// patch inserts. Their keys can be updated; adding, removing or moving them
// is done in the "patches" of the including file.
var ErrPatched = errors.New("loader: entry is provided by a patch of the including file")

// patchKeys are the keys of an entry that a patch can replace, with the value
// written when the key is cleared.
var patchKeys = map[string]string{
	"config": "{}", "disabled": "false", "requires": "[]", "settings": "{}", "isolate": "{}",
}

// patch is one element of "patches". Without ID it appends Insert to the top
// level of the included file. With ID it appends Insert to that group and
// replaces Keys of that entry; Plugin, when set, must match the entry.
type patch struct {
	ID     string
	Plugin string
	Insert []spec
	Keys   map[string]json.RawMessage
}

func (p patch) MarshalJSON() ([]byte, error) {
	fields := map[string]any{}
	for key, value := range p.Keys {
		fields[key] = value
	}
	if p.ID != "" {
		fields["id"] = p.ID
	}
	if p.Plugin != "" {
		fields["plugin"] = p.Plugin
	}
	if p.Insert != nil {
		fields["insert"] = p.Insert
	}
	return json.Marshal(fields)
}

func (p *patch) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*p = patch{}
	for key, raw := range fields {
		var err error
		switch key {
		case "id":
			err = contract.Decode(raw, &p.ID)
		case "plugin":
			err = contract.Decode(raw, &p.Plugin)
		case "insert":
			err = contract.Decode(raw, &p.Insert)
		default:
			if _, ok := patchKeys[key]; !ok {
				err = errors.New("a patch cannot set this key")
			}
			if p.Keys == nil {
				p.Keys = map[string]json.RawMessage{}
			}
			p.Keys[key] = raw
		}
		if err != nil {
			return &contract.PathError{Path: "/" + key, Err: err}
		}
	}
	if p.ID == "" && (p.Insert == nil || p.Plugin != "" || len(p.Keys) > 0) {
		return errors.New(`a patch without "id" can only "insert"`)
	}
	return nil
}

// origin locates an entry that a patch inserts: the patch, then the indexes
// leading from its "insert" list down through "plugins" lists. An empty path
// is the patch itself.
type origin struct {
	patch int
	path  []int
}

func (o origin) child(index int) *origin {
	return &origin{patch: o.patch, path: append(append([]int(nil), o.path...), index)}
}

// patchEdit is a change of one key that a patch provides.
type patchEdit struct {
	origin origin
	key    string
	value  json.RawMessage // nil clears the key
}

// setPatches returns a copy of patches with the edits applied.
func setPatches(patches []patch, edits []patchEdit) ([]patch, error) {
	data, err := json.Marshal(patches)
	if err != nil {
		return nil, err
	}
	var out []patch
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	for _, pending := range edits {
		o, value := pending.origin, pending.value
		if len(o.path) == 0 {
			if value == nil {
				// The patch keeps the key, or the included file's value
				// would come back.
				value = json.RawMessage(patchKeys[pending.key])
			}
			out[o.patch].Keys[pending.key] = value
			continue
		}
		target := &out[o.patch].Insert[o.path[0]]
		for _, index := range o.path[1:] {
			target = &target.Plugins[index]
		}
		if *target, err = target.with(pending.key, value); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// include reads the file an entry refers to and builds its tree.
// files are the paths of the file the entry is in and of the files that
// include it, innermost first.
func (e *entry) include(scope *cordis.Scope, s spec, files []string, result *entry) error {
	l := e.tree.loader
	path := s.Include
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(files[0]), path)
	}
	if slices.Contains(files, path) {
		return under("/include", fmt.Errorf("%s includes itself", path))
	}
	src, err := read(path)
	if errors.Is(err, os.ErrNotExist) && s.Initial != nil {
		// Only a missing file is created; a file that exists but cannot be
		// read or parsed is left alone.
		var data []byte
		if data, err = initialContent(path, *s.Initial); err == nil {
			err = l.write(path, data)
		}
		if err == nil {
			src, err = read(path)
		}
	}
	if err != nil {
		var located *config.Error
		if errors.As(err, &located) {
			return err
		}
		return under("/include", err)
	}
	result.scope, result.release = scope.Sub()
	result.subtree = l.newTree(src, e.globalID()+":", result.scope)
	result.subtree.owner, result.subtree.patches = e, s.Patches
	result.subtree.adopt(nil, src.document.Plugins, "")
	return nil
}

// initialContent encodes the initial document in the format of path.
func initialContent(path string, initial document) ([]byte, error) {
	if initial.Plugins == nil {
		initial.Plugins = []spec{}
	}
	data, err := json.Marshal(initial)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		var out bytes.Buffer
		err := json.Indent(&out, data, "", "  ")
		return append(out.Bytes(), '\n'), err
	case ".yaml", ".yml":
		node, err := encodeJSON(data)
		if err != nil {
			return nil, err
		}
		return encodeYAML(node)
	}
	return nil, fmt.Errorf("initial content needs a YAML or JSON file, not %s", filepath.Ext(path))
}

// applyPatches brings the tree in line with the owner's patches. It starts
// from what the entries are without patches, so it can be repeated after
// every change of the tree: a patch that no longer applies lets go of its
// keys, and one whose target appeared takes effect. Patches that cannot be
// applied are recorded and skipped.
func (t *tree) applyPatches() {
	t.unapplied = nil
	desired := map[*entry]spec{}
	owned := map[*entry]map[string]int{}
	t.each(func(e *entry) {
		if len(e.patched) > 0 {
			desired[e], owned[e] = e.unpatched(), map[string]int{}
		}
	})
	for index, p := range t.patches {
		fail := func(err error) {
			t.unapplied = append(t.unapplied, under("/patches/"+strconv.Itoa(index), err))
		}
		var target *entry
		if p.ID != "" {
			target = t.byID[p.ID]
			if target == nil || target.removed {
				fail(fmt.Errorf("the included file has no entry %q", p.ID))
				continue
			}
			if p.Plugin != "" && target.spec.Plugin != p.Plugin {
				fail(fmt.Errorf("entry %q runs plugin %q, not %q", p.ID, target.spec.Plugin, p.Plugin))
				continue
			}
		}
		if p.Insert != nil {
			// Insert once, and again when what was inserted went away with
			// its group.
			roots := t.inserted[index]
			if len(roots) == 0 || slices.ContainsFunc(roots, func(e *entry) bool { return e.removed }) {
				roots, err := t.insert(target, p.Insert, origin{patch: index})
				if err != nil {
					fail(err)
					continue
				}
				t.inserted[index] = roots
			}
		}
		for _, key := range sortedKeys(p.Keys) {
			current, known := desired[target]
			if !known {
				current = target.spec
			}
			next, err := current.with(key, p.Keys[key])
			if err != nil {
				fail(under("/"+key, err))
				continue
			}
			if _, was := target.patched[key]; !was && !target.written {
				// The file cannot tell what an unwritten entry had here.
				if target.shadow == nil {
					target.shadow = map[string]json.RawMessage{}
				}
				target.shadow[key] = target.spec.key(key)
			}
			if owned[target] == nil {
				owned[target] = map[string]int{}
			}
			desired[target], owned[target][key] = next, index
		}
	}
	for e, next := range desired {
		if !e.spec.equal(next) {
			e.apply(next)
		}
		e.patched = owned[e]
	}
}

// insert appends the entries of a patch to group, or to the top level, and
// returns them.
func (t *tree) insert(group *entry, specs []spec, from origin) ([]*entry, error) {
	if group != nil && group.spec.kind() != "group" {
		return nil, fmt.Errorf("entry %q is not a group", group.id)
	}
	var taken func([]spec) error
	taken = func(list []spec) error {
		for _, s := range list {
			if existing := t.byID[s.ID]; s.ID != "" && existing != nil && !existing.gone {
				return fmt.Errorf("entry id %q is already used in the included file", s.ID)
			}
			if err := taken(s.Plugins); err != nil {
				return err
			}
		}
		return nil
	}
	if err := taken(specs); err != nil {
		return nil, err
	}
	var adopt func(parent *entry, list []spec, from origin) []*entry
	adopt = func(parent *entry, list []spec, from origin) []*entry {
		entries := make([]*entry, 0, len(list))
		for index, s := range list {
			e := t.newEntry(parent, s, false)
			e.origin = from.child(index)
			e.children = adopt(e, s.Plugins, *e.origin)
			entries = append(entries, e)
		}
		return entries
	}
	entries := adopt(group, specs, from)
	if group == nil {
		t.entries = append(t.entries, entries...)
	} else {
		group.children = append(group.children, entries...)
	}
	return entries, nil
}
