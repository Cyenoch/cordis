package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/Cyenoch/cordis/contract"
)

func (i *instance) Status() contract.Status {
	e := i.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	status := contract.Status{Identity: i.identity, State: i.state, Desired: i.desired, Applied: i.applied}
	if i.activation != nil {
		status.Identity = i.activation.identity
	}
	if i.failure != nil {
		status.Error = i.failure.Error()
	}
	if i.state == contract.Waiting {
		for _, req := range i.definition.Requires {
			if e.resolveLocked(i.entry.Realm, req) == nil {
				status.WaitingFor = append(status.WaitingFor, req.Key())
			}
		}
		if i.entry.Parent != "" {
			parent := e.instances[i.entry.Parent]
			if parent == nil || parent.activation == nil || !parent.activation.committed || parent.activation.retiring {
				status.WaitingFor = append(status.WaitingFor, "parent:"+i.entry.Parent)
			}
		}
	}
	return status
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
	if !actor.committed && (actor.instance == i || ownedByLocked(e, i, actor.instance.entry.ID)) {
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
	pending := i.revisions[revision]
	if pending == nil || pending.finished {
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
	config = slices.Clone(config)
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
	for rev, pending := range i.revisions {
		if !pending.finished {
			i.finishRevisionLocked(rev, contract.UpdateResult{Revision: rev, Superseded: true}, nil)
		}
		if pending.finished {
			delete(i.revisions, rev)
		}
	}
	i.desired++
	revision := i.desired
	i.config = config
	i.entry.Config = slices.Clone(config)
	i.failure = nil
	result := &revisionResult{done: make(chan struct{})}
	i.revisions[revision] = result
	if i.activation != nil {
		i.activation.terminal = contract.Waiting
		e.retireLocked(i.activation, contract.Waiting)
	} else {
		i.state = contract.Waiting
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
	e := i.engine
	e.configMu.Lock()
	e.mu.Lock()
	if e.instances[i.entry.ID] != i {
		e.mu.Unlock()
		e.configMu.Unlock()
		return contract.Fail(contract.NotReady, "instance was removed")
	}
	actor, _ := ctx.Value(actorKey{}).(*activation)
	if actor != nil && (actor.instance == i || ownedByLocked(e, actor.instance, i.entry.ID)) {
		e.mu.Unlock()
		e.configMu.Unlock()
		return contract.Fail(contract.Reentrant, "cannot synchronously stop the caller's ownership tree")
	}
	var release func()
	var waitErr error
	if i.activation != nil {
		release, waitErr = i.waitEdgeLocked(ctx)
	}
	if waitErr != nil {
		e.mu.Unlock()
		e.configMu.Unlock()
		return waitErr
	}
	if release != nil {
		defer release()
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
		for rev, pending := range owner.revisions {
			if !pending.finished {
				owner.finishRevisionLocked(rev, contract.UpdateResult{Revision: rev}, contract.Fail(contract.Canceled, "instance stopped"))
			}
		}
		if owner.activation != nil {
			e.retireLocked(owner.activation, contract.Stopped)
		} else {
			owner.state = contract.Stopped
		}
	}
	stopTree(i)
	var pending []<-chan struct{}
	for _, child := range e.instances {
		if child == i || ownedByLocked(e, child, i.entry.ID) {
			if child.activation != nil {
				pending = append(pending, child.activation.done)
			}
		}
	}
	e.signalLocked()
	e.mu.Unlock()
	e.configMu.Unlock()
	for _, done := range pending {
		select {
		case <-ctx.Done():
			return fmt.Errorf("instance cleanup blocked: %w", ctx.Err())
		case <-done:
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var errs []error
	for _, child := range e.instances {
		if child == i || ownedByLocked(e, child, i.entry.ID) {
			if child.failure != nil {
				errs = append(errs, child.failure)
			}
		}
	}
	return joinErrors(errs)
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
func (i *instance) Retry(ctx context.Context) error {
	e := i.engine
	e.mu.Lock()
	if !i.enabled || e.closed || i.removing || e.instances[i.entry.ID] != i {
		e.mu.Unlock()
		return contract.Fail(contract.NotReady, "instance is stopped or removed")
	}
	if i.state != contract.Failed {
		e.mu.Unlock()
		return contract.Fail(contract.Conflict, "only failed instances can be retried")
	}
	i.failure = nil
	i.state = contract.Waiting
	i.revisions[i.desired] = &revisionResult{done: make(chan struct{})}
	e.signalLocked()
	e.mu.Unlock()
	return i.Wait(ctx)
}

// Configure validates, durably records the desired configuration through the
// host's commit, then submits it. Caller serializes entry mutations; no engine
// lock is held while commit performs I/O. Waiting is separate from submission.
func (e *Engine) Configure(id string, config json.RawMessage, commit func() error) (func(context.Context) (contract.UpdateResult, error), error) {
	e.mu.Lock()
	i := e.instances[id]
	e.mu.Unlock()
	if i == nil {
		return nil, contract.Fail(contract.NotReady, "entry not found")
	}
	return i.configure(config, commit)
}
