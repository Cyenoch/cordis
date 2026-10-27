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
	mu           sync.Mutex
	configMu     sync.Mutex
	realms       map[string]realm
	instances    map[string]*instance
	services     map[string]map[string]*provider
	events       []*subscription
	edges        map[*activation]map[*activation]int
	changed      chan struct{}
	wake         chan struct{}
	done         chan struct{}
	closed       bool
	sequence     uint64
	hooks        Hooks
	cleanupSlots chan struct{}
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
	instance      *instance
	identity      contract.Identity
	revision      uint64
	config        json.RawMessage
	ctx           context.Context
	cancel        context.CancelFunc
	startDone     chan struct{}
	done          chan struct{}
	gate          chan struct{}
	work          sync.WaitGroup
	prepareDone   chan struct{}
	bindings      map[string]*provider
	provided      map[string]*provider
	subscriptions []*subscription
	cleanups      []contract.Cleanup
	retiring      bool
	committed     bool
	terminal      contract.State
	startErr      error
	forcedErr     error
	cleanupErr    error
}

func New() *Engine { return NewWithHooks(Hooks{}) }

func NewWithHooks(hooks Hooks) *Engine {
	e := &Engine{realms: map[string]realm{"": {}}, instances: map[string]*instance{}, services: map[string]map[string]*provider{}, edges: map[*activation]map[*activation]int{}, changed: make(chan struct{}), wake: make(chan struct{}, 1), done: make(chan struct{}), hooks: hooks, cleanupSlots: make(chan struct{}, 8)}
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
	if entry.ID == "" || definition.Start == nil {
		return nil, contract.Fail(contract.Invalid, "entry id and start callback are required")
	}
	if entry.Definition != "" && entry.Definition != definition.ID {
		return nil, contract.Fail(contract.Invalid, "entry definition mismatch")
	}
	config := slices.Clone(entry.Config)
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	if err := validate(definition, config); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, contract.Fail(contract.NotReady, "engine is closed")
	}
	if _, ok := e.instances[entry.ID]; ok {
		return nil, contract.Fail(contract.Conflict, "entry already exists")
	}
	if _, ok := e.realms[entry.Realm]; !ok {
		return nil, contract.Fail(contract.Invalid, "unknown realm")
	}
	if entry.Parent != "" {
		parent, ok := e.instances[entry.Parent]
		if !ok || !parent.enabled || parent.removing || !e.descendantLocked(entry.Realm, parent.entry.Realm) {
			return nil, contract.Fail(contract.Invalid, "parent is unavailable or outside realm ancestry")
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
	for {
		if p := e.services[namespace][requirement.Key()]; p != nil {
			if !p.owner.retiring && requirement.Accepts(p.service) {
				return p
			}
			return nil
		}
		r := e.realms[namespace]
		if namespace == "" || r.isolated[requirement.Key()] {
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
		for _, i := range e.instances {
			if i.activation != nil || !i.enabled || i.state == contract.Failed || e.closed {
				continue
			}
			if i.entry.Parent != "" {
				p := e.instances[i.entry.Parent]
				if p.activation == nil || !p.activation.committed || p.activation.retiring {
					continue
				}
			}
			bindings := map[string]*provider{}
			ready := true
			for _, req := range i.definition.Requires {
				p := e.resolveLocked(i.entry.Realm, req)
				if p == nil {
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
			a := &activation{instance: i, identity: identity, revision: i.desired, config: slices.Clone(i.config), ctx: ctx, cancel: cancel, startDone: make(chan struct{}), done: make(chan struct{}), gate: make(chan struct{}, 1), bindings: bindings, provided: map[string]*provider{}}
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
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("plugin start panic: %v", v)
		}
	}()
	return a.instance.definition.Start(context.WithValue(a.ctx, actorKey{}, a), &Scope{activation: a})
}
func (e *Engine) start(a *activation) {
	a.gate <- struct{}{}
	err := invokeStart(a)
	<-a.gate
	e.mu.Lock()
	retiring := a.retiring
	if !retiring {
		a.prepareDone = make(chan struct{})
	}
	e.mu.Unlock()
	if !retiring {
		if err == nil && e.hooks.BeforePublish != nil {
			err = invokePublish(e.hooks.BeforePublish, a.identity)
		}
		close(a.prepareDone)
	}
	e.mu.Lock()
	a.startErr = errors.Join(a.forcedErr, err)
	close(a.startDone)
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
	if err != nil {
		a.startErr = err
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
		i.state = contract.Active
		i.applied = a.revision
		i.finishRevisionLocked(a.revision, contract.UpdateResult{Revision: a.revision, Applied: true}, nil)
		// Only consumers whose effective binding changed retire on a local override.
		for _, consumer := range e.instances {
			current := consumer.activation
			if current == nil || current == a || current.retiring {
				continue
			}
			for _, req := range consumer.definition.Requires {
				if e.resolveLocked(consumer.entry.Realm, req) != current.bindings[req.Key()] {
					e.retireLocked(current, contract.Waiting)
					break
				}
			}
		}
	}
	e.signalLocked()
	e.mu.Unlock()
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
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("cleanup panic: %v", v)
		}
	}()
	return fn(ctx)
}
func (e *Engine) dispose(a *activation, dependents []*activation) {
	e.mu.Lock()
	prepared := a.prepareDone
	e.mu.Unlock()
	if prepared != nil {
		<-prepared
	}
	var hookErr error
	if e.hooks.OnRetire != nil {
		hookErr = invokeRetire(e.hooks.OnRetire, a.identity)
	}
	<-a.startDone
	for _, child := range dependents {
		<-child.done
	}
	a.work.Wait()
	e.cleanupSlots <- struct{}{}
	defer func() { <-e.cleanupSlots }()
	cleanupCtx := context.WithValue(context.Background(), cleanupKey{}, a)
	cleanupCtx = context.WithValue(cleanupCtx, actorKey{}, a)
	var errs []error
	if hookErr != nil {
		errs = append(errs, hookErr)
	}
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
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("publication hook panic: %v", value)
		}
	}()
	return hook(identity)
}

