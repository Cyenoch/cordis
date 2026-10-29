package packagehost

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/Cyenoch/cordis/contract"
)

// EnableEntry validates trusted selected code and saves desired state without
// executing it. Callers supply the complete entry, not a configuration patch.
func (s *Store) EnableEntry(ctx context.Context, entry contract.Entry) (saved contract.Entry, err error) {
	var committed bool
	var ref contract.PackageRef
	defer func() { s.logMutation(ctx, "entry.enable", ref, entry.ID, committed, err) }()
	s.mu.Lock()
	revision := s.catalog.Revision
	defer func() { committed = s.catalog.Revision != revision; s.mu.Unlock() }()
	if err := s.check(); err != nil {
		return saved, err
	}
	var exists bool
	ref, exists = s.catalog.Selected[entry.Package]
	if !exists {
		return saved, ErrNotSelected
	}
	p, err := s.get(ref)
	if err != nil {
		return saved, err
	}
	var options ChangeOptions
	for _, e := range s.catalog.Entries {
		if e.Enabled && !slices.Contains(options.Enabled, e.Package) {
			options.Enabled = append(options.Enabled, e.Package)
		}
	}
	if _, err := s.prepareSelection(ctx, ref, options); err != nil {
		return saved, err
	}
	entry.Enabled = true
	if entry.Definition == "" {
		entry.Definition = p.Manifest.DefaultDefinition
	}
	if len(entry.Config) == 0 {
		entry.Config = json.RawMessage(`{}`)
	}
	if err := validateEntry(p.Manifest, entry); err != nil {
		return saved, err
	}
	entry.Config, entry.Isolate = slices.Clone(entry.Config), slices.Clone(entry.Isolate)
	c := s.cloneCatalog()
	c.Entries[entry.ID] = entry
	err = s.mutate(ctx, c)
	if err != nil && s.catalog.Revision == revision {
		return saved, err
	}
	saved = entry
	saved.Config, saved.Isolate = slices.Clone(entry.Config), slices.Clone(entry.Isolate)
	return saved, err
}

// DisableEntry atomically disables a persistent subtree without discarding its
// configuration or running plugin code.
func (s *Store) DisableEntry(ctx context.Context, id string) (err error) {
	var committed bool
	defer func() { s.logMutation(ctx, "entry.disable", contract.PackageRef{}, id, committed, err) }()
	s.mu.Lock()
	revision := s.catalog.Revision
	defer func() { committed = s.catalog.Revision != revision; s.mu.Unlock() }()
	if err := s.check(); err != nil {
		return err
	}
	if _, exists := s.catalog.Entries[id]; !exists {
		return contract.Fail(contract.NotReady, "entry does not exist")
	}
	c := s.cloneCatalog()
	children := map[string][]string{}
	for _, e := range c.Entries {
		children[e.Parent] = append(children[e.Parent], e.ID)
	}
	pending := []string{id}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		entry := c.Entries[current]
		entry.Enabled = false
		c.Entries[current] = entry
		pending = append(pending, children[current]...)
	}
	return s.mutate(ctx, c)
}
