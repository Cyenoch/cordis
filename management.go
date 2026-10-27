package cordis

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/Cyenoch/cordis/contract"
)

// Realm returns a view that reuses the host's routing and visibility rules.
func (h *Host) Realm(name string) *Realm { return &Realm{host: h, name: name} }

type Realm struct {
	host *Host
	name string
}

func (r *Realm) Call(ctx context.Context, required contract.Requirement, input json.RawMessage) (json.RawMessage, error) {
	return r.host.Call(ctx, r.name, required, input)
}

func (r *Realm) Emit(ctx context.Context, name string, input json.RawMessage, mode contract.Dispatch) ([]contract.EventResult, error) {
	return r.host.Emit(ctx, r.name, name, input, mode)
}

func (h *Host) Instance(id string) (contract.Instance, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	i := h.entries[id]
	if i == nil {
		return nil, contract.Fail(contract.NotReady, "entry does not exist")
	}
	return i, nil
}

func (h *Host) Instances() []contract.InstanceInfo {
	h.mu.Lock()
	result := make([]contract.InstanceInfo, 0, len(h.entries))
	instances := make([]contract.Instance, 0, len(h.entries))
	for id, i := range h.entries {
		entry := h.entrySpecs[id]
		entry.Config = slices.Clone(entry.Config)
		result = append(result, contract.InstanceInfo{Entry: entry, Package: h.packages[id]})
		instances = append(instances, i)
	}
	h.mu.Unlock()
	for n, instance := range instances {
		result[n].Status = instance.Status()
		result[n].Status.Enabled = result[n].Entry.Enabled
		result[n].Status.TemporaryStopped = result[n].Entry.Enabled && result[n].Status.State == contract.Stopped
	}
	slices.SortFunc(result, func(a, b contract.InstanceInfo) int {
		if a.Entry.ID < b.Entry.ID {
			return -1
		}
		if a.Entry.ID > b.Entry.ID {
			return 1
		}
		return 0
	})
	return result
}

func (h *Host) reserveTree(root string) (func(), error) {
	return h.reserveTrees([]string{root})
}

func (h *Host) reserveTrees(roots []string) (func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entries := make([]contract.Entry, 0, len(h.entrySpecs))
	for _, entry := range h.entrySpecs {
		entries = append(entries, entry)
	}
	selected := map[string]bool{}
	for _, root := range roots {
		selected[root] = true
	}
	owned := ownedEntries(entries, selected)
	ids := slices.Clone(roots)
	for _, entry := range owned {
		if !slices.Contains(ids, entry.ID) {
			ids = append(ids, entry.ID)
		}
	}
	for _, id := range ids {
		if h.controls[id] || h.admissions[id] {
			return nil, contract.Fail(contract.Conflict, "entry ownership tree has an operation in progress")
		}
	}
	if h.controls == nil {
		h.controls = map[string]bool{}
	}
	for _, id := range ids {
		h.controls[id] = true
	}
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, id := range ids {
			delete(h.controls, id)
		}
	}, nil
}

// Remove deletes the durable entry record after its runtime tree has stopped.
// It never deletes plugin namespace data or uninstalls package code.
func (h *Host) Remove(ctx context.Context, id string) error {
	if err := h.lockOperation(ctx); err != nil {
		return err
	}
	defer h.unlockOperation()
	release, err := h.reserveTree(id)
	if err != nil {
		return err
	}
	defer release()
	entries, err := h.store.Entries()
	if err != nil {
		return err
	}
	h.mu.Lock()
	for key, entry := range h.entrySpecs {
		if _, builtin := h.builtins[key]; builtin {
			entries = append(entries, entry)
		}
	}
	h.mu.Unlock()
	owned := ownedEntries(entries, map[string]bool{id: true})
	if err := h.disable(ctx, id); err != nil {
		return err
	}
	var result error
	for n := len(owned) - 1; n >= 0; n-- {
		if owned[n].Package != "builtin" {
			err := h.store.DeleteEntry(ctx, owned[n].ID)
			result = errors.Join(result, err)
			if err != nil && !catalogCommitted(err) {
				return result
			}
		}
	}
	return result
}

func (h *Host) controlBusy(id, parent string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.controls[id] {
		return true
	}
	for parent != "" {
		if h.controls[parent] {
			return true
		}
		parent = h.entrySpecs[parent].Parent
	}
	return false
}

func (h *Host) packageBusy(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.packageOps[id]
}

func (h *Host) reservePackage(id string) (func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.packageOps[id] {
		return nil, contract.Fail(contract.Conflict, "package operation is in progress")
	}
	if h.packageOps == nil {
		h.packageOps = map[string]bool{}
	}
	h.packageOps[id] = true
	return func() { h.mu.Lock(); delete(h.packageOps, id); h.mu.Unlock() }, nil
}

func (h *Host) reservePlan(plan CascadePlan) (func(), error) {
	releasePackage, err := h.reservePackage(plan.Target.ID)
	if err != nil {
		return nil, err
	}
	roots := make([]string, 0, len(plan.Entries))
	for _, entry := range plan.Entries {
		roots = append(roots, entry.ID)
	}
	// Selection cascades omit the target from the removal plan, but replacement
	// still snapshots and rebuilds its entire current ownership tree.
	h.mu.Lock()
	for id, ref := range h.packages {
		if ref.ID == plan.Target.ID && !slices.Contains(roots, id) {
			roots = append(roots, id)
		}
	}
	h.mu.Unlock()
	releaseTrees, err := h.reserveTrees(roots)
	if err != nil {
		releasePackage()
		return nil, err
	}
	return func() { releaseTrees(); releasePackage() }, nil
}

// waitOperation relinquishes only global admission. Entry/tree reservations
// remain owned by the operation until its final state has been recorded.
func (h *Host) waitOperation(wait func() error) error {
	h.unlockOperation()
	err := wait()
	h.operations <- struct{}{}
	return err
}
