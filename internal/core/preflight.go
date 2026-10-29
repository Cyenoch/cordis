package core

import "github.com/Cyenoch/cordis/contract"

// Preflight checks immutable entry admission before a facade durably enables it.
// The facade serializes registration, removal and close across this boundary.
func (e *Engine) Preflight(entry contract.Entry, definition contract.Definition) error {
	entry.Config = normalizeConfig(entry.Config)
	if err := validateAdmission(entry, definition); err != nil {
		return err
	}
	if err := validate(definition, entry.Config); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.checkAdmissionLocked(entry, nil)
}

func validateAdmission(entry contract.Entry, definition contract.Definition) error {
	if entry.ID == "" || definition.Start == nil {
		return contract.Fail(contract.Invalid, "entry id and start callback are required")
	}
	if entry.Definition != "" && entry.Definition != definition.ID {
		return contract.Fail(contract.Invalid, "entry definition mismatch")
	}
	return nil
}

func (e *Engine) checkAdmissionLocked(entry contract.Entry, owner *contract.Identity) error {
	if e.closed {
		return contract.Fail(contract.NotReady, "engine is closed")
	}
	if e.instances[entry.ID] != nil {
		return contract.Fail(contract.Conflict, "entry already exists")
	}
	if entry.Parent != "" {
		parent := e.instances[entry.Parent]
		if parent == nil || !parent.enabled || parent.removing {
			return contract.Fail(contract.Invalid, "parent is unavailable")
		}
		if owner != nil && (parent.activation == nil || parent.activation.identity != *owner || parent.activation.retiring) {
			return contract.Fail(contract.NotReady, "parent activation is no longer current")
		}
	}
	return nil
}
