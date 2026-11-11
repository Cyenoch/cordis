package hosting

import (
	"context"
	"errors"
	"slices"

	"github.com/Cyenoch/cordis/contract"
)

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
