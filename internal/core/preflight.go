package core

import "github.com/Cyenoch/cordis/contract"

// Preflight checks immutable entry admission before a facade durably enables it.
// The facade serializes registration, removal and close across this boundary.
func (e *Engine) Preflight(entry contract.Entry, definition contract.Definition) error {
	if entry.ID == "" || definition.Start == nil {
		return contract.Fail(contract.Invalid, "entry and start required")
	}
	if err := validate(definition, normalizeConfig(entry.Config)); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.instances[entry.ID] != nil {
		return contract.Fail(contract.Conflict, "entry already exists or engine closed")
	}
	if _, ok := e.realms[entry.Realm]; !ok {
		return contract.Fail(contract.Invalid, "unknown realm")
	}
	if entry.Parent != "" {
		parent := e.instances[entry.Parent]
		if parent == nil || !parent.enabled || parent.removing || !e.descendantLocked(entry.Realm, parent.entry.Realm) {
			return contract.Fail(contract.Invalid, "parent unavailable")
		}
	}
	return nil
}
