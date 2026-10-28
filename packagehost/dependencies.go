package packagehost

import (
	"slices"

	"github.com/Cyenoch/cordis/contract"
)

type ChangeAction string

const (
	SelectPackage ChangeAction = "select"
	RemovePackage ChangeAction = "remove"
)

// ReverseDependencies plans an explicit cascade without mutating selections.
// The sorted result excludes ref.ID and includes only
// enabled package IDs blocked by required (including transitive) deps, using
// the same preflight rules as Select and Uninstall.
func (s *Store) ReverseDependencies(ref contract.PackageRef, enabled []string, action ChangeAction) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return nil, err
	}
	if _, err := s.get(ref); err != nil {
		return nil, err
	}
	c := s.cloneCatalog()
	switch action {
	case SelectPackage:
		c.Selected[ref.ID] = ref
	case RemovePackage:
		if c.Selected[ref.ID] == ref {
			delete(c.Selected, ref.ID)
		}
	default:
		return nil, contract.Fail(contract.Invalid, "unknown package change action")
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
	slices.Sort(result)
	return result, nil
}
