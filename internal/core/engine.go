// Package core coordinates activation snapshots and their owned resources.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

type provider struct {
	service contract.Service
	handler contract.Handler
	owner   *activation
}
type revisionResult struct {
	done     chan struct{}
	result   contract.UpdateResult
	err      error
	finished bool
}

// Hooks connects activation retirement to owned child registration cleanup.
type Hooks struct {
	OnRetire     func(contract.Identity)
	OnDisposed   func(contract.Identity, contract.Outcome) error
	Logger       *slog.Logger
	CapturePanic bool
}

// Engine owns the registry, dependency graph, and all instance transitions.
// Its mutex never protects execution of plugin code.
type Engine struct {
	mu            sync.Mutex
	configMu      sync.Mutex
	instances     map[string]*instance
	services      map[serviceKey]*provider
	events        []*subscription
	edges         map[*activation]map[*activation]int
	instanceEdges map[*activation]map[*instance]int
	changed       chan struct{}
	wake          chan struct{}
	done          chan struct{}
	closed        bool
	sequence      uint64
	hooks         Hooks
	diagnostics   []slog.Record
}

type instance struct {
	engine     *Engine
	entry      contract.Entry
	parent     *instance
	definition contract.Definition
	identity   contract.Identity
	desired    uint64
	applied    uint64
	config     json.RawMessage
	state      contract.State
	failure    error
	enabled    bool
	removing   bool
	activation *activation
	pending    *revisionResult
	outcome    contract.Outcome
}

type activation struct {
	instance       *instance
	identity       contract.Identity
	revision       uint64
	config         json.RawMessage
	ctx            context.Context
	cancel         context.CancelFunc
	done           chan struct{}
	gate           chan struct{}
	work           sync.WaitGroup
	managedWork    sync.WaitGroup
	managed        []func(context.Context) error
	publishHooks   []func(context.Context) error
	retireHooks    []func(context.Context) error
	retireStarted  bool
	retireWork     sync.WaitGroup
	retireErr      error
	startFinished  bool
	publishDone    chan struct{}
	retireNotified chan struct{}
	bindings       map[string]*provider
	provided       map[string]*provider
	subscriptions  map[uint64]*subscription
	cleanups       []contract.Cleanup
	retiring       bool
	committed      bool
	ready          bool
	terminal       contract.State
	startErr       error
	forcedErr      error
	cleanupErr     error
	startedAt      time.Time
	outcome        contract.Outcome
}

func New() *Engine { return NewWithHooks(Hooks{}) }

func NewWithHooks(hooks Hooks) *Engine {
	e := &Engine{instances: map[string]*instance{}, services: map[serviceKey]*provider{}, edges: map[*activation]map[*activation]int{}, instanceEdges: map[*activation]map[*instance]int{}, changed: make(chan struct{}), wake: make(chan struct{}, 1), done: make(chan struct{}), hooks: hooks}
	go e.coordinate()
	return e
}

func (e *Engine) Add(entry contract.Entry, definition contract.Definition, identity contract.Identity) (contract.Instance, error) {
	return e.add(entry, definition, identity, nil, nil)
}

func (e *Engine) AddOwned(entry contract.Entry, definition contract.Definition, identity, parent contract.Identity) (contract.Instance, error) {
	entry.Parent = parent.Entry
	return e.add(entry, definition, identity, &parent, nil)
}

// AddWithCommit checks admission before commit validates and records configuration.
// The definition validator is retained for updates, not repeated after commit.
// No engine mutex is held while commit performs storage I/O.
func (e *Engine) AddWithCommit(entry contract.Entry, definition contract.Definition, identity contract.Identity, commit func() error) (contract.Instance, error) {
	return e.add(entry, definition, identity, nil, commit)
}

