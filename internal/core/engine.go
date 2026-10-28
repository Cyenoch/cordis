// Package core coordinates activation snapshots and their owned resources.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/Cyenoch/cordis/contract"
)

type realm struct {
	parent   string
	isolated map[string]bool
}
type provider struct {
	service contract.Service
	handler contract.Handler
	owner   *activation
}
type subscription struct {
	name    string
	handler contract.EventHandler
	order   uint64
	owner   *activation
}
type revisionResult struct {
	done     chan struct{}
	result   contract.UpdateResult
	err      error
	finished bool
}

// Hooks prepare external activation-owned records. Readers of those records must
// gate visibility with IsActive; hooks alone cannot make separate registries atomic.
type Hooks struct {
	BeforePublish func(contract.Identity) error
	OnRetire      func(contract.Identity)
}

// Engine owns the registry, dependency graph, and all instance transitions.
// Its mutex never protects execution of plugin code.
type Engine struct {
	mu            sync.Mutex
	configMu      sync.Mutex
	realms        map[string]realm
	instances     map[string]*instance
	services      map[string]map[string]*provider
	events        []*subscription
	edges         map[*activation]map[*activation]int
	instanceEdges map[*activation]map[*instance]int
	changed       chan struct{}
	wake          chan struct{}
	done          chan struct{}
	closed        bool
	sequence      uint64
	hooks         Hooks
}

type instance struct {
	engine      *Engine
	entry       contract.Entry
	definition  contract.Definition
	identity    contract.Identity
	desired     uint64
	applied     uint64
	config      json.RawMessage
	state       contract.State
	failure     error
	enabled     bool
	removing    bool
	supervising bool
	activation  *activation
	revisions   map[uint64]*revisionResult
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
	prepareDone    chan struct{}
	publishDone    chan struct{}
	retireNotified chan struct{}
	bindings       map[string]*provider
	provided       map[string]*provider
	subscriptions  []*subscription
	cleanups       []contract.Cleanup
	retiring       bool
	committed      bool
	ready          bool
	terminal       contract.State
	startErr       error
	forcedErr      error
	cleanupErr     error
}

func New() *Engine { return NewWithHooks(Hooks{}) }

func NewWithHooks(hooks Hooks) *Engine {
	e := &Engine{realms: map[string]realm{"": {}}, instances: map[string]*instance{}, services: map[string]map[string]*provider{}, edges: map[*activation]map[*activation]int{}, instanceEdges: map[*activation]map[*instance]int{}, changed: make(chan struct{}), wake: make(chan struct{}, 1), done: make(chan struct{}), hooks: hooks}
	go e.coordinate()
	return e
}

// ConfigureRealm creates an explicit service namespace. Isolated keys do not
// inherit a provider from ancestors; other keys permit local overrides.
func (e *Engine) ConfigureRealm(name, parent string, isolated []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return contract.Fail(contract.NotReady, "engine is closed")
	}
	if name == "" {
		return contract.Fail(contract.Conflict, "root realm already exists")
	}
	if _, ok := e.realms[name]; ok {
		return contract.Fail(contract.Conflict, "realm already exists")
	}
	if _, ok := e.realms[parent]; !ok {
		return contract.Fail(contract.Invalid, "unknown parent realm")
	}
	keys := make(map[string]bool, len(isolated))
	for _, key := range isolated {
		keys[key] = true
	}
	e.realms[name] = realm{parent: parent, isolated: keys}
	return nil
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
	identity.Entry = entry.ID
	if identity.Package == "" {
		identity.Package = entry.Package
	}
	identity.Activation = ""
	i := &instance{engine: e, entry: entry, definition: definition, identity: identity, desired: 1, config: config, state: contract.Waiting, enabled: entry.Enabled, revisions: map[uint64]*revisionResult{1: {done: make(chan struct{})}}}
	if !entry.Enabled {
		i.state = contract.Stopped
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

func (e *Engine) descendantLocked(child, parent string) bool {
	for {
		if child == parent {
			return true
		}
		if child == "" {
			return false
		}
		child = e.realms[child].parent
	}
}
func (e *Engine) resolveLocked(namespace string, requirement contract.Requirement) *provider {
	p := e.visibleProviderLocked(namespace, requirement.Key())
	if p != nil && requirement.Accepts(p.service) {
		return p
	}
	return nil
}

func (e *Engine) visibleProviderLocked(namespace, key string) *provider {
	for {
		if p := e.services[namespace][key]; p != nil {
			if !p.owner.retiring {
				return p
			}
			return nil
		}
		r := e.realms[namespace]
		if namespace == "" || r.isolated[key] {
			return nil
		}
		namespace = r.parent
	}
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
				p := e.resolveLocked(i.entry.Realm, req)
				if p == nil || !p.owner.ready {
					ready = false
					break
				}
				bindings[req.Key()] = p
			}
			if !ready {
				i.state = contract.Waiting
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			identity := i.identity
			identity.Activation = contract.NewID()
			a := &activation{instance: i, identity: identity, revision: i.desired, config: slices.Clone(i.config), ctx: ctx, cancel: cancel, publishDone: make(chan struct{}), retireNotified: make(chan struct{}), done: make(chan struct{}), gate: make(chan struct{}, 1), bindings: bindings, provided: map[string]*provider{}}
			i.activation = a
			i.state = contract.Starting
			i.failure = nil
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
		if allStopped {
			return
		}
	}
}
func (e *Engine) signalChangedLocked() { close(e.changed); e.changed = make(chan struct{}) }

