package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/Cyenoch/cordis/contract"
)

func (i *instance) Status() contract.Status {
	e := i.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	status := contract.Status{
		Identity: i.identity, State: i.state, Desired: i.desired, Applied: i.applied,
		// Durable intent is separate from the runtime stop request, even in Quiescing.
		Enabled: i.entry.Enabled, TemporaryStopped: !i.enabled && i.entry.Enabled,
		Outcome: i.outcome.Clone(),
	}
	if i.activation != nil {
		status.Identity = i.activation.identity
		status.Outcome = i.activation.outcome.Clone()
	}
	if i.failure != nil {
		status.Error = i.failure.Error()
	}
	if i.state == contract.Waiting {
		for _, req := range i.definition.Requires {
			if p := e.resolveLocked(i, req); p == nil || !p.owner.ready {
				reason := contract.WaitReason{Kind: "service", Requirement: &req, Reason: "missing"}
				reason.Visible = e.visibleServicesLocked(i, req)
				if len(reason.Visible) != 0 {
					reason.Reason = "incompatible"
				}
				status.Waiting = append(status.Waiting, reason)
			}
		}
		if i.entry.Parent != "" {
			parent := e.instances[i.entry.Parent]
			if parent == nil || parent.activation == nil || !parent.activation.ready || parent.activation.retiring {
				status.Waiting = append(status.Waiting, contract.WaitReason{Kind: "parent", Parent: i.entry.Parent, Reason: "unpublished"})
			}
		}
	}
	return status
}

func (e *Engine) visibleServicesLocked(i *instance, req contract.Requirement) []contract.Service {
	visible := []contract.Service{}
	for key, p := range e.services {
		if key == serviceAddress(i, p.service.Key()) && p.service.Name == req.Name && p.owner.ready && !p.owner.retiring {
			visible = append(visible, p.service)
		}
	}
	slices.SortFunc(visible, func(a, b contract.Service) int {
		if a.Major < b.Major {
			return -1
		}
		if a.Major > b.Major {
			return 1
		}
		return 0
	})
	return visible
}
func (i *instance) Wait(ctx context.Context) error {
	e := i.engine
	for {
		e.mu.Lock()
		state, err, changed := i.state, i.failure, e.changed
		var release func()
		if state != contract.Active && state != contract.Failed && state != contract.Stopped {
			release, err = i.waitEdgeLocked(ctx)
			if err != nil {
				e.mu.Unlock()
				return err
			}
		}
		e.mu.Unlock()
		switch state {
		case contract.Active:
			return nil
		case contract.Failed:
			if err != nil {
				return err
			}
			return contract.Fail(contract.PluginFailed, "instance failed")
		case contract.Stopped:
			if err != nil {
				return err
			}
			return contract.Fail(contract.NotReady, "instance stopped")
		}
		select {
		case <-ctx.Done():
			if release != nil {
				release()
			}
			return fmt.Errorf("wait for instance: %w", ctx.Err())
		case <-changed:
			if release != nil {
				release()
			}
		}
	}
}

// Instance controls participate in the same wait graph as service calls.
func (i *instance) waitEdgeLocked(ctx context.Context) (func(), error) {
	e := i.engine
	actor, _ := ctx.Value(actorKey{}).(*activation)
	if actor == nil {
		return nil, nil
	}
	if !actor.ready && (actor.instance == i || ownedByLocked(e, i, actor.instance.entry.ID)) {
		return nil, contract.Fail(contract.Reentrant, "initialization cannot wait for its own publication or owned children")
	}
	target := i.activation
	if target != nil && (target == actor || e.pathLocked(target, actor, map[*activation]bool{})) {
		return nil, contract.Fail(contract.Reentrant, "instance control wait cycle")
	}
	if e.instanceEdges[actor] == nil {
		e.instanceEdges[actor] = map[*instance]int{}
	}
	e.instanceEdges[actor][i]++
	return func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.instanceEdges[actor][i] <= 1 {
			delete(e.instanceEdges[actor], i)
		} else {
			e.instanceEdges[actor][i]--
		}
	}, nil
}
func (i *instance) finishRevisionLocked(revision uint64, result contract.UpdateResult, err error) {
	pending := i.pending
	if revision != i.desired || pending == nil || pending.finished {
		return
	}
	pending.result = result
	pending.err = err
	pending.finished = true
	close(pending.done)
}
func (i *instance) Update(ctx context.Context, config json.RawMessage) (contract.UpdateResult, error) {
	wait, err := i.configure(config, nil)
	if err != nil {
		return contract.UpdateResult{}, err
	}
	return wait(ctx)
}