func (e *Engine) add(entry contract.Entry, definition contract.Definition, identity contract.Identity, owner *contract.Identity, commit func() error) (contract.Instance, error) {
	defer e.flushDiagnostics()
	config := normalizeConfig(entry.Config)
	entry.Config = config
	if err := validateAdmission(entry, definition); err != nil {
		return nil, err
	}
	if commit == nil {
		if err := validate(definition, config); err != nil {
			return nil, err
		}
	}
	e.configMu.Lock()
	defer e.configMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkAdmissionLocked(entry, owner); err != nil {
		return nil, err
	}
	if commit != nil {
		e.mu.Unlock()
		err := commit()
		e.mu.Lock()
		if err != nil {
			return nil, err
		}
	}
	definition.Requires = slices.Clone(definition.Requires)
	definition.Provides = slices.Clone(definition.Provides)
	entry.Config = slices.Clone(config)
	entry.Isolate = slices.Clone(entry.Isolate)
	identity.Entry = entry.ID
	if identity.Package == "" {
		identity.Package = entry.Package
	}
	identity.Activation = ""
	i := &instance{engine: e, entry: entry, definition: definition, identity: identity, desired: 1, config: config, enabled: entry.Enabled, pending: &revisionResult{done: make(chan struct{})}}
	i.parent = e.instances[entry.Parent]
	e.stateLocked(i, contract.Waiting)
	if !entry.Enabled {
		e.stateLocked(i, contract.Stopped)
	}
	e.instances[entry.ID] = i
	e.signalLocked()
	return i, nil
}

func normalizeConfig(config json.RawMessage) json.RawMessage {
	if len(config) == 0 {
		return json.RawMessage(`{}`)
	}
	return slices.Clone(config)
}

func validate(def contract.Definition, config json.RawMessage) error {
	if err := contract.CheckJSON(config, 32, 10000); err != nil {
		return fmt.Errorf("validate configuration: %w", err)
	}
	if def.Validate != nil {
		if err := def.Validate(config); err != nil {
			return fmt.Errorf("validate configuration: %w", err)
		}
	}
	return nil
}

func (e *Engine) resolveLocked(i *instance, requirement contract.Requirement) *provider {
	p := e.visibleProviderLocked(i, requirement.Key())
	if p != nil && requirement.Accepts(p.service) {
		return p
	}
	return nil
}

