package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
	"github.com/Cyenoch/cordis/schema"
)

// hostInstance is one registered entry: a persistent one, or a dynamic
// child of a native activation. It is a kernel instance for as long as it
// is registered, whatever happens to its process.
type hostInstance struct {
	host        *Host
	id          string
	diagnostics []string
	owner       *contract.Identity // set for a dynamic child
	pkg         packagehost.Package
	spec        contract.DefinitionSpec
	parent      string
	instance    *cordis.Instance[json.RawMessage]
	scope       *cordis.Scope // where it is loaded; children derive their isolation from it

	mu       sync.Mutex
	gate     *cordis.Instance[struct{}] // provides runKey; nil while temporarily stopped
	enabled  bool
	desired  uint64
	applied  uint64
	updating int  // Update calls whose revision has not settled
	run      *run // the current or last activation
	identity contract.Identity
	outcome  contract.Outcome
	failed   error // why the last activation failed, as far as the bridge knows
	cleanup  error
}

func (i *hostInstance) currentLocked() bool {
	if i.owner == nil {
		return i.host.entries[i.id] == i
	}
	child, exists := i.host.dynamic[i.id]
	return exists && child.instance == i && child.owner == *i.owner
}

// plugin is the kernel plugin that represents the entry.
func (i *hostInstance) plugin() cordis.Plugin[json.RawMessage] {
	requires := make([]cordis.Requirement, 0, len(i.spec.Requires)+2)
	for _, consumer := range i.spec.Requires {
		r := consumer.Requirement
		requires = append(requires, contract.Require(contract.EndpointKey(r.Name, r.Major), r.MinMinor, r.MaxMinor))
	}
	if i.owner == nil {
		requires = append(requires, runKey(i.id))
	}
	if i.parent != "" {
		requires = append(requires, parentKey(i.parent))
	}
	return cordis.Plugin[json.RawMessage]{
		Name:     i.id,
		Requires: requires,
		Validate: func(config json.RawMessage) error { return schema.Validate(i.spec.ConfigSchema, config) },
		Start: func(ctx context.Context, s *cordis.Scope, config json.RawMessage) (err error) {
			defer func() { i.note(err) }()
			return i.start(ctx, s, config)
		},
	}
}

func (i *hostInstance) begin(r *run) {
	i.mu.Lock()
	i.run, i.identity, i.outcome, i.failed, i.cleanup = r, r.identity, contract.Outcome{}, nil, nil
	i.mu.Unlock()
}

func (i *hostInstance) note(err error) {
	if err == nil {
		return
	}
	i.mu.Lock()
	if i.failed == nil {
		i.failed = err
	}
	i.mu.Unlock()
}

func (i *hostInstance) setOutcome(r *run, outcome contract.Outcome) {
	i.mu.Lock()
	if i.run == r {
		i.outcome = outcome
	}
	i.mu.Unlock()
}

// endRun records how an activation ended.
func (i *hostInstance) endRun(r *run) error {
	i.mu.Lock()
	outcome, failed := i.outcome, i.failed
	if i.run != r {
		outcome, failed = contract.Outcome{}, nil
	}
	i.mu.Unlock()
	if failed = r.reported(failed); failed != nil && outcome.FailureCode == "" {
		outcome.FailureCode = contract.ErrorCode(failed)
	}
	state := "stopped"
	if outcome.FailureCode == contract.Canceled {
		state = "canceled"
	} else if outcome.FailureCode != "" || outcome.CleanupCode != "" {
		state = "failed"
	}
	i.setOutcome(r, outcome)
	return i.host.store.EndRun(context.Background(), r.identity, state, outcome)
}

// stopped reports whether the entry is temporarily stopped.
func (i *hostInstance) stopped() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.owner == nil && i.gate == nil
}

func (i *hostInstance) Status() contract.Status {
	kernel := i.instance.Status()
	i.mu.Lock()
	status := contract.Status{
		Identity: i.identity, Enabled: i.enabled, TemporaryStopped: i.enabled && i.owner == nil && i.gate == nil,
		Desired: i.desired, Applied: i.applied, Outcome: i.outcome.Clone(), Diagnostics: slices.Clone(i.diagnostics),
		Waiting: []contract.WaitReason{},
	}
	// A revision has taken effect once nothing is starting or stopping for it.
	if i.updating == 0 && kernel.State != cordis.Starting && kernel.State != cordis.Quiescing {
		i.applied = i.desired
		status.Applied = i.applied
	}
	run, failed := i.run, i.failed
	i.mu.Unlock()
	if status.Identity == (contract.Identity{}) {
		status.Identity = contract.Identity{Package: i.pkg.Ref.ID, Entry: i.id, Digest: i.pkg.Ref.Digest}
	}
	status.State = contract.State(kernel.State.String())
	halted := status.TemporaryStopped || !status.Enabled
	if halted && (kernel.State == cordis.Waiting || kernel.State == cordis.Failed) {
		status.State = contract.Stopped
		return status
	}
	if kernel.State == cordis.Failed {
		err := kernel.Err
		if run != nil {
			err = run.reported(err)
		}
		if err == nil {
			err = failed
		}
		if err != nil {
			status.Error = err.Error()
		}
	}
	for _, missing := range kernel.Missing {
		switch {
		case missing.Service == runKey(i.id).Name():
		case i.parent != "" && missing.Service == parentKey(i.parent).Name():
			status.Waiting = append(status.Waiting, contract.WaitReason{Kind: "parent", Parent: i.parent, Visible: []contract.Service{}, Reason: "unpublished"})
		default:
			for _, consumer := range i.spec.Requires {
				required := consumer.Requirement
				if required.Key() != missing.Service {
					continue
				}
				reason := contract.WaitReason{Kind: "service", Requirement: &required, Visible: []contract.Service{}, Reason: "missing"}
				var version *contract.VersionError
				if errors.As(missing.Reason, &version) {
					reason.Visible, reason.Reason = []contract.Service{version.Visible}, "incompatible"
				}
				status.Waiting = append(status.Waiting, reason)
			}
		}
	}
	return status
}

