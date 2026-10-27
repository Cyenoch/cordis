package cordis

import (
	"context"
	"errors"
	"slices"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

// CascadePlan lists parent-first entries; execution retires them in reverse.
// Replacement rebuilds target instances separately, removal includes them here.
type CascadePlan struct {
	Target  contract.PackageRef      `json:"target"`
	Action  packagehost.ChangeAction `json:"action"`
	Entries []contract.Entry         `json:"entries"`
}

// ConfirmCascade must not reenter Host management operations. It receives an
// independent snapshot; mutations cannot change the plan that will be executed.
type ConfirmCascade func(context.Context, CascadePlan) error

func (h *Host) PlanCascade(ctx context.Context, ref contract.PackageRef, action packagehost.ChangeAction) (CascadePlan, error) {
	if err := h.lockOperation(ctx); err != nil {
		return CascadePlan{}, err
	}
	defer h.unlockOperation()
	return h.planCascade(ref, action)
}

func (h *Host) planCascade(ref contract.PackageRef, action packagehost.ChangeAction) (CascadePlan, error) {
	options, err := h.changeOptions()
	if err != nil {
		return CascadePlan{}, err
	}
	packages, err := h.store.ReverseDependencies(ref, options.Enabled, action)
	if err != nil {
		return CascadePlan{}, err
	}
	if action == packagehost.RemovePackage {
		if selected, err := h.store.Selected(ref.ID); err == nil && selected.Ref == ref {
			packages = append(packages, ref.ID)
		}
	}
	entries, err := h.store.Entries()
	if err != nil {
		return CascadePlan{}, err
	}
	h.mu.Lock()
	for id, entry := range h.entrySpecs {
		if _, builtin := h.builtins[id]; builtin {
			entry.Config = slices.Clone(entry.Config)
			entries = append(entries, entry)
		}
	}
	h.mu.Unlock()
	roots := map[string]bool{}
	for _, entry := range entries {
		if entry.Enabled && slices.Contains(packages, entry.Package) {
			roots[entry.ID] = true
		}
	}
	return CascadePlan{Target: ref, Action: action, Entries: ownedEntries(entries, roots)}, nil
}

// optionsAfterCascade models durable enabled entries that will remain, so all
// package/trust/config/data preflight happens before any retirement.
func (h *Host) optionsAfterCascade(plan CascadePlan) (packagehost.ChangeOptions, error) {
	options, err := h.changeOptions()
	if err != nil {
		return options, err
	}
	entries, err := h.store.Entries()
	if err != nil {
		return options, err
	}
	removed := map[string]bool{}
	for _, entry := range plan.Entries {
		removed[entry.ID] = true
	}
	options.Enabled = nil
	for _, entry := range entries {
		if entry.Enabled && !removed[entry.ID] && !slices.Contains(options.Enabled, entry.Package) {
			options.Enabled = append(options.Enabled, entry.Package)
		}
	}
	// A live activation may use an older selection; preserve unrelated refs.
	h.mu.Lock()
	options.InUse = nil
	for id, instance := range h.entries {
		if removed[id] {
			continue
		}
		if _, builtin := h.builtins[id]; builtin {
			continue
		}
		if state := instance.(*hostInstance).Instance.Status().State; state != contract.Stopped && state != contract.Failed {
			options.InUse = append(options.InUse, h.packages[id])
		}
	}
	h.mu.Unlock()
	return options, nil
}

func (h *Host) executeCascade(ctx context.Context, plan CascadePlan, confirm ConfirmCascade) error {
	if confirm == nil {
		return contract.Fail(contract.Denied, "cascade requires explicit host confirmation")
	}
	snapshot := plan
	snapshot.Entries = slices.Clone(plan.Entries)
	for n := range snapshot.Entries {
		snapshot.Entries[n].Config = slices.Clone(snapshot.Entries[n].Config)
	}
	if err := confirm(ctx, snapshot); err != nil {
		return err
	}
	var result error
	for n := len(plan.Entries) - 1; n >= 0; n-- {
		entry := plan.Entries[n]
		h.mu.Lock()
		_, live := h.entries[entry.ID]
		h.mu.Unlock()
		if live {
			err := h.disableOne(ctx, entry.ID)
			result = errors.Join(result, err)
			if err != nil && !h.engine.Missing(entry.ID) {
				return result
			}
		} else {
			if !entry.Enabled {
				continue
			}
			entry.Enabled = false
			if err := h.store.SaveEntry(ctx, entry); err != nil {
				result = errors.Join(result, err)
				if !catalogCommitted(err) {
					return result
				}
			}
		}
	}
	return result
}

func (h *Host) ReplaceCascade(ctx context.Context, ref contract.PackageRef, options packagehost.ChangeOptions, confirm ConfirmCascade) error {
	if err := h.lockOperation(ctx); err != nil {
		return err
	}
	defer h.unlockOperation()
	if options.MigrationCommitted {
		if err := h.store.ConfirmDataVersion(ctx, ref.ID, options.DataVersion); err != nil {
			return err
		}
	}
	plan, err := h.planCascade(ref, packagehost.SelectPackage)
	if err != nil {
		return err
	}
	release, err := h.reservePlan(plan)
	if err != nil {
		return err
	}
	defer release()
	actual, err := h.optionsAfterCascade(plan)
	if err != nil {
		return err
	}
	actual.DataVersion = options.DataVersion
	actual.MigrationCommitted = options.MigrationCommitted
	if err := h.store.CheckSelect(ref, actual); err != nil {
		return err
	}
	if err := h.checkRuntime(ref); err != nil {
		return err
	}
	if err := h.executeCascade(ctx, plan, confirm); err != nil {
		return err
	}
	return h.replace(ctx, ref, options)
}

func (h *Host) checkRuntime(ref contract.PackageRef) error {
	pkg, err := h.store.Get(ref)
	if err != nil {
		return err
	}
	if h.options.Runtimes[pkg.Manifest.Runtime.Kind] == nil {
		return contract.Fail(contract.Denied, "runtime not enabled by host")
	}
	return nil
}

func (h *Host) UninstallCascade(ctx context.Context, ref contract.PackageRef, deleteData bool, confirm ConfirmCascade) (packagehost.Removal, error) {
	if err := h.lockOperation(ctx); err != nil {
		return packagehost.Removal{}, err
	}
	defer h.unlockOperation()
	plan, err := h.planCascade(ref, packagehost.RemovePackage)
	if err != nil {
		return packagehost.Removal{}, err
	}
	release, err := h.reservePlan(plan)
	if err != nil {
		return packagehost.Removal{}, err
	}
	defer release()
	actual, err := h.optionsAfterCascade(plan)
	if err != nil {
		return packagehost.Removal{}, err
	}
	actual.DeleteData = deleteData
	if err := h.store.CheckUninstall(ref, actual); err != nil {
		return packagehost.Removal{}, err
	}
	if err := h.executeCascade(ctx, plan, confirm); err != nil {
		return packagehost.Removal{}, err
	}
	actual, err = h.changeOptions()
	if err != nil {
		return packagehost.Removal{}, err
	}
	actual.DeleteData = deleteData
	return h.store.Uninstall(ctx, ref, actual)
}
