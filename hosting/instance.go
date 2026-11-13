package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"time"

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
	closing  <-chan struct{}            // closed when the gate last taken down has gone
	enabled  bool
	desired  uint64
	applied  uint64
	updating int  // Update calls whose revision has not settled
	run      *run // the current or last activation
	identity contract.Identity
	outcome  contract.Outcome
	failed   error // why the last activation failed, as far as the bridge knows
	refused  bool  // the package store refused to run the last activation's package
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
		Validate: func(config json.RawMessage) error { return validConfig(i.spec, config) },
		Start: func(ctx context.Context, s *cordis.Scope, config json.RawMessage) (err error) {
			defer func() { i.note(err) }()
			return i.start(ctx, s, config)
		},
	}
}

func (i *hostInstance) begin(r *run) {
	i.mu.Lock()
	i.run, i.identity, i.outcome, i.failed, i.refused, i.cleanup = r, r.identity, contract.Outcome{}, nil, false, nil
	i.mu.Unlock()
}

// refuse records that the package store refused to run the package of r.
func (i *hostInstance) refuse(r *run) {
	i.mu.Lock()
	if i.run == r {
		i.refused = true
	}
	i.mu.Unlock()
}

// refusedRun reports whether the last activation failed because the
// package store refused to run its package. Starting again is refused the
// same way until the package is approved.
func (i *hostInstance) refusedRun() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.refused
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
	outcome.StartupDuration = r.startup
	state := "stopped"
	if outcome.FailureCode == contract.Canceled {
		state = "canceled"
	} else if outcome.FailureCode != "" || outcome.CleanupCode != "" {
		state = "failed"
	}
	i.setOutcome(r, outcome)
	i.host.logEnd(r, state, outcome, failed)
	return i.host.store.EndRun(context.Background(), r.identity, state, outcome)
}

// Status combines the kernel state with the entry's record. A disabled or
// temporarily stopped entry reads as stopped rather than waiting or failed.
// Waiting always names what the entry waits for.
func (i *hostInstance) Status() contract.Status {
	kernel := i.instance.Status()
	waiting := i.waitReasons(kernel.Missing)
	i.mu.Lock()
	status := contract.Status{
		Identity: i.identity, Enabled: i.enabled, TemporaryStopped: i.enabled && i.owner == nil && i.gate == nil,
		Desired: i.desired, Outcome: i.outcome.Clone(), Diagnostics: slices.Clone(i.diagnostics),
		Waiting: []contract.WaitReason{},
	}
	status.State = contract.State(kernel.State.String())
	switch halted := status.TemporaryStopped || !status.Enabled; {
	case halted && (kernel.State == cordis.Waiting || kernel.State == cordis.Failed):
		status.State = contract.Stopped
	case kernel.State == cordis.Waiting && len(waiting) == 0:
		// Nothing the entry requires is missing: its run gate or the
		// kernel has yet to get to it, and it is starting.
		status.State = contract.Starting
	case kernel.State == cordis.Waiting:
		status.Waiting = waiting
	}
	// A revision has taken effect once nothing is starting or stopping for it.
	if i.updating == 0 && status.State != contract.Starting && status.State != contract.Quiescing {
		i.applied = i.desired
	}
	status.Applied = i.applied
	run, failed := i.run, i.failed
	i.mu.Unlock()
	if status.Identity == (contract.Identity{}) {
		status.Identity = contract.Identity{Package: i.pkg.Ref.ID, Entry: i.id, Digest: i.pkg.Ref.Digest}
	}
	if status.State == contract.Failed {
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
	return status
}

// waitReasons says what the entry waits for. The run gate is not a reason:
// it is up whenever the entry is not stopped.
func (i *hostInstance) waitReasons(missing []cordis.Missing) []contract.WaitReason {
	reasons := []contract.WaitReason{}
	for _, missing := range missing {
		switch {
		case missing.Service == runKey(i.id).Name():
		case i.parent != "" && missing.Service == parentKey(i.parent).Name():
			reasons = append(reasons, contract.WaitReason{Kind: "parent", Parent: i.parent, Visible: []contract.Service{}, Reason: "unpublished"})
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
				reasons = append(reasons, reason)
			}
		}
	}
	return reasons
}

