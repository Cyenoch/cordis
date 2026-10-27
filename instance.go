package cordis

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/Cyenoch/cordis/contract"
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
	h.operations.Lock()
	h.mu.Lock()
	current := h.entries[i.id]
	entry := h.entrySpecs[i.id]
	h.mu.Unlock()
	if current != i {
		h.operations.Unlock()
		return contract.UpdateResult{}, contract.Fail(contract.NotReady, "instance has been replaced")
	}
	entry.Config = slices.Clone(config)
	wait, err := h.engine.Configure(i.id, config, func() error {
		if err := h.store.SaveEntry(ctx, entry); err != nil {
			return err
		}
		h.mu.Lock()
		h.entrySpecs[i.id] = entry
		h.mu.Unlock()
		return nil
	})
	h.operations.Unlock()
	if err != nil {
		return contract.UpdateResult{}, err
	}
	return wait(ctx)
}

func (i *hostInstance) Stop(ctx context.Context) error {
	h := i.host
	h.operations.Lock()
	defer h.operations.Unlock()
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
	h.operations.Lock()
	entries, err := h.store.Entries()
	if err != nil {
		h.operations.Unlock()
		return err
	}
	instances := []contract.Instance{}
	for _, entry := range orderedEntries(entries) {
		if !entry.Enabled {
			continue
		}
		instance, err := h.enable(ctx, entry)
		if err != nil {
			h.operations.Unlock()
			return err
		}
		instances = append(instances, instance)
	}
	h.operations.Unlock()
	for _, instance := range instances {
		if err := instance.Wait(ctx); err != nil {
			return err
		}
	}
	return nil
}