func invokeStart(a *activation) (err error) {
	ctx, cancel := context.WithCancel(context.WithValue(a.ctx, actorKey{}, a))
	defer cancel()
	defer recoverCallback("plugin start", &err)
	return a.instance.definition.Start(ctx, &Scope{activation: a})
}
func (e *Engine) start(a *activation) {
	defer close(a.publishDone)
	a.gate <- struct{}{}
	err := invokeStart(a)
	<-a.gate
	e.mu.Lock()
	a.startFinished = true
	retiring := a.retiring
	if !retiring {
		a.prepareDone = make(chan struct{})
	}
	e.mu.Unlock()
	if !retiring && err == nil && e.hooks.BeforePublish != nil {
		err = invokePublish(e.hooks.BeforePublish, a.identity)
	}
	if !retiring {
		close(a.prepareDone)
	}
	e.mu.Lock()
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
			if p := e.services[i.entry.Realm][key]; p != nil && p.owner != a {
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
		if e.services[i.entry.Realm] == nil {
			e.services[i.entry.Realm] = map[string]*provider{}
		}
		for key, p := range a.provided {
			e.services[i.entry.Realm][key] = p
		}
		e.events = append(e.events, a.subscriptions...)
		slices.SortFunc(e.events, func(a, b *subscription) int {
			if a.order < b.order {
				return -1
			}
			if a.order > b.order {
				return 1
			}
			return 0
		})
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
		i.state = contract.Active
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
			p := e.visibleProviderLocked(consumer.entry.Realm, req.Key())
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
	namespace := a.instance.entry.Realm
	previous := e.services[namespace]
	proposed := make(map[string]*provider, len(previous)+len(a.provided))
	for key, p := range previous {
		proposed[key] = p
	}
	for key, p := range a.provided {
		proposed[key] = p
	}
	e.services[namespace] = proposed
	defer func() { e.services[namespace] = previous }()
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
			if p := e.resolveLocked(i.entry.Realm, req); p != nil && visit(p.owner.instance) {
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
	root.terminal = terminal
	root.instance.state = contract.Quiescing
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
	defer recoverCallback("cleanup", &err)
	return fn(ctx)
}
func (e *Engine) dispose(a *activation, dependents []*activation) {
	e.notifyRetire(a)
	// Open the per-scope barrier before the global hook, which may remove and
	// synchronously wait for children that need this provider for cleanup.
	close(a.retireNotified)
	e.mu.Lock()
	prepared := a.prepareDone
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
	if prepared != nil {
		<-prepared
	}
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
	if a.startErr != nil && a.terminal == contract.Failed {
		errs = append(errs, a.startErr)
	}
	for key, p := range a.provided {
		if e.services[a.instance.entry.Realm][key] == p {
			delete(e.services[a.instance.entry.Realm], key)
		}
	}
	e.events = slices.DeleteFunc(e.events, func(s *subscription) bool { return s.owner == a })
	a.cleanupErr = errors.Join(errs...)
	i := a.instance
	if i.activation == a {
		i.activation = nil
		i.state = a.terminal
		i.failure = a.cleanupErr
		if !i.enabled || e.closed {
			i.state = contract.Stopped
		}
		if a.cleanupErr != nil && i.state != contract.Stopped {
			i.state = contract.Failed
		}
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
	e.configMu.Lock()
	e.mu.Lock()
	e.closed = true
	for _, i := range e.instances {
		i.enabled = false
		for revision, pending := range i.revisions {
			if !pending.finished {
				i.finishRevisionLocked(revision, contract.UpdateResult{Revision: revision}, contract.Fail(contract.Canceled, "engine closed"))
			}
		}
		if i.activation != nil {
			e.retireLocked(i.activation, contract.Stopped)
		} else {
			i.state = contract.Stopped
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

func invokePublish(hook func(contract.Identity) error, identity contract.Identity) (err error) {
	defer recoverCallback("publication hook", &err)
	return hook(identity)
}

func (e *Engine) IsActive(identity contract.Identity) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	i := e.instances[identity.Entry]
	return i != nil && i.activation != nil && i.activation.identity == identity && i.activation.ready && !i.activation.retiring
}

// FailActivation rejects a late failure from an already replaced session.
func (e *Engine) FailActivation(identity contract.Identity, cause error) error {
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
	i.state = contract.Waiting
	i.failure = nil
	i.revisions[i.desired] = &revisionResult{done: make(chan struct{})}
	e.signalLocked()
	return i, nil
}

func invokeRetire(hook func(contract.Identity), identity contract.Identity) (err error) {
	defer recoverCallback("retirement hook", &err)
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
			if e.resolveLocked(i.entry.Realm, req) != nil {
				continue
			}
			var candidates []*instance
			namespace := i.entry.Realm
			for {
				for _, p := range e.instances {
					if !p.enabled || p.state == contract.Failed || p.entry.Realm != namespace {
						continue
					}
					for _, service := range p.definition.Provides {
						if req.Accepts(service) {
							candidates = append(candidates, p)
							break
						}
					}
				}
				if len(candidates) > 0 || namespace == "" || e.realms[namespace].isolated[req.Key()] {
					break
				}
				namespace = e.realms[namespace].parent
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
						member.state = contract.Failed
						member.failure = contract.Fail(contract.Reentrant, "service dependency activation cycle")
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
