package cordis

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

type hostInstance struct {
	contract.Instance
	host        *Host
	id          string
	diagnostics []string
}

func (i *hostInstance) Status() contract.Status {
	status := i.Instance.Status()
	status.Diagnostics = slices.Clone(i.diagnostics)
	return status
}

func (i *hostInstance) Update(ctx context.Context, config json.RawMessage) (contract.UpdateResult, error) {
	h := i.host
	if err := h.lockOperation(ctx); err != nil {
		return contract.UpdateResult{}, err
	}
	h.mu.Lock()
	current := h.entries[i.id]
	entry := h.entrySpecs[i.id]
	_, builtin := h.builtins[i.id]
	h.mu.Unlock()
	if current != i {
		h.unlockOperation()
		return contract.UpdateResult{}, contract.Fail(contract.NotReady, "instance has been replaced")
	}
	entry.Config = slices.Clone(config)
	var commitErr error
	wait, err := h.engine.Configure(i.id, config, func() error {
		if !builtin {
			commitErr = h.store.SaveEntry(ctx, entry)
			if commitErr != nil && !catalogCommitted(commitErr) {
				return commitErr
			}
		}
		h.mu.Lock()
		h.entrySpecs[i.id] = entry
		h.mu.Unlock()
		return nil
	})
	h.unlockOperation()
	if err != nil {
		return contract.UpdateResult{}, err
	}
	result, waitErr := wait(ctx)
	return result, errors.Join(commitErr, waitErr)
}

func catalogCommitted(err error) bool {
	var committed *packagehost.CommitError
	return errors.As(err, &committed)
}

func (i *hostInstance) Stop(ctx context.Context) error {
	h := i.host
	if err := h.lockOperation(ctx); err != nil {
		return err
	}
	defer h.unlockOperation()
	return i.Instance.Stop(ctx)
}

func orderedEntries(entries []contract.Entry) []contract.Entry {
	byID := make(map[string]contract.Entry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	slices.SortFunc(entries, func(a, b contract.Entry) int {
		depth := func(entry contract.Entry) int {
			depth := 0
			for entry.Parent != "" && depth < len(entries) {
				parent, exists := byID[entry.Parent]
				if !exists {
					break
				}
				depth++
				entry = parent
			}
			return depth
		}
		if da, db := depth(a), depth(b); da < db {
			return -1
		} else if da > db {
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return entries
}

// Restore enables the catalog's selected entries only when the host explicitly
// requests it, after configuring realms, capability handlers and validators.
func (h *Host) Restore(ctx context.Context) error {
	if err := h.lockOperation(ctx); err != nil {
		return err
	}
	entries, err := h.store.Entries()
	if err != nil {
		h.unlockOperation()
		return err
	}
	instances := []contract.Instance{}
	var result error
	for _, entry := range orderedEntries(entries) {
		if !entry.Enabled {
			continue
		}
		instance, err := h.enable(ctx, entry)
		if err != nil && instance == nil {
			h.unlockOperation()
			return errors.Join(result, err)
		}
		result = errors.Join(result, err)
		instances = append(instances, instance)
	}
	h.unlockOperation()
	for _, instance := range instances {
		result = errors.Join(result, instance.Wait(ctx))
	}
	return result
}
