package packagehost

import (
	"context"
	"fmt"
	"slices"
	"sort"
)

// RegisterParent persists only a host-owned parent ID, never builtin code or
// configuration. The host must recreate the parent before restoring children.
func (s *Store) RegisterParent(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if !validName(id) {
		return fmt.Errorf("invalid external parent %q", id)
	}
	if _, ok := s.catalog.Entries[id]; ok {
		return fmt.Errorf("external parent %s conflicts with an entry", id)
	}
	if slices.Contains(s.catalog.ExternalParents, id) {
		return ctx.Err()
	}
	c, err := s.cloneCatalog()
	if err != nil {
		return err
	}
	c.ExternalParents = append(c.ExternalParents, id)
	sort.Strings(c.ExternalParents)
	return s.mutate(ctx, "register-parent", c)
}

// UnregisterParent refuses to orphan any entry, including a disabled child.
func (s *Store) UnregisterParent(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if !validName(id) {
		return fmt.Errorf("invalid external parent %q", id)
	}
	index := slices.Index(s.catalog.ExternalParents, id)
	if index < 0 {
		return ctx.Err()
	}
	c, err := s.cloneCatalog()
	if err != nil {
		return err
	}
	c.ExternalParents = slices.Delete(c.ExternalParents, index, index+1)
	return s.mutate(ctx, "unregister-parent", c)
}