func (e *Engine) visibleProviderLocked(i *instance, key string) *provider {
	p := e.services[serviceAddress(i, key)]
	if p != nil && !p.owner.retiring {
		return p
	}
	return nil
}
func (e *Engine) signalLocked() {
	close(e.changed)
	e.changed = make(chan struct{})
	select {
	case e.wake <- struct{}{}:
	default:
	}
}
func (e *Engine) coordinate() {
	defer close(e.done)
	for range e.wake {
		e.mu.Lock()
		e.detectWaitingCyclesLocked()
		e.rebindLocked()
		for _, i := range e.instances {
			if i.activation != nil || !i.enabled || i.state == contract.Failed || e.closed {
				continue
			}
			if i.entry.Parent != "" {
				p := e.instances[i.entry.Parent]
				if p.activation == nil || !p.activation.ready || p.activation.retiring {
					continue
				}
			}
			bindings := map[string]*provider{}
			ready := true
			for _, req := range i.definition.Requires {
				p := e.resolveLocked(i, req)
				if p == nil || !p.owner.ready {
					ready = false
					break
				}
				bindings[req.Key()] = p
			}
			if !ready {
				e.stateLocked(i, contract.Waiting)
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			identity := i.identity
			identity.Activation = contract.NewID()
			a := &activation{instance: i, identity: identity, revision: i.desired, config: slices.Clone(i.config), ctx: ctx, cancel: cancel, publishDone: make(chan struct{}), retireNotified: make(chan struct{}), done: make(chan struct{}), gate: make(chan struct{}, 1), bindings: bindings, provided: map[string]*provider{}, subscriptions: map[uint64]*subscription{}, startedAt: time.Now()}
			i.activation = a
			i.failure = nil
			e.stateLocked(i, contract.Starting)
			go e.start(a)
		}
		allStopped := e.closed
		if allStopped {
			for _, i := range e.instances {
				if i.activation != nil {
					allStopped = false
					break
				}
			}
		}
		e.signalChangedLocked()
		e.mu.Unlock()
		e.flushDiagnostics()
		if allStopped {
			return
		}
	}
}
func (e *Engine) signalChangedLocked() { close(e.changed); e.changed = make(chan struct{}) }

func invokeStart(a *activation) (err error) {
	ctx, cancel := context.WithCancel(context.WithValue(a.ctx, actorKey{}, a))
	defer cancel()
	defer recoverCallback(ctx, "plugin start", &err)
	return a.instance.definition.Start(ctx, &Scope{activation: a})
}
func (e *Engine) start(a *activation) {
	defer e.flushDiagnostics()
	defer close(a.publishDone)
	a.gate <- struct{}{}
	err := invokeStart(a)
	<-a.gate
	e.mu.Lock()
	a.startFinished = true
	a.outcome.StartupDuration = time.Since(a.startedAt)
	a.startErr = errors.Join(a.forcedErr, err)
	i := a.instance
	if a.retiring {
		e.signalLocked()
		e.mu.Unlock()
		return
	}
	if err == nil {
		for _, declared := range i.definition.Provides {
			p := a.provided[declared.Key()]
			if p == nil || p.service != declared {
				err = contract.Fail(contract.Invalid, "declared service not provided: "+declared.Key())
				break
			}
		}
	}
	if err == nil {
		for key := range a.provided {
			if p := e.services[serviceAddress(i, key)]; p != nil && p.owner != a {
				err = contract.Fail(contract.Conflict, "duplicate provider: "+key)
				break
			}
		}
	}
	if err == nil {
		err = e.checkPublicationCycleLocked(a)
	}
	if err != nil {
		a.startErr = errors.Join(a.forcedErr, err)
		e.retireLocked(a, contract.Failed)
	} else if a.revision != i.desired || !i.enabled || e.closed {
		e.retireLocked(a, contract.Waiting)
	} else {
		for key, p := range a.provided {
			e.services[serviceAddress(i, key)] = p
		}
		for _, sub := range a.subscriptions {
			e.publishSubscriptionLocked(sub)
		}
		a.committed = true
		e.mu.Unlock()
		err = e.publish(a)
		e.mu.Lock()
		if err != nil {
			a.startErr = errors.Join(a.startErr, err)
			e.retireLocked(a, contract.Failed)
		}
		if a.retiring {
			e.signalLocked()
			e.mu.Unlock()
			return
		}
		a.ready = true
		for _, work := range a.managed {
			a.managedWork.Add(1)
			go e.runManaged(a, work)
		}
		a.outcome.StartupDuration = time.Since(a.startedAt)
		e.stateLocked(i, contract.Active)
		i.applied = a.revision
		i.finishRevisionLocked(a.revision, contract.UpdateResult{Revision: a.revision, Applied: true}, nil)
		e.rebindLocked()
	}
	e.signalLocked()
	e.mu.Unlock()
}

// Recheck after both readiness and disposal: a failed, unacknowledged override
// can reveal a provider that became ready while the override was pending.
func (e *Engine) rebindLocked() {
	for _, consumer := range e.instances {
		current := consumer.activation
		if current == nil || current.retiring {
			continue
		}
		for _, req := range consumer.definition.Requires {
			p := e.visibleProviderLocked(consumer, req.Key())
			if p != nil && !p.owner.ready {
				continue
			}
			if p != nil && !req.Accepts(p.service) {
				p = nil
			}
			if p != current.bindings[req.Key()] {
				e.retireLocked(current, contract.Waiting)
				break
			}
		}
	}
}

func (e *Engine) checkPublicationCycleLocked(a *activation) error {
	// Resolve against the proposed snapshot before publishing it. The graph
	// includes ownership, so a parent's new binding cannot depend on its child.
	previous := e.services
	proposed := make(map[serviceKey]*provider, len(previous)+len(a.provided))
	for key, p := range previous {
		proposed[key] = p
	}
	for key, p := range a.provided {
		proposed[serviceAddress(a.instance, key)] = p
	}
	e.services = proposed
	defer func() { e.services = previous }()
	visiting := map[*instance]uint8{}
	var visit func(*instance) bool
	visit = func(i *instance) bool {
		if visiting[i] == 1 {
			return true
		}
		if visiting[i] == 2 {
			return false
		}
		visiting[i] = 1
		if parent := e.instances[i.entry.Parent]; parent != nil && visit(parent) {
			return true
		}
		for _, req := range i.definition.Requires {
			if p := e.resolveLocked(i, req); p != nil && visit(p.owner.instance) {
				return true
			}
		}
		visiting[i] = 2
		return false
	}
	for _, i := range e.instances {
		if i.enabled && i.state != contract.Failed && visit(i) {
			return contract.Fail(contract.Reentrant, "service publication creates an ownership/dependency cycle")
		}
	}
	return nil
}

// retireLocked first revokes and cancels the complete transitive closure. Only
// then may any disposer wait for consumers. Bound cleanup calls remain valid.
func (e *Engine) retireLocked(root *activation, terminal contract.State) {
	if root.retiring {
		if terminal == contract.Stopped || terminal == contract.Failed {
			root.terminal = terminal
		}
		return
	}
	root.retiring = true
	for _, sub := range root.subscriptions {
		e.removeSubscriptionLocked(sub)
	}
	root.terminal = terminal
	e.stateLocked(root.instance, contract.Quiescing)
	root.cancel()
	dependents := []*activation{}
	for _, i := range e.instances {
		a := i.activation
		if a == nil || a == root {
			continue
		}
		dependent := i.entry.Parent == root.instance.entry.ID
		for _, p := range a.bindings {
			if p.owner == root {
				dependent = true
				break
			}
		}
		if dependent {
			dependents = append(dependents, a)
			e.retireLocked(a, contract.Waiting)
		}
	}
	go e.dispose(root, dependents)
}
func invokeCleanup(fn contract.Cleanup, ctx context.Context) (err error) {
	defer recoverCallback(ctx, "cleanup", &err)
	return fn(ctx)
}
func (e *Engine) dispose(a *activation, dependents []*activation) {
	defer e.flushDiagnostics()
	e.flushDiagnostics()
	e.notifyRetire(a)
	// Open the per-scope barrier before the global hook, which may remove and
	// synchronously wait for children that need this provider for cleanup.
	close(a.retireNotified)
	e.mu.Lock()
	var notifications []<-chan struct{}
	for _, p := range a.bindings {
		if p.owner.retiring {
			notifications = append(notifications, p.owner.retireNotified)
		}
	}
	if parent := e.instances[a.instance.entry.Parent]; parent != nil && parent.activation != nil && parent.activation.retiring {
		notifications = append(notifications, parent.activation.retireNotified)
	}
	e.mu.Unlock()
	for _, notified := range notifications {
		<-notified
	}
	var errs []error
	if e.hooks.OnRetire != nil {
		if err := invokeRetire(e.hooks.OnRetire, a.identity); err != nil {
			errs = append(errs, err)
		}
	}
	<-a.publishDone
	a.retireWork.Wait()
	if a.retireErr != nil {
		errs = append(errs, a.retireErr)
	}
	for _, child := range dependents {
		<-child.done
	}
	a.work.Wait()
	a.managedWork.Wait()
	// Cleanup is per activation: a stuck builtin must not exhaust a global pool.
	// Go cannot forcibly stop it; its ownership/dependency tree remains pending.
	cleanupCtx := context.WithValue(context.Background(), cleanupKey{}, a)
	cleanupCtx = context.WithValue(cleanupCtx, actorKey{}, a)
	for _, child := range dependents {
		if child.cleanupErr != nil {
			errs = append(errs, child.cleanupErr)
		}
	}
	for n := len(a.cleanups) - 1; n >= 0; n-- {
		if err := invokeCleanup(a.cleanups[n], cleanupCtx); err != nil {
			errs = append(errs, fmt.Errorf("cleanup %s[%d]: %w", a.identity.Entry, n, err))
		}
	}
	e.mu.Lock()
	outcome := a.outcome.Clone()
	if a.startErr != nil {
		outcome.FailureCode = failureCode(a.startErr)
	}
	if cleanupErr := errors.Join(errs...); cleanupErr != nil {
		outcome.CleanupCode = failureCode(cleanupErr)
	}
	if outcome.Reason == "" && outcome.FailureCode == "" {
		outcome.Reason = contract.ExitStopped
	}
	e.mu.Unlock()
	if e.hooks.OnDisposed != nil {
		if err := e.hooks.OnDisposed(a.identity, outcome); err != nil {
			errs = append(errs, err)
			outcome.CleanupCode = failureCode(errors.Join(errs...))
		}
	}
	e.mu.Lock()
	if a.startErr != nil && a.terminal == contract.Failed {
		errs = append(errs, a.startErr)
	}
	for key, p := range a.provided {
		address := serviceAddress(a.instance, key)
		if e.services[address] == p {
			delete(e.services, address)
		}
	}
	a.cleanupErr = errors.Join(errs...)
	i := a.instance
	if i.activation == a {
		i.activation = nil
		i.identity = a.identity
		i.outcome = outcome
		state := a.terminal
		i.failure = a.cleanupErr
		if !i.enabled || e.closed {
			state = contract.Stopped
		}
		if a.cleanupErr != nil && state != contract.Stopped {
			state = contract.Failed
		}
		e.stateLocked(i, state)
		if i.state == contract.Failed {
			i.finishRevisionLocked(i.desired, contract.UpdateResult{Revision: i.desired}, a.cleanupErr)
		}
	}
	delete(e.edges, a)
	delete(e.instanceEdges, a)
	for _, edges := range e.edges {
		delete(edges, a)
	}
	close(a.done)
	e.signalLocked()
	e.mu.Unlock()
}

func (e *Engine) Close(ctx context.Context) error {
	defer e.flushDiagnostics()
	e.configMu.Lock()
	e.mu.Lock()
	e.closed = true
	for _, i := range e.instances {
		i.enabled = false
		i.finishRevisionLocked(i.desired, contract.UpdateResult{Revision: i.desired}, contract.Fail(contract.Canceled, "engine closed"))
		if i.activation != nil {
			e.retireLocked(i.activation, contract.Stopped)
		} else {
			e.stateLocked(i, contract.Stopped)
		}
	}
	e.signalLocked()
	e.mu.Unlock()
	e.configMu.Unlock()
	select {
	case <-ctx.Done():
		return fmt.Errorf("engine close blocked: %w", ctx.Err())
	case <-e.done:
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var errs []error
	for _, i := range e.instances {
		if i.failure != nil {
			errs = append(errs, fmt.Errorf("entry %s: %w", i.entry.ID, i.failure))
		}
	}
	return errors.Join(errs...)
}

func (e *Engine) IsActive(identity contract.Identity) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	i := e.instances[identity.Entry]
	return i != nil && i.activation != nil && i.activation.identity == identity && i.activation.ready && !i.activation.retiring
}

// FailActivation rejects a late failure from an already replaced session.
func (e *Engine) FailActivation(identity contract.Identity, cause error) error {
	defer e.flushDiagnostics()
	if cause == nil {
		return contract.Fail(contract.Invalid, "failure cause is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	i := e.instances[identity.Entry]
	if i == nil || i.activation == nil || i.activation.identity != identity {
		return contract.Fail(contract.NotReady, "activation is no longer current")
	}
	a := i.activation
	a.forcedErr = errors.Join(a.forcedErr, cause)
	a.startErr = errors.Join(a.startErr, cause)
	e.retireLocked(a, contract.Failed)
	e.signalLocked()
	return nil
}

// Remove waits for complete disposal before freeing the persistent entry ID.
// Remove child registrations first; ownership is not silently transferred.
func (e *Engine) Remove(ctx context.Context, entryID string) error {
	e.configMu.Lock()
	e.mu.Lock()
	i := e.instances[entryID]
	if i == nil {
		e.mu.Unlock()
		e.configMu.Unlock()
		return contract.Fail(contract.NotReady, "entry does not exist")
	}
	if i.removing {
		e.mu.Unlock()
		e.configMu.Unlock()
		return contract.Fail(contract.Conflict, "entry removal already in progress")
	}
	for _, child := range e.instances {
		if child.entry.Parent == entryID {
			e.mu.Unlock()
			e.configMu.Unlock()
			return contract.Fail(contract.Conflict, "remove child entries before their parent")
		}
	}
	i.removing = true
	e.mu.Unlock()
	e.configMu.Unlock()
	err := i.Stop(ctx)
	e.mu.Lock()
	defer e.mu.Unlock()
	if i.activation != nil {
		i.removing = false
		return errors.Join(err, contract.Fail(contract.NotReady, "entry cleanup has not finished"))
	}
	delete(e.instances, entryID)
	e.signalLocked()
	return err
}

// Resume submits the enable intent without waiting for dependencies or Start.
func (e *Engine) Resume(entryID string) (contract.Instance, error) {
	defer e.flushDiagnostics()
	e.mu.Lock()
	defer e.mu.Unlock()
	i := e.instances[entryID]
	if i == nil {
		return nil, contract.Fail(contract.NotReady, "entry does not exist")
	}
	if e.closed || i.removing || i.state != contract.Stopped || i.activation != nil {
		return nil, contract.Fail(contract.Conflict, "entry is not available for enabling")
	}
	if i.entry.Parent != "" {
		p := e.instances[i.entry.Parent]
		if p == nil || !p.enabled || p.removing {
			return nil, contract.Fail(contract.NotReady, "parent is stopped")
		}
	}
	i.enabled = true
	i.failure = nil
	e.stateLocked(i, contract.Waiting)
	i.pending = &revisionResult{done: make(chan struct{})}
	e.signalLocked()
	return i, nil
}

func invokeRetire(hook func(contract.Identity), identity contract.Identity) (err error) {
	defer recoverCallback(context.Background(), "retirement hook", &err)
	hook(identity)
	return nil
}

// detectWaitingCyclesLocked diagnoses only definite waits: ambiguous duplicate
// candidates are left for normal provider conflict handling, never guessed.
func (e *Engine) detectWaitingCyclesLocked() {
	graph := map[*instance][]*instance{}
	for _, i := range e.instances {
		if !i.enabled || i.activation != nil || i.state == contract.Failed {
			continue
		}
		if i.entry.Parent != "" {
			p := e.instances[i.entry.Parent]
			if p.activation == nil {
				graph[i] = append(graph[i], p)
			}
		}
		for _, req := range i.definition.Requires {
			if e.resolveLocked(i, req) != nil {
				continue
			}
			var candidates []*instance
			for _, p := range e.instances {
				if !p.enabled || p.state == contract.Failed {
					continue
				}
				for _, service := range p.definition.Provides {
					if req.Accepts(service) && serviceAddress(i, req.Key()) == serviceAddress(p, service.Key()) {
						candidates = append(candidates, p)
						break
					}
				}
			}
			if len(candidates) == 1 && candidates[0].activation == nil {
				graph[i] = append(graph[i], candidates[0])
			}
		}
	}
	visiting := map[*instance]int{}
	var stack []*instance
	var visit func(*instance)
	visit = func(i *instance) {
		if visiting[i] == 2 {
			return
		}
		if visiting[i] == 1 {
			for n, p := range stack {
				if p == i {
					for _, member := range stack[n:] {
						member.failure = contract.Fail(contract.Reentrant, "service dependency activation cycle")
						e.stateLocked(member, contract.Failed)
						member.finishRevisionLocked(member.desired, contract.UpdateResult{Revision: member.desired}, member.failure)
					}
					break
				}
			}
			return
		}
		visiting[i] = 1
		stack = append(stack, i)
		for _, next := range graph[i] {
			visit(next)
		}
		stack = stack[:len(stack)-1]
		visiting[i] = 2
	}
	for i := range graph {
		visit(i)
	}
}
