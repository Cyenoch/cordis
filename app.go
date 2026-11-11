// Package cordis is an in-process composition kernel: plugins form a tree of
// instances, exchange ordinary Go values through typed service keys, dispatch
// typed events, and release their resources when their instance stops.
package cordis

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Options configures a kernel. The zero value is usable.
type Options struct {
	// Logger receives state transitions and failures. It never receives
	// configuration or event payloads. Nil keeps the kernel silent.
	Logger *slog.Logger
	// CapturePanic writes the stack of a recovered plugin panic to Logger.
	CapturePanic bool
}

// App owns one instance tree. Create it with New and release it with Close.
type App struct {
	options Options

	// mu guards the whole graph. Plugin code never runs while it is held.
	mu       sync.Mutex
	changed  chan struct{}
	root     *node
	scope    *Scope // root scope; stays valid as a handle after Close
	nodes    map[uint64]*node
	sequence uint64
	services map[address]*provision
	handlers map[handlerKey][]*handler
	closing  bool
	failures []error
	done     chan struct{}

	notices    []notice
	dispatches bool
}

// New returns a running kernel whose root scope accepts registrations.
func New(options Options) *App {
	a := &App{
		options:  options,
		changed:  make(chan struct{}),
		nodes:    map[uint64]*node{},
		services: map[address]*provision{},
		handlers: map[handlerKey][]*handler{},
		done:     make(chan struct{}),
	}
	a.root = &node{app: a, id: a.nextID(), name: "root", state: Active}
	a.nodes[a.root.id] = a.root
	act := a.root.newActivation("", 0, nil)
	act.active = true
	a.root.act = act
	a.scope = act.scope
	return a
}

// Scope returns the root scope. It belongs to the root instance, which is
// always active and is an ancestor of every other instance.
func (a *App) Scope() *Scope { return a.scope }

// Done is closed once Close has stopped every instance.
func (a *App) Done() <-chan struct{} { return a.done }

