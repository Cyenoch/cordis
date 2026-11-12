package cordis

import (
	"context"
	"fmt"
)

// State is the lifecycle state of an instance.
type State int

const (
	Waiting   State = iota // a required service is missing or not accepted
	Starting               // Start is running
	Active                 // Start returned and the instance is published
	Quiescing              // stopping: consumers, children, callbacks, cleanups
	Stopped                // terminal
	Failed                 // start, update or managed work failed; needs Update or Restart
)

// String is the lower-case state name, or State(n) for an unknown value.
func (s State) String() string {
	switch s {
	case Waiting:
		return "waiting"
	case Starting:
		return "starting"
	case Active:
		return "active"
	case Quiescing:
		return "quiescing"
	case Stopped:
		return "stopped"
	case Failed:
		return "failed"
	}
	return fmt.Sprintf("State(%d)", int(s))
}

// Info identifies an instance. Parent is the ID of the instance that loaded
// it; it is zero only for the root instance.
type Info struct {
	ID     uint64
	Name   string
	Parent uint64
}

// Missing explains one requirement that keeps an instance Waiting.
type Missing struct {
	Service string
	Reason  error
}

// Status is a consistent snapshot of an instance.
type Status struct {
	State       State
	Err         error
	Missing     []Missing
	Activations uint64
}

// Scope is where a plugin registers what it owns. Every registration made
// through a scope belongs to the scope's instance and is undone when that
// instance stops. A scope is valid for one activation only.
type Scope struct {
	act       *activation
	isolation *isolation
	settings  *setting
	filter    func(*Scope) bool
}

func (s *Scope) derive() *Scope {
	derived := *s
	return &derived
}

// Info identifies the instance the scope belongs to.
func (s *Scope) Info() Info { return s.act.node.info() }

// Defer registers a cleanup that runs once when the instance stops, in
// reverse registration order. The returned function runs it early. A cleanup
// registered after the activation has finished runs immediately, because the
// resource it releases already exists.
func (s *Scope) Defer(fn func(context.Context) error) (release func(context.Context) error) {
	a := s.act.node.app
	c := &cleanup{fn: fn}
	name := s.act.node.name
	a.mu.Lock()
	if s.act.ended {
		a.mu.Unlock()
		c.run(context.Background(), a, name)
		return func(context.Context) error { return c.err }
	}
	s.act.cleanups = append(s.act.cleanups, c)
	a.mu.Unlock()
	return func(ctx context.Context) error {
		a.mu.Lock()
		for i, registered := range s.act.cleanups {
			if registered == c {
				s.act.cleanups = append(s.act.cleanups[:i], s.act.cleanups[i+1:]...)
				break
			}
		}
		a.mu.Unlock()
		return c.run(context.WithoutCancel(ctx), a, name)
	}
}

// Go runs work in a goroutine owned by the instance. Work registered during
// Start begins once the instance is Active. Its ctx is canceled when the
// instance stops, and the instance waits for it before running cleanups. A
// returned error other than that cancellation fails the instance.
func (s *Scope) Go(work func(context.Context) error) {
	a := s.act.node.app
	a.mu.Lock()
	defer a.mu.Unlock()
	if s.act.closed {
		return
	}
	host := s.act.node.named().act
	if host.active {
		a.runLocked(s.act, work)
		return
	}
	host.managed = append(host.managed, managed{act: s.act, work: work})
}

// Sub derives a scope whose registrations can be released together, before
// the instance stops. Release follows the stop order, waits for it, and
// returns cleanup errors.
func (s *Scope) Sub() (*Scope, func(context.Context) error) {
	a := s.act.node.app
	a.mu.Lock()
	defer a.mu.Unlock()
	n := s.childLocked()
	n.act = n.newActivation("", 0, nil)
	if n.stopped {
		n.act.closed, n.act.ended = true, true
		n.act.cancel()
		return n.act.scope, func(context.Context) error { return nil }
	}
	n.act.active = true
	n.state = Active
	return n.act.scope, n.release
}