// Wait blocks until the entry is Active, and returns the error it failed
// with. An entry that waits for a service or for its parent keeps Wait
// waiting; a stopped one does not.
func (i *hostInstance) Wait(ctx context.Context) error {
	for {
		changed := i.host.changes()
		i.mu.Lock()
		gate := i.gate
		i.mu.Unlock()
		if gate != nil {
			// The entry cannot start before its gate is up.
			if err := gate.Wait(ctx); err != nil {
				return err
			}
		}
		err := i.instance.Wait(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		switch status := i.Status(); status.State {
		case contract.Active, contract.Stopped:
			return nil
		case contract.Failed:
			i.mu.Lock()
			run := i.run
			i.mu.Unlock()
			if run != nil {
				err = run.reported(err)
			}
			if err == nil {
				err = contract.Fail(contract.PluginFailed, status.Error)
			}
			return err
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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
	// Nothing is saved or stopped for a configuration the definition rejects.
	if err := schema.Validate(i.spec.ConfigSchema, config); err != nil {
		release()
		h.unlockOperation()
		return contract.UpdateResult{}, err
	}
	entry.Config = slices.Clone(config)
	var commitErr error
	if i.owner == nil {
		commitErr = h.store.SaveEntry(ctx, entry)
		if commitErr != nil && !catalogCommitted(commitErr) {
			release()
			h.unlockOperation()
			return contract.UpdateResult{}, commitErr
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
	i.mu.Lock()
	i.desired++
	i.updating++
	revision := i.desired
	i.mu.Unlock()
	defer func() {
		i.mu.Lock()
		i.updating--
		i.mu.Unlock()
	}()
	h.resetRecovery(i.id)
	// The revision now belongs to the kernel. Later revisions and Stop may
	// supersede this wait.
	release()
	h.unlockOperation()
	result := contract.UpdateResult{Revision: revision}
	err = i.instance.Update(ctx, slices.Clone(config))
	switch {
	case errors.Is(err, cordis.ErrSuperseded):
		result.Superseded, err = true, nil
	case err == nil:
		result.Applied = true
	}
	return result, errors.Join(commitErr, err)
}

func catalogCommitted(err error) bool {
	var committed *packagehost.CommitError
	return errors.As(err, &committed)
}

// Stop stops the entry until it is resumed or the host restarts. A dynamic
// child stops for good.
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
	h.cancelRecoveryTree(i.id)
	// Stopping an entry stops the persistent entries below it as well; each
	// stays stopped until it is resumed.
	h.mu.Lock()
	specs := make([]contract.Entry, 0, len(h.entrySpecs))
	for _, spec := range h.entrySpecs {
		specs = append(specs, spec)
	}
	var tree []*hostInstance
	for _, spec := range ownedEntries(specs, map[string]bool{i.id: true}) {
		if below := h.entries[spec.ID]; below != nil && below != i {
			tree = append(tree, below)
		}
	}
	h.mu.Unlock()
	h.unlockOperation()
	var result error
	for n := len(tree) - 1; n >= 0; n-- {
		result = errors.Join(result, tree[n].halt(ctx))
	}
	return errors.Join(result, i.halt(ctx))
}

// halt stops what runs for the entry and keeps it registered.
func (i *hostInstance) halt(ctx context.Context) error {
	if i.owner != nil {
		err := i.instance.Stop(ctx)
		if i.gone() {
			// A stopped child is gone for good; its identifier is free again.
			h := i.host
			h.mu.Lock()
			if child, exists := h.dynamic[i.id]; exists && child.instance == i {
				delete(h.dynamic, i.id)
			}
			h.mu.Unlock()
		}
		return err
	}
	i.mu.Lock()
	gate := i.gate
	i.gate = nil
	i.mu.Unlock()
	// A failed entry stops with the error it failed with.
	var failure error
	if kernel := i.instance.Status(); kernel.State == cordis.Failed {
		failure = kernel.Err
	}
	if gate == nil {
		return errors.Join(i.instance.Wait(ctx), failure)
	}
	// Withdrawing the gate lets the entry clean up like any consumer.
	return errors.Join(gate.Stop(ctx), failure)
}

// open lets a temporarily stopped entry run again.
func (i *hostInstance) open() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.owner != nil || i.gate != nil {
		return
	}
	id := i.id
	i.gate = cordis.Plugin[struct{}]{Name: "run:" + id, Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		return runKey(id).Provide(s, struct{}{})
	}}.Load(i.host.app.Scope(), struct{}{})
}

// retire stops the entry for good.
func (i *hostInstance) retire(ctx context.Context) error {
	// A failed entry stops with the error it failed with: the caller learns
	// that what it removes never worked.
	err := i.instance.Stop(ctx)
	i.mu.Lock()
	gate := i.gate
	i.gate = nil
	i.mu.Unlock()
	if gate != nil {
		// Also when the wait above was given up: the stop is submitted, and
		// the gate goes once the entry has cleaned up.
		if gateErr := gate.Stop(ctx); i.instance.Status().State == cordis.Stopped {
			err = errors.Join(err, gateErr)
		}
	}
	return err
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