// Wait blocks until the entry is Active, and returns the error it failed
// with. An entry that waits for a service or for its parent keeps Wait
// waiting; a stopped one does not, and nor does a closed Host.
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
		case <-i.host.done:
			// Close stops every instance and reports no further change.
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Update validates config, saves it for a catalog entry and applies it. A
// later revision that replaces it sets Superseded rather than an error.
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
	if err := validConfig(i.spec, config); err != nil {
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
		err := i.stopForGood(ctx)
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
	before := i.instance.Status()
	i.mu.Lock()
	gate := i.gate
	i.gate = nil
	if gate != nil {
		i.closing = gate.Done()
	}
	i.mu.Unlock()
	if gate == nil {
		return i.settled(ctx, before)
	}
	// Withdrawing the gate lets the entry clean up like any consumer. The
	// gate is gone as soon as the entry has let go of it; the entry reports
	// the end of its own stop a moment later, and the caller is told when
	// that has happened too, with what went wrong while it stopped.
	stopErr := gate.Stop(ctx)
	if ctx.Err() != nil {
		if stopErr == nil {
			stopErr = ctx.Err()
		}
		return stopErr
	}
	return errors.Join(stopErr, i.settled(ctx, before))
}

// settled waits until nothing starts or stops for the entry and returns
// what it went wrong with since before.
func (i *hostInstance) settled(ctx context.Context, before cordis.Status) error {
	err := i.instance.Wait(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return since(before, err)
}

// stopForGood stops the kernel instance for good and returns what went
// wrong while it stopped.
func (i *hostInstance) stopForGood(ctx context.Context) error {
	before := i.instance.Status()
	err := i.instance.Stop(ctx)
	if !i.gone() {
		return err
	}
	return since(before, err)
}

// since leaves out of a stop's result the failure the instance was already
// in, or already failing with, before the stop. It cleans up because of
// that failure rather than because of the stop; the failure stays in its
// status and run history and would only make a stop that went fine look
// like one that did not.
func since(before cordis.Status, err error) error {
	if before.Err != nil && holds(err, before.Err) {
		return nil
	}
	return err
}

// holds reports whether err is target itself or wraps that very value.
// errors.Is would not do: a *contract.Error matches any other one with the
// same code, and most plugin failures share plugin_failed.
func holds(err, target error) bool {
	if err == nil {
		return false
	}
	if reflect.TypeOf(err).Comparable() && err == target {
		return true
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() error }:
		return holds(wrapped.Unwrap(), target)
	case interface{ Unwrap() []error }:
		return slices.ContainsFunc(wrapped.Unwrap(), func(err error) bool { return holds(err, target) })
	}
	return false
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
	err := i.stopForGood(ctx)
	i.mu.Lock()
	gate := i.gate
	i.gate = nil
	if gate != nil {
		i.closing = gate.Done()
	}
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

// logEnd records how an activation ended. One that failed before it was
// active has no startup, and its duration says how long starting took.
func (h *Host) logEnd(r *run, state string, outcome contract.Outcome, failed error) {
	attrs := []any{"state", state, "duration", time.Since(r.began)}
	if r.startup == 0 {
		attrs = append(attrs, "stage", "starting")
	}
	if outcome.Reason != "" {
		attrs = append(attrs, "reason", outcome.Reason)
	}
	if outcome.ExitCode != nil {
		attrs = append(attrs, "exitCode", *outcome.ExitCode)
	}
	if outcome.Signal != "" {
		attrs = append(attrs, "signal", outcome.Signal)
	}
	if outcome.Forced {
		attrs = append(attrs, "forced", true)
	}
	if state != "failed" {
		h.logActivation(r, slog.LevelInfo, "activation "+state, attrs...)
		return
	}
	if outcome.FailureCode != "" {
		attrs = append(attrs, "errorCode", outcome.FailureCode)
	}
	if failed != nil {
		attrs = append(attrs, "error", failed)
	}
	if outcome.CleanupCode != "" {
		attrs = append(attrs, "cleanupCode", outcome.CleanupCode)
	}
	h.logActivation(r, slog.LevelError, "activation failed", attrs...)
}

// validConfig rejects a configuration the definition's schema does not
// accept, as invalid.
func validConfig(spec contract.DefinitionSpec, config json.RawMessage) error {
	if err := schema.Validate(spec.ConfigSchema, config); err != nil {
		return contract.Wrap(contract.Invalid, err)
	}
	return nil
}