// When runs start in a sub-scope while every requirement is available, and
// stops that sub-scope when one of them goes away. The instance itself does
// not wait for them.
func (s *Scope) When(requires []Requirement, start func(context.Context, *Scope) error) (release func(context.Context) error) {
	a := s.act.node.app
	a.mu.Lock()
	defer a.mu.Unlock()
	n := s.childLocked()
	if n.stopped {
		return func(context.Context) error { return nil }
	}
	n.requires = resolve(requires)
	n.start = func(ctx context.Context, scope *Scope, _ any) error { return start(ctx, scope) }
	n.kickLocked()
	return n.release
}

// childLocked creates an anonymous child instance. On a closed scope the
// child is already stopped and is not attached.
func (s *Scope) childLocked() *node {
	a := s.act.node.app
	n := &node{app: a, id: a.nextID(), name: s.act.node.name, anonymous: true, owner: s.act, base: s}
	if s.act.closed {
		n.stopped = true
		n.state = Stopped
		return n
	}
	a.nodes[n.id] = n
	s.act.children = append(s.act.children, n)
	return n
}

// release stops an anonymous instance and waits for it.
func (n *node) release(ctx context.Context) error {
	a := n.app
	a.mu.Lock()
	if n.released {
		a.mu.Unlock()
		return nil
	}
	n.stopped = true
	n.kickLocked()
	blocked := a.blockedLocked(ctx, n)
	a.mu.Unlock()
	if blocked {
		return ErrReentrant
	}
	if err := a.awaitContext(ctx, func() bool { return n.state == Stopped }); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if n.released {
		return nil
	}
	n.released = true // the error is reported once, to the first waiter
	return n.err
}

// Within reports whether s belongs to other's instance or to one of its
// descendants.
func (s *Scope) Within(other *Scope) bool {
	return s.act.node.within(other.act.node.named())
}

// Shares reports whether s and other resolve service in the same space.
func (s *Scope) Shares(other *Scope, service ServiceKey) bool {
	name := service.serviceName()
	return realmOf(s, name) == realmOf(other, name)
}

// Filter derives a scope whose dispatches only reach handlers whose
// registering scope satisfies match. Handlers registered as Global are
// reached regardless.
func (s *Scope) Filter(match func(*Scope) bool) *Scope {
	derived := s.derive()
	derived.filter = match
	return derived
}

// Realm is an isolated resolution space for services.
type Realm struct{ label string }

// NewRealm returns a resolution space that several scopes can share through
// IsolateIn. The label is what String returns.
func NewRealm(label string) *Realm { return &Realm{label: label} }

// String is the label given to NewRealm.
func (r *Realm) String() string { return r.label }

type isolation struct {
	name   string
	realm  *Realm
	parent *isolation
}

// Isolate derives a scope in which each given service resolves in a fresh
// private space. Instances loaded on it, and their descendants, inherit that.
func (s *Scope) Isolate(services ...ServiceKey) *Scope {
	derived := s.derive()
	for _, service := range services {
		name := service.serviceName()
		derived.isolation = &isolation{name: name, realm: NewRealm(name), parent: derived.isolation}
	}
	return derived
}

// IsolateIn is Isolate with an explicit space, so that separate scopes can
// resolve the given services together.
func (s *Scope) IsolateIn(realm *Realm, services ...ServiceKey) *Scope {
	derived := s.derive()
	for _, service := range services {
		derived.isolation = &isolation{name: service.serviceName(), realm: realm, parent: derived.isolation}
	}
	return derived
}

// realmOf returns the space in which s resolves name; nil is the shared space.
func realmOf(s *Scope, name string) *Realm {
	if s == nil {
		return nil
	}
	for entry := s.isolation; entry != nil; entry = entry.parent {
		if entry.name == name {
			return entry.realm
		}
	}
	return nil
}