// Close stops every instance in ownership and dependency order. It returns
// the cleanup errors of this shutdown joined with the errors of instances that
// were Failed. A canceled ctx only ends the wait; call Close again to resume.
func (a *App) Close(ctx context.Context) error {
	a.mu.Lock()
	a.closing = true
	a.root.stopped = true
	a.root.kickLocked()
	a.mu.Unlock()
	if chain(ctx) != nil {
		// Shutdown is under way, but it has to wait for the calling callback.
		return ErrReentrant
	}
	select {
	case <-a.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return errors.Join(a.failures...)
}

func (a *App) nextID() uint64 {
	a.sequence++
	return a.sequence
}

// broadcastLocked wakes every waiter; they re-check their condition.
func (a *App) broadcastLocked() {
	close(a.changed)
	a.changed = make(chan struct{})
}

// await blocks until done reports true. done runs with mu held.
func (a *App) await(done func() bool) {
	for {
		a.mu.Lock()
		if done() {
			a.mu.Unlock()
			return
		}
		changed := a.changed
		a.mu.Unlock()
		<-changed
	}
}

// awaitContext is await bounded by ctx. It returns with mu released.
func (a *App) awaitContext(ctx context.Context, done func() bool) error {
	for {
		a.mu.Lock()
		if done() {
			a.mu.Unlock()
			return nil
		}
		changed := a.changed
		a.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (a *App) kickAllLocked() {
	for _, n := range a.nodes {
		n.kickLocked()
	}
	a.broadcastLocked()
}

// node is one instance: a plugin applied with a configuration under a scope.
type node struct {
	app       *App
	id        uint64
	name      string
	anonymous bool        // sub-scope or conditional scope; reports as its owner
	owner     *activation // activation that loaded this instance; nil for root
	base      *Scope      // scope it was loaded on
	requires  []requirement
	start     func(context.Context, *Scope, any) error
	validate  func(any) error
	configTyp reflect.Type

	config      any
	revision    uint64
	validated   uint64
	state       State
	err         error
	stopped     bool
	act         *activation
	activations uint64
	driving     bool
	done        chan struct{} // closed at Stopped; nil for anonymous instances
	released    bool          // a sub-scope release has reported its outcome
	conditions  map[int]condition
}

type condition struct {
	provision uint64
	epoch     uint64
	err       error
}

// activation is one run of an instance: its scope, bindings and registrations.
type activation struct {
	node     *node
	scope    *Scope
	ctx      context.Context
	cancel   context.CancelFunc
	epoch    string
	revision uint64
	bound    map[string]*provision

	active   bool // Start returned and the instance was published
	stopping bool // provisions withdrawn, consumers being stopped
	closed   bool // context canceled; no new callbacks or registrations
	ended    bool // cleanup finished

	provisions []*provision
	children   []*node
	cleanups   []*cleanup
	managed    []managed
	inflight   int
	update     func(context.Context, any) (bool, error)
	restart    bool // the update handler declined the pending revision
}

type managed struct {
	act  *activation
	work func(context.Context) error
}

type cleanup struct {
	once sync.Once
	fn   func(context.Context) error
	err  error
}

func (n *node) newActivation(epoch string, revision uint64, bound map[string]*provision) *activation {
	ctx, cancel := context.WithCancel(context.Background())
	act := &activation{node: n, cancel: cancel, epoch: epoch, revision: revision, bound: bound}
	act.ctx = context.WithValue(ctx, chainKey{}, []*node{n})
	act.scope = &Scope{act: act}
	if n.base != nil {
		act.scope.isolation = n.base.isolation
		act.scope.settings = n.base.settings
		act.scope.filter = n.base.filter
	}
	return act
}

// named returns the instance a scope reports as: the nearest non-anonymous one.
func (n *node) named() *node {
	for n.anonymous {
		n = n.owner.node
	}
	return n
}

func (n *node) info() Info {
	n = n.named()
	info := Info{ID: n.id, Name: n.name}
	if n.owner != nil {
		info.Parent = n.owner.node.named().id
	}
	return info
}

// within reports whether n is other or one of its descendants.
func (n *node) within(other *node) bool {
	for ; n != nil; n = n.parent() {
		if n == other {
			return true
		}
	}
	return false
}

func (n *node) parent() *node {
	if n.owner == nil {
		return nil
	}
	return n.owner.node
}

func (n *node) setStateLocked(state State) {
	if n.state == state {
		return
	}
	previous := n.state
	n.state = state
	if !n.anonymous {
		n.app.noticeLocked(notice{instance: &InstanceChange{Instance: n.info(), From: previous, To: state, Err: n.err}})
	}
	n.app.broadcastLocked()
}

// kickLocked makes sure a driver re-evaluates the instance. A start that is
// already obsolete is canceled right away so a blocked Start can return.
func (n *node) kickLocked() {
	if act := n.act; act != nil && !act.active && !act.closed && n.obsoleteLocked(act) {
		act.cancel()
	}
	if n.driving || n.idleLocked() {
		return
	}
	n.driving = true
	go n.drive()
}

// idleLocked reports that a driver would find nothing to do, so that a
// change elsewhere in the graph does not wake every instance.
func (n *node) idleLocked() bool {
	if act := n.act; act != nil {
		if n.obsoleteLocked(act) || act.revision != n.revision || !act.active {
			return false
		}
		for i, req := range n.requires {
			if req.accept == nil {
				continue
			}
			p := act.bound[req.name]
			if cached := n.conditions[i]; p == nil || cached.provision != p.id || cached.epoch != p.epoch {
				return false
			}
		}
		return true
	}
	switch {
	case n.stopped:
		return n.state == Stopped
	case n.err != nil:
		return n.state == Failed
	}
	if n.state != Waiting {
		return false
	}
	for _, req := range n.requires {
		if n.candidateLocked(req) == nil {
			return true
		}
	}
	return false
}

// obsoleteLocked reports whether act must stop, without evaluating conditions.
func (n *node) obsoleteLocked(act *activation) bool {
	if n.stopped || n.err != nil || act.restart || (act.revision != n.revision && (act.update == nil || !act.active)) {
		return true
	}
	for _, p := range act.bound {
		if !n.app.visibleLocked(p) {
			return true
		}
	}
	return false
}

// desiredLocked computes the binding the instance should run with. ok is
// false when it cannot run. pending lists conditions that must be evaluated
// outside the lock before the answer is known.
func (n *node) desiredLocked() (epoch string, bound map[string]*provision, ok bool, pending []int) {
	if n.stopped || n.err != nil {
		return "", nil, false, nil
	}
	bound = map[string]*provision{}
	var ids []string
	ok = true
	for i, req := range n.requires {
		p := n.candidateLocked(req)
		if p == nil {
			ok = false
			continue
		}
		if req.typ != nil && p.typ != nil && req.typ != p.typ {
			n.err = &typeError{name: req.name, held: p.typ, asked: req.typ, instance: n.name}
			return "", nil, false, nil
		}
		if req.accept != nil {
			cached, known := n.conditions[i]
			if !known || cached.provision != p.id || cached.epoch != p.epoch {
				pending = append(pending, i)
				continue
			}
			if cached.err != nil {
				ok = false
				continue
			}
		}
		bound[req.name] = p
		ids = append(ids, strconv.FormatUint(p.id, 10))
	}
	if len(pending) > 0 || !ok {
		return "", nil, false, pending
	}
	return strings.Join(ids, ":"), bound, true, nil
}

// candidateLocked finds the visible provider for a requirement in the
// instance's resolution view. A provider inside the instance itself never
// counts: it could not outlive its own consumer.
func (n *node) candidateLocked(req requirement) *provision {
	p := n.app.services[address{realm: realmOf(n.base, req.name), name: req.name}]
	if p == nil || !n.app.visibleLocked(p) || p.owner.node.within(n) {
		return nil
	}
	return p
}

// evaluate runs pending acceptance conditions without the lock held.
func (n *node) evaluate(pending []int) {
	a := n.app
	type job struct {
		index int
		p     *provision
		epoch uint64
	}
	var jobs []job
	a.mu.Lock()
	for _, i := range pending {
		if p := n.candidateLocked(n.requires[i]); p != nil {
			jobs = append(jobs, job{i, p, p.epoch})
		}
	}
	a.mu.Unlock()
	for _, j := range jobs {
		req := n.requires[j.index]
		err := a.guard("condition", n.name, func() error { return req.accept(j.p.value) })
		a.mu.Lock()
		if n.conditions == nil {
			n.conditions = map[int]condition{}
		}
		n.conditions[j.index] = condition{provision: j.p.id, epoch: j.epoch, err: err}
		a.mu.Unlock()
	}
}

// drive converges the instance on its desired state. At most one driver runs
// per instance, so starts, stops and in-place updates never overlap.
func (n *node) drive() {
	a := n.app
	a.mu.Lock()
	for {
		epoch, bound, ok, pending := n.desiredLocked()
		if len(pending) > 0 {
			a.mu.Unlock()
			n.evaluate(pending)
			a.mu.Lock()
			continue
		}
		if act := n.act; act != nil {
			if !ok || epoch != act.epoch || act.restart || (act.revision != n.revision && act.update == nil) {
				a.mu.Unlock()
				n.stop(act)
				a.mu.Lock()
				continue
			}
			if act.revision != n.revision {
				n.applyLocked(act)
				continue
			}
			break
		}
		if n.stopped {
			n.finishLocked()
			break
		}
		if !ok {
			if n.err != nil {
				n.setStateLocked(Failed)
				// A conditional scope has no handle of its own; its failure
				// is the failure of the instance that declared it.
				if owner := n.named(); n.anonymous && owner.err == nil && owner.act != nil {
					owner.err = n.err
					owner.kickLocked()
				}
			} else {
				n.setStateLocked(Waiting)
			}
			break
		}
		n.launchLocked(epoch, bound)
	}
	n.driving = false
	a.broadcastLocked()
	a.mu.Unlock()
}

// applyLocked hands a new configuration to the instance's update handler.
func (n *node) applyLocked(act *activation) {
	a := n.app
	revision, config, handler := n.revision, n.config, act.update
	a.mu.Unlock()
	var applied bool
	err := a.guard("update", n.name, func() (err error) {
		applied, err = handler(act.ctx, config)
		return err
	})
	a.mu.Lock()
	switch {
	case n.revision != revision:
		// A newer revision arrived meanwhile; this outcome no longer counts.
	case err != nil:
		if n.err == nil {
			n.err = err
		}
	case applied:
		act.revision = revision
	default:
		act.restart = true
	}
	a.broadcastLocked()
}

// launchLocked validates the configuration and runs Start.
func (n *node) launchLocked(epoch string, bound map[string]*provision) {
	a := n.app
	revision, config := n.revision, n.config
	if n.validate != nil && n.validated != revision {
		a.mu.Unlock()
		err := a.guard("validation", n.name, func() error { return n.validate(config) })
		a.mu.Lock()
		if n.revision != revision {
			return
		}
		if err != nil {
			n.err = err
			return
		}
		n.validated = revision
		return // bindings may have moved while unlocked; recompute
	}
	act := n.newActivation(epoch, revision, bound)
	for _, p := range bound {
		p.dependents[act] = struct{}{}
	}
	n.act = act
	n.activations++
	n.setStateLocked(Starting)
	if n.start != nil {
		a.mu.Unlock()
		err := a.guard("start", n.name, func() error { return n.start(act.ctx, act.scope, config) })
		a.mu.Lock()
		// The outcome of a start that has become obsolete must not override
		// the newer target: neither its error nor its success counts.
		if err != nil && n.err == nil && !n.obsoleteLocked(act) {
			n.err = err
		}
	}
	if n.obsoleteLocked(act) {
		return // the loop stops it
	}
	act.active = true
	n.setStateLocked(Active)
	for _, m := range act.managed {
		if !m.act.closed {
			a.runLocked(m.act, m.work)
		}
	}
	act.managed = nil
	for _, p := range a.provisionsLocked(act) {
		a.announceLocked(p, true)
	}
	a.kickAllLocked()
}

// stop takes one activation down. Order matters: consumers and children
// finish while this instance still works, then its own callbacks drain, then
// its cleanups run.
func (n *node) stop(act *activation) {
	a := n.app
	a.mu.Lock()
	act.stopping = true
	n.setStateLocked(Quiescing)
	provisions := a.provisionsLocked(act)
	for _, p := range provisions {
		a.announceLocked(p, false)
	}
	a.kickAllLocked()
	a.mu.Unlock()

	var childErrs []error // cleanup errors of sub-scopes, reported by their owner
	a.await(func() bool {
		for _, p := range provisions {
			if len(p.dependents) > 0 {
				return false
			}
		}
		return true
	})

	for {
		a.mu.Lock()
		if len(act.children) == 0 {
			// Closed in the same critical section, so nothing can be loaded
			// between the last child stopping and the scope closing.
			act.closed = true
			act.managed = nil
			a.mu.Unlock()
			break
		}
		child := act.children[len(act.children)-1]
		child.stopped = true
		child.kickLocked()
		a.mu.Unlock()
		a.await(func() bool { return child.state == Stopped })
		if child.anonymous && child.err != nil && !errors.Is(n.err, child.err) {
			childErrs = append(childErrs, child.err)
		}
	}
	act.cancel()
	a.await(func() bool { return act.inflight == 0 })

	errs := childErrs
	cleanupCtx := context.WithoutCancel(act.ctx)
	for {
		a.mu.Lock()
		if len(act.cleanups) == 0 {
			act.ended = true
			a.mu.Unlock()
			break
		}
		last := act.cleanups[len(act.cleanups)-1]
		act.cleanups = act.cleanups[:len(act.cleanups)-1]
		a.mu.Unlock()
		if err := last.run(cleanupCtx, a, n.name); err != nil {
			errs = append(errs, err)
		}
	}

	a.mu.Lock()
	for _, p := range act.provisions {
		delete(a.services, address{realm: p.realm, name: p.name})
	}
	for key, list := range a.handlers {
		list = slices.DeleteFunc(list, func(h *handler) bool { return h.act == act })
		if len(list) == 0 {
			delete(a.handlers, key)
		} else {
			a.handlers[key] = list
		}
	}
	for _, p := range act.bound {
		delete(p.dependents, act)
	}
	n.act = nil
	if err := errors.Join(errs...); err != nil {
		n.err = errors.Join(n.err, err)
	}
	a.kickAllLocked()
	a.mu.Unlock()
}

func (c *cleanup) run(ctx context.Context, a *App, name string) error {
	c.once.Do(func() {
		c.err = a.guard("cleanup", name, func() error { return c.fn(ctx) })
	})
	return c.err
}

// finishLocked records the terminal state of a stopped instance.
func (n *node) finishLocked() {
	a := n.app
	if n.state == Stopped {
		return
	}
	n.setStateLocked(Stopped)
	if n.done != nil {
		close(n.done)
	}
	delete(a.nodes, n.id)
	if n.owner != nil {
		n.owner.children = slices.DeleteFunc(n.owner.children, func(c *node) bool { return c == n })
	}
	if a.closing && n.err != nil {
		a.failures = append(a.failures, n.err)
	}
	if n == a.root {
		a.finishWhenIdleLocked()
	}
	a.broadcastLocked()
}

// provisionsLocked lists what an activation publishes, including the
// publications of its sub-scopes, which become visible and invisible with it.
func (a *App) provisionsLocked(act *activation) []*provision {
	list := slices.Clone(act.provisions)
	for _, child := range act.children {
		if child.anonymous && child.act != nil {
			list = append(list, a.provisionsLocked(child.act)...)
		}
	}
	return list
}

// visibleLocked reports whether other instances may bind p: its owner, and
// every sub-scope between it and the owning plugin instance, must be active
// and not stopping.
func (a *App) visibleLocked(p *provision) bool {
	for act := p.owner; ; act = act.node.owner {
		if !act.active || act.stopping {
			return false
		}
		if !act.node.anonymous {
			return true
		}
	}
}

// runLocked starts managed work as an in-flight callback of act.
func (a *App) runLocked(act *activation, work func(context.Context) error) {
	act.inflight++
	go func() {
		n := act.node
		err := a.guard("managed work", n.name, func() error { return work(act.ctx) })
		a.mu.Lock()
		act.inflight--
		// What work returns after its context was canceled is the result of
		// stopping, not a failure of the instance.
		failed := err != nil && act.ctx.Err() == nil
		owner := n.named()
		if failed && owner != a.root && owner.err == nil && owner.act != nil {
			owner.err = err
			owner.kickLocked()
		}
		a.broadcastLocked()
		a.mu.Unlock()
		if failed && owner == a.root && a.options.Logger != nil {
			// The root instance has no plugin to fail; report and carry on.
			a.options.Logger.Error("work on the root scope failed", "error", err)
		}
	}()
}

type chainKey struct{}

// chain lists the instances whose callbacks are on the current call stack.
func chain(ctx context.Context) []*node {
	nodes, _ := ctx.Value(chainKey{}).([]*node)
	return nodes
}

// blockedLocked reports whether waiting for target to stop or restart from
// ctx would wait on the caller itself: the caller runs inside target, inside
// one of its descendants, or inside a consumer of either.
func (a *App) blockedLocked(ctx context.Context, target *node) bool {
	callers := chain(ctx)
	if len(callers) == 0 {
		return false
	}
	seen := map[*node]bool{}
	var reach func(*node) bool
	reach = func(n *node) bool {
		if seen[n] {
			return false
		}
		seen[n] = true
		for _, caller := range callers {
			if caller.within(n) {
				return true
			}
		}
		if n.act == nil {
			return false
		}
		for _, p := range a.provisionsLocked(n.act) {
			for dependent := range p.dependents {
				if reach(dependent.node) {
					return true
				}
			}
		}
		for _, child := range n.act.children {
			if reach(child) {
				return true
			}
		}
		return false
	}
	return reach(target)
}