func (e *Engine) IsActive(identity contract.Identity) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	i := e.instances[identity.Entry]
	return i != nil && i.activation != nil && i.activation.identity == identity && i.activation.committed && !i.activation.retiring
}

// Fail reports an activation/session failure without automatically retrying it.
func (e *Engine) Fail(entryID string, cause error) error {
	if cause == nil {
		return contract.Fail(contract.Invalid, "failure cause is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	i := e.instances[entryID]
	if i == nil {
		return contract.Fail(contract.Invalid, "unknown entry")
	}
	if i.activation == nil {
		i.state = contract.Failed
		i.failure = cause
		i.finishRevisionLocked(i.desired, contract.UpdateResult{Revision: i.desired}, cause)
	} else {
		i.activation.forcedErr = errors.Join(i.activation.forcedErr, cause)
		i.activation.startErr = errors.Join(i.activation.startErr, cause)
		e.retireLocked(i.activation, contract.Failed)
	}
	e.signalLocked()
	return nil
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

func (e *Engine) AddChild(parent string, entry contract.Entry, definition contract.Definition, identity contract.Identity) (contract.Instance, error) {
	entry.Parent = parent
	return e.Add(entry, definition, identity)
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
		return contract.Fail(contract.Invalid, "unknown entry")
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

// Enable starts a previously stopped entry. Failed entries require Retry.
func (e *Engine) Enable(ctx context.Context, entryID string) error {
	e.mu.Lock()
	i := e.instances[entryID]
	if i == nil {
		e.mu.Unlock()
		return contract.Fail(contract.Invalid, "unknown entry")
	}
	if e.closed || i.removing || i.state != contract.Stopped || i.activation != nil {
		e.mu.Unlock()
		return contract.Fail(contract.Conflict, "entry is not available for enabling")
	}
	if i.entry.Parent != "" {
		p := e.instances[i.entry.Parent]
		if p == nil || !p.enabled || p.removing {
			e.mu.Unlock()
			return contract.Fail(contract.NotReady, "parent is stopped")
		}
	}
	i.enabled = true
	i.state = contract.Waiting
	i.failure = nil
	i.revisions[i.desired] = &revisionResult{done: make(chan struct{})}
	e.signalLocked()
	e.mu.Unlock()
	return i.Wait(ctx)
}

func invokeRetire(hook func(contract.Identity), identity contract.Identity) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("retirement hook panic: %v", value)
		}
	}()
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