func (i *instance) configure(config json.RawMessage, commit func() error) (func(context.Context) (contract.UpdateResult, error), error) {
	defer i.engine.flushDiagnostics()
	config = normalizeConfig(config)
	if err := validate(i.definition, config); err != nil {
		return nil, err
	}
	e := i.engine
	e.configMu.Lock()
	defer e.configMu.Unlock()
	e.mu.Lock()
	eligible := !e.closed && i.enabled && !i.removing && e.instances[i.entry.ID] == i
	e.mu.Unlock()
	if !eligible {
		return nil, contract.Fail(contract.NotReady, "instance is stopped or removed")
	}
	if commit != nil {
		if err := commit(); err != nil {
			return nil, err
		}
	}
	e.mu.Lock()
	if e.closed || !i.enabled || i.removing || e.instances[i.entry.ID] != i {
		e.mu.Unlock()
		return nil, contract.Fail(contract.NotReady, "instance is stopped or removed")
	}
	i.finishRevisionLocked(i.desired, contract.UpdateResult{Revision: i.desired, Superseded: true}, nil)
	i.desired++
	revision := i.desired
	i.config = config
	i.entry.Config = slices.Clone(config)
	i.failure = nil
	result := &revisionResult{done: make(chan struct{})}
	i.pending = result
	if i.activation != nil {
		i.activation.terminal = contract.Waiting
		e.retireLocked(i.activation, contract.Waiting)
	} else {
		e.stateLocked(i, contract.Waiting)
	}
	e.signalLocked()
	e.mu.Unlock()
	return func(ctx context.Context) (contract.UpdateResult, error) {
		e.mu.Lock()
		release, err := i.waitEdgeLocked(ctx)
		e.mu.Unlock()
		if err != nil {
			return contract.UpdateResult{Revision: revision}, err
		}
		if release != nil {
			defer release()
		}
		select {
		case <-ctx.Done():
			return contract.UpdateResult{Revision: revision}, fmt.Errorf("wait for configuration revision %d: %w", revision, ctx.Err())
		case <-result.done:
			return result.result, result.err
		}
	}, nil
}
func (i *instance) Stop(ctx context.Context) error {
	wait, err := i.stop(ctx, false)
	if err != nil {
		return err
	}
	return wait(ctx)
}

// Stop submits retirement synchronously; the returned waiter owns no admission lock.
func (e *Engine) Stop(ctx context.Context, target contract.Instance) (func(context.Context) error, error) {
	i, ok := target.(*instance)
	if !ok || i.engine != e {
		return nil, contract.Fail(contract.NotReady, "entry not found")
	}
	return i.stop(ctx, false)
}

// Disable records persistent disable intent in the same state snapshot as Stop.
func (e *Engine) Disable(ctx context.Context, entryID string) (func(context.Context) error, error) {
	e.mu.Lock()
	i := e.instances[entryID]
	e.mu.Unlock()
	if i == nil {
		return nil, contract.Fail(contract.NotReady, "entry does not exist")
	}
	return i.stop(ctx, true)
}

