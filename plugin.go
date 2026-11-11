package cordis

import (
	"context"
	"fmt"
	"reflect"
)

// Plugin describes how to run one instance from a configuration of type C.
type Plugin[C any] struct {
	// Name identifies instances in errors, logs and lifecycle events.
	Name string
	// Requires lists the services that must be available before Start runs.
	// When one of them goes away the instance stops and waits for it.
	Requires []Requirement
	// Validate rejects a configuration before it is applied. Optional.
	Validate func(C) error
	// Start sets the instance up and returns. ctx is canceled when the
	// instance stops; s is valid for this activation only.
	Start func(ctx context.Context, s *Scope, config C) error
}

// Load starts an instance of the plugin as a child of the scope's instance
// and returns at once; use Wait to learn the outcome. On a scope whose
// instance is stopping, the returned handle is already Stopped.
func (p Plugin[C]) Load(s *Scope, config C) *Instance[C] {
	a := s.act.node.app
	n := &node{
		app: a, name: p.Name, owner: s.act, base: s, config: config, revision: 1, done: make(chan struct{}),
		requires:  resolve(p.Requires),
		configTyp: reflect.TypeFor[C](),
	}
	if p.Start != nil {
		n.start = func(ctx context.Context, scope *Scope, config any) error {
			return p.Start(ctx, scope, config.(C))
		}
	}
	if p.Validate != nil {
		n.validate = func(config any) error { return p.Validate(config.(C)) }
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	n.id = a.nextID()
	if s.act.closed {
		n.stopped = true
		n.state = Stopped
		n.err = fmt.Errorf("load %q: %w", p.Name, ErrInactive)
		close(n.done)
		return &Instance[C]{n: n}
	}
	a.nodes[n.id] = n
	s.act.children = append(s.act.children, n)
	n.kickLocked()
	return &Instance[C]{n: n}
}

// Instance is the handle of a loaded plugin.
type Instance[C any] struct{ n *node }

// Info identifies the instance.
func (i *Instance[C]) Info() Info { return i.n.info() }

// Status returns a consistent snapshot.
func (i *Instance[C]) Status() Status {
	n := i.n
	n.app.mu.Lock()
	defer n.app.mu.Unlock()
	status := Status{State: n.state, Err: n.err, Activations: n.activations}
	if n.state == Waiting {
		for index, req := range n.requires {
			p := n.candidateLocked(req)
			if p == nil {
				status.Missing = append(status.Missing, Missing{Service: req.name, Reason: ErrUnavailable})
			} else if cached, ok := n.conditions[index]; ok && cached.provision == p.id && cached.err != nil {
				status.Missing = append(status.Missing, Missing{Service: req.name, Reason: cached.err})
			}
		}
	}
	return status
}

// Done is closed when the instance has reached Stopped.
func (i *Instance[C]) Done() <-chan struct{} { return i.n.done }

// Wait blocks until no start or stop is in flight. It returns the instance's
// error when it is Failed. Canceling ctx only ends the wait.
func (i *Instance[C]) Wait(ctx context.Context) error {
	return i.n.settle(ctx, 0)
}

// Update validates config and applies it. By default the instance stops and
// starts again; an instance that registered OnUpdate may apply it in place.
// It returns ErrSuperseded when a later Update replaced this one.
func (i *Instance[C]) Update(ctx context.Context, config C) error {
	n := i.n
	if n.validate != nil {
		if err := n.app.guard("validation", n.name, func() error { return n.validate(config) }); err != nil {
			return err
		}
	}
	return n.revise(ctx, func() {
		n.config = config
		n.validated = n.revision + 1
	})
}

// Restart stops the instance and starts it again with its current
// configuration. It also recovers a Failed instance. It does not supersede
// a pending Update.
func (i *Instance[C]) Restart(ctx context.Context) error {
	n := i.n
	a := n.app
	a.mu.Lock()
	if n.stopped {
		a.mu.Unlock()
		return fmt.Errorf("restart %q: %w", n.name, ErrInactive)
	}
	n.err = nil
	if n.act != nil {
		n.act.restart = true
	}
	n.kickLocked()
	blocked := a.blockedLocked(ctx, n)
	a.mu.Unlock()
	if blocked {
		return ErrReentrant
	}
	return n.settle(ctx, 0)
}

// Stop stops the instance for good and waits for its cleanups. It returns
// the error the instance ended with: a cleanup error, or the failure it was
// already in. The handle cannot be used to start it again, and stopping it
// again is a no-op that returns the same error.
func (i *Instance[C]) Stop(ctx context.Context) error {
	n := i.n
	a := n.app
	a.mu.Lock()
	if n.state == Stopped {
		defer a.mu.Unlock()
		return n.err
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
	return n.err
}

// revise commits a new configuration revision and waits for it to settle.
func (n *node) revise(ctx context.Context, change func()) error {
	a := n.app
	a.mu.Lock()
	if n.stopped {
		a.mu.Unlock()
		return fmt.Errorf("update %q: %w", n.name, ErrInactive)
	}
	change()
	n.revision++
	n.err = nil
	revision := n.revision
	n.kickLocked()
	blocked := a.blockedLocked(ctx, n)
	a.mu.Unlock()
	if blocked {
		return ErrReentrant
	}
	return n.settle(ctx, revision)
}

// settle waits until the instance has no transition in flight. A non-zero
// revision makes the wait fail once a newer revision has been committed.
func (n *node) settle(ctx context.Context, revision uint64) error {
	a := n.app
	a.mu.Lock()
	if n.driving && a.blockedLocked(ctx, n) {
		a.mu.Unlock()
		return ErrReentrant
	}
	a.mu.Unlock()
	superseded := false
	err := a.awaitContext(ctx, func() bool {
		if revision != 0 && n.revision != revision {
			superseded = true
			return true
		}
		return !n.driving
	})
	if err != nil {
		return err
	}
	if superseded {
		return fmt.Errorf("update %q: %w", n.name, ErrSuperseded)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if n.state == Failed {
		return n.err
	}
	return nil
}

// OnUpdate registers how the scope's instance applies a new configuration
// without restarting. Call it from Start. fn reports whether it applied the
// configuration; false falls back to a restart, and an error fails the
// instance. C must be the plugin's configuration type.
func OnUpdate[C any](s *Scope, fn func(ctx context.Context, config C) (applied bool, err error)) error {
	a := s.act.node.app
	a.mu.Lock()
	defer a.mu.Unlock()
	// The handler belongs to the activation the scope came from. A scope of
	// an earlier activation must not install its handler on the current one.
	act := s.act
	for act.node.anonymous {
		act = act.node.owner
	}
	if s.act.closed || act.node.act != act {
		return fmt.Errorf("on update of %q: %w", act.node.name, ErrInactive)
	}
	if want := act.node.configTyp; want != reflect.TypeFor[C]() {
		return fmt.Errorf("on update of %q: %w: plugin configuration is %v, handler takes %v", act.node.name, ErrTypeMismatch, want, reflect.TypeFor[C]())
	}
	act.update = func(ctx context.Context, config any) (bool, error) { return fn(ctx, config.(C)) }
	return nil
}
