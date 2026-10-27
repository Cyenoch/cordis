package packagehost

import (
	"sort"

	"github.com/Cyenoch/cordis/contract"
)

// ReverseDependencies plans an explicit cascade without mutating selections.
// A currently selected ref means removal; another installed ref means replacing
// that package's selection. The sorted result excludes ref.ID and includes only
// enabled package IDs blocked by required (including transitive) deps, using
// the same preflight rules as Select and Uninstall.
func (s *Store) ReverseDependencies(ref contract.PackageRef, enabled []string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return nil, err
	}
	if _, err := s.get(ref); err != nil {
		return nil, err
	}
	c, err := s.cloneCatalog()
	if err != nil {
		return nil, err
	}
	if c.Selected[ref.ID] == ref {
		delete(c.Selected, ref.ID)
	} else {
		c.Selected[ref.ID] = ref
	}
	blocked := map[string]bool{}
	for _, id := range enabled {
		if id == ref.ID {
			continue
		}
		selected, ok := c.Selected[id]
		if !ok {
			continue
		}
		if _, err := preflight(c, selected); err != nil {
			blocked[id] = true
		}
	}
	result := make([]string, 0, len(blocked))
	for id := range blocked {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}
