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
	owner       *contract.Identity
}

func (i *hostInstance) currentLocked() bool {
	if i.owner == nil {
		return i.host.entries[i.id] == i
	}
	child, exists := i.host.dynamic[i.id]
	return exists && child.instance == i && child.owner == *i.owner
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
	current := i.currentLocked()
	entry := h.entrySpecs[i.id]
	if i.owner != nil {
		entry = h.dynamic[i.id].entry
	}
	_, builtin := h.builtins[i.id]
	h.mu.Unlock()
	if !current {
		h.unlockOperation()
		return contract.UpdateResult{}, contract.Fail(contract.NotReady, "instance has been replaced")
	}
	release, err := h.reserveTree(i.id)
	if err != nil {
		h.unlockOperation()
		return contract.UpdateResult{}, err
	}
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	entry.Config = slices.Clone(config)
	var commitErr error
	wait, err := h.engine.Configure(i.Instance, config, func() error {
		if !builtin && i.owner == nil {
			commitErr = h.store.SaveEntry(ctx, entry)
			if commitErr != nil && !catalogCommitted(commitErr) {
				return commitErr
			}
		}
		h.mu.Lock()
		if i.owner == nil {
			h.entrySpecs[i.id] = entry
		} else if child, exists := h.dynamic[i.id]; exists && child.instance == i {
			child.entry = entry
			h.dynamic[i.id] = child
		}
		h.mu.Unlock()
		return nil
	})
	// The revision now belongs to core. Later revisions and Stop may supersede
	// this wait; only registration/replacement operations need a long reservation.
	if err == nil {
		h.resetRecovery(i.id)
	}
	release()
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
	h.mu.Lock()
	current := i.currentLocked()
	h.mu.Unlock()
	if !current {
		h.unlockOperation()
		return contract.Fail(contract.NotReady, "instance has been replaced")
	}
	release, err := h.reserveTree(i.id)
	if err != nil {
		h.unlockOperation()
		return err
	}
	defer release()
	wait, err := h.engine.Stop(ctx, i.Instance)
	if err == nil {
		h.cancelRecoveryTree(i.id)
	}
	h.unlockOperation()
	if err != nil {
		return err
	}
	return wait(ctx)
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
// requests it, after opening the package store.
func (h *Host) Restore(ctx context.Context) (contract.RestoreReport, error) {
	var report contract.RestoreReport
	if err := h.lockOperation(ctx); err != nil {
		return report, err
	}
	entries, err := h.store.Entries()
	if err != nil {
		h.unlockOperation()
		return report, err
	}
	instances := map[int]contract.Instance{}
	blocked := map[string]bool{}
	var result error
	for _, entry := range orderedEntries(entries) {
		if !entry.Enabled {
			report.Entries = append(report.Entries, contract.RestoreEntry{ID: entry.ID, Action: "disabled"})
			continue
		}
		record := contract.RestoreEntry{ID: entry.ID, Action: "registered"}
		h.mu.Lock()
		current, exists := h.entrySpecs[entry.ID]
		existing := h.entries[entry.ID]
		ref := h.packages[entry.ID]
		h.mu.Unlock()
		if exists {
			selected, selectErr := h.store.Selected(entry.Package)
			if selectErr != nil || ref != selected.Ref || current.Package != entry.Package || current.Parent != entry.Parent || current.Definition != entry.Definition || !slices.Equal(current.Isolate, entry.Isolate) {
				record.Action = "conflict"
				record.Error = errors.Join(contract.Fail(contract.Conflict, "restore target differs from current registration"), selectErr)
				blocked[entry.ID] = true
			} else {
				record.Action = "existing"
				record.Status = existing.Status()
				if record.Status.State == contract.Failed {
					record.Error = existing.Wait(ctx)
				}
			}
			report.Entries = append(report.Entries, record)
			h.restoreFailure(ctx, record)
			result = errors.Join(result, record.Error)
			continue
		}
		if blocked[entry.Parent] {
			record.Action = "blocked"
			record.Error = contract.Fail(contract.NotReady, "restore parent could not be registered")
			blocked[entry.ID] = true
			report.Entries = append(report.Entries, record)
			h.restoreFailure(ctx, record)
			result = errors.Join(result, record.Error)
			continue
		}
		if h.controlBusy(entry.ID, entry.Parent) || h.packageBusy(entry.Package) {
			record.Action = "conflict"
			record.Error = contract.Fail(contract.Conflict, "restore target has a package or ownership tree operation in progress")
			blocked[entry.ID] = true
			report.Entries = append(report.Entries, record)
			h.restoreFailure(ctx, record)
			result = errors.Join(result, record.Error)
			continue
		}
		instance, err := h.enable(ctx, entry)
		if err != nil && instance == nil {
			blocked[entry.ID] = true
			record.Action = "failed"
		}
		record.Error = err
		result = errors.Join(result, err)
		if instance != nil {
			instances[len(report.Entries)] = instance
		}
		report.Entries = append(report.Entries, record)
		h.restoreFailure(ctx, record)
	}
	h.unlockOperation()
	for index, instance := range instances {
		err := instance.Wait(ctx)
		report.Entries[index].Status = instance.Status()
		report.Entries[index].Error = errors.Join(report.Entries[index].Error, err)
		result = errors.Join(result, err)
	}
	return report, result
}

func (h *Host) restoreFailure(ctx context.Context, entry contract.RestoreEntry) {
	if entry.Error != nil && h.options.Logger != nil {
		h.options.Logger.ErrorContext(ctx, "entry restore failed", "operation", "restore",
			"entry", entry.ID, "stage", entry.Action, "errorCode", contract.ErrorCode(entry.Error), "error", entry.Error)
	}
}