func (i *instance) stop(ctx context.Context, disable bool) (func(context.Context) error, error) {
	defer i.engine.flushDiagnostics()
	e := i.engine
	e.configMu.Lock()
	e.mu.Lock()
	if e.instances[i.entry.ID] != i {
		e.mu.Unlock()
		e.configMu.Unlock()
		return nil, contract.Fail(contract.NotReady, "instance was removed")
	}
	actor, _ := ctx.Value(actorKey{}).(*activation)
	if actor != nil && (actor.instance == i || ownedByLocked(e, actor.instance, i.entry.ID)) {
		e.mu.Unlock()
		e.configMu.Unlock()
		return nil, contract.Fail(contract.Reentrant, "cannot synchronously stop the caller's ownership tree")
	}
	var release func()
	var waitErr error
	if i.activation != nil {
		release, waitErr = i.waitEdgeLocked(ctx)
	}
	if waitErr != nil {
		e.mu.Unlock()
		e.configMu.Unlock()
		return nil, waitErr
	}
	if release != nil {
		defer release()
	}
	if disable {
		i.entry.Enabled = false
	}
	i.enabled = false
	// Parent ownership persists even while a child is waiting for dependencies.
	var stopTree func(*instance)
	stopTree = func(owner *instance) {
		owner.enabled = false
		for _, child := range e.instances {
			if child.entry.Parent == owner.entry.ID {
				stopTree(child)
			}
		}
		owner.finishRevisionLocked(owner.desired, contract.UpdateResult{Revision: owner.desired}, contract.Fail(contract.Canceled, "instance stopped"))
		if owner.activation != nil {
			e.retireLocked(owner.activation, contract.Stopped)
		} else {
			e.stateLocked(owner, contract.Stopped)
		}
	}
	stopTree(i)
	var pending []*activation
	var stoppedErrors []error
	for _, child := range e.instances {
		if child == i || ownedByLocked(e, child, i.entry.ID) {
			if child.activation != nil {
				pending = append(pending, child.activation)
			} else if child.failure != nil {
				stoppedErrors = append(stoppedErrors, child.failure)
			}
		}
	}
	e.signalLocked()
	e.mu.Unlock()
	e.configMu.Unlock()
	return func(ctx context.Context) error {
		e.mu.Lock()
		actor, _ := ctx.Value(actorKey{}).(*activation)
		if actor != nil {
			for _, a := range pending {
				select {
				case <-a.done:
					continue
				default:
				}
				if a == actor || e.pathLocked(a, actor, map[*activation]bool{}) {
					e.mu.Unlock()
					return contract.Fail(contract.Reentrant, "instance cleanup wait cycle")
				}
			}
			if e.edges[actor] == nil {
				e.edges[actor] = map[*activation]int{}
			}
			for _, a := range pending {
				e.edges[actor][a]++
			}
			defer func() {
				e.mu.Lock()
				defer e.mu.Unlock()
				for _, a := range pending {
					if e.edges[actor][a] <= 1 {
						delete(e.edges[actor], a)
					} else {
						e.edges[actor][a]--
					}
				}
			}()
		}
		e.mu.Unlock()
		errs := slices.Clone(stoppedErrors)
		for _, a := range pending {
			select {
			case <-ctx.Done():
				return fmt.Errorf("instance cleanup blocked: %w", ctx.Err())
			case <-a.done:
			}
			errs = append(errs, a.cleanupErr)
		}
		return errors.Join(errs...)
	}, nil
}
func ownedByLocked(e *Engine, i *instance, parent string) bool {
	for i.entry.Parent != "" {
		if i.entry.Parent == parent {
			return true
		}
		i = e.instances[i.entry.Parent]
	}
	return false
}

// Configure validates, durably records the desired configuration through the
// host's commit, then submits it. Caller serializes entry mutations; no engine
// lock is held while commit performs I/O. Waiting is separate from submission.
func (e *Engine) Configure(target contract.Instance, config json.RawMessage, commit func() error) (func(context.Context) (contract.UpdateResult, error), error) {
	i, ok := target.(*instance)
	if !ok || i.engine != e {
		return nil, contract.Fail(contract.NotReady, "entry not found")
	}
	return i.configure(config, commit)
}
