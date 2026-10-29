package cordis

import (
	"context"
	"errors"
	"slices"

	"github.com/Cyenoch/cordis/contract"
)

func (h *Host) Instance(id string) (contract.Instance, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	i := h.entries[id]
	if i == nil {
		i = h.dynamic[id].instance
		if i == nil {
			return nil, contract.Fail(contract.NotReady, "entry does not exist")
		}
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
		entry.Isolate = slices.Clone(entry.Isolate)
		origin := "persistent"
		if _, builtin := h.builtins[id]; builtin {
			origin = "builtin"
		}
		result = append(result, contract.InstanceInfo{Entry: entry, Package: h.packages[id], Origin: origin})
		instances = append(instances, i)
	}
	for _, child := range h.dynamic {
		entry, owner := child.entry, child.owner
		entry.Config = slices.Clone(entry.Config)
		entry.Isolate = slices.Clone(entry.Isolate)
		result = append(result, contract.InstanceInfo{Entry: entry, Package: child.ref, Origin: "dynamic", Owner: &owner})
		instances = append(instances, child.instance)
	}
	h.mu.Unlock()
	for n, instance := range instances {
		result[n].Status = instance.Status()
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

func (h *Host) entrySnapshot() ([]contract.Entry, error) {
	entries, err := h.store.Entries()
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, entry := range h.entrySpecs {
		if _, builtin := h.builtins[id]; builtin {
			entry.Config = slices.Clone(entry.Config)
			entry.Isolate = slices.Clone(entry.Isolate)
			entries = append(entries, entry)
		}
	}
	return entries, nil
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
// It does not uninstall package code.
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
	if err := h.requireStatic(id); err != nil {
		return err
	}
	entries, err := h.entrySnapshot()
	if err != nil {
		return err
	}
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

func (h *Host) requireStatic(id string) error {
	h.mu.Lock()
	_, dynamic := h.dynamic[id]
	h.mu.Unlock()
	if dynamic {
		return contract.Fail(contract.Invalid, "dynamic child: use its instance handle or restart its owner")
	}
	return nil
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
	h.packageOps[id] = true
	return func() { h.mu.Lock(); delete(h.packageOps, id); h.mu.Unlock() }, nil
}

// waitOperation relinquishes only global admission. Entry/tree reservations
// remain owned by the operation until its final state has been recorded.
func (h *Host) waitOperation(wait func() error) error {
	h.unlockOperation()
	err := wait()
	h.operations <- struct{}{}
	return err
}
