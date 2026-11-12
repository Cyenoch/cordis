package cordis

import (
	"fmt"
	"reflect"
)

// Requirement is one entry of Plugin.Requires: a service the instance needs
// before it starts. Service keys, Name, Service.Where and Setting.Default
// produce requirements.
type Requirement interface {
	requirement() requirement
}

// ServiceKey names a service without regard to its Go type. Service keys and
// Name are service keys.
type ServiceKey interface {
	serviceName() string
}

type requirement struct {
	name    string
	typ     reflect.Type    // nil when required by name only
	accept  func(any) error // nil when any provider is acceptable
	setting *settingID      // default setting carried by the requirement
	value   any
}

func resolve(requires []Requirement) []requirement {
	resolved := make([]requirement, len(requires))
	for i, r := range requires {
		resolved[i] = r.requirement()
	}
	return resolved
}

type address struct {
	realm *Realm
	name  string
}

// provision is one published service value.
type provision struct {
	id         uint64
	name       string
	announced  bool // listeners were told it is available
	realm      *Realm
	typ        reflect.Type
	value      any
	owner      *activation
	epoch      uint64 // bumped by Changed to re-evaluate conditions
	dependents map[*activation]struct{}
}

type typeError struct {
	name, instance string
	held, asked    reflect.Type
}

// Error names the service, the requiring instance and both Go types.
func (e *typeError) Error() string {
	return fmt.Sprintf("service %q required by %q: %v: provided as %v, requested as %v", e.name, e.instance, ErrTypeMismatch, e.held, e.asked)
}

// Unwrap yields ErrTypeMismatch.
func (e *typeError) Unwrap() error { return ErrTypeMismatch }

// Name refers to a service by name only, without a Go type. It is meant for
// code driven by configuration; plugins should use typed keys.
type Name string

func (n Name) serviceName() string      { return string(n) }
func (n Name) requirement() requirement { return requirement{name: string(n)} }

// Service is a typed key for a service whose value has type T. Declare keys
// as package-level variables. T is usually a small interface.
type Service[T any] struct{ name string }

// NewService returns the key for the service called name.
func NewService[T any](name string) Service[T] { return Service[T]{name: name} }

// Name returns the service name.
func (k Service[T]) Name() string { return k.name }

func (k Service[T]) serviceName() string { return k.name }

func (k Service[T]) requirement() requirement {
	return requirement{name: k.name, typ: reflect.TypeFor[T]()}
}

// Where requires the service only when accept returns nil for its value.
// Otherwise the instance waits and the returned error is reported as the
// reason. The result is cached until the provider changes or calls Changed.
func (k Service[T]) Where(accept func(T) error) Requirement {
	r := k.requirement()
	r.accept = func(value any) error {
		typed, _ := value.(T) // a nil interface value stays the zero T
		return accept(typed)
	}
	return required(r)
}

type required requirement

func (r required) requirement() requirement { return requirement(r) }

// Provide publishes value as the service for the scope's instance. Other
// instances see it once that instance is Active, and it is withdrawn when the
// instance stops. A second provider in the same resolution space is an error.
func (k Service[T]) Provide(s *Scope, value T) error {
	a := s.act.node.app
	a.mu.Lock()
	defer a.mu.Unlock()
	if s.act.closed {
		return fmt.Errorf("provide %q from %q: %w", k.name, s.act.node.name, ErrInactive)
	}
	at := address{realm: realmOf(s, k.name), name: k.name}
	if existing := a.services[at]; existing != nil {
		return fmt.Errorf("provide %q from %q: %w by %q", k.name, s.act.node.name, ErrConflict, existing.owner.node.name)
	}
	p := &provision{id: a.nextID(), name: k.name, realm: at.realm, typ: reflect.TypeFor[T](), value: value, owner: s.act, dependents: map[*activation]struct{}{}}
	a.services[at] = p
	s.act.provisions = append(s.act.provisions, p)
	if a.visibleLocked(p) {
		a.announceLocked(p, true)
		a.kickAllLocked()
	}
	return nil
}

// announceLocked queues a ServiceChanged event, once per actual change.
func (a *App) announceLocked(p *provision, available bool) {
	if p.announced == available {
		return
	}
	p.announced = available
	a.noticeLocked(notice{service: &ServiceChange{Service: p.name, Provider: p.owner.node.info(), Available: available}, realm: p.realm})
}

// Get returns the service value visible from s. It succeeds for services the
// instance or an ancestor declared in Requires, and for services the instance
// or an ancestor provides. For those it keeps succeeding until the instance
// has finished its cleanups. From the root scope it returns whatever provider
// is currently available, without any lifetime guarantee.
func (k Service[T]) Get(s *Scope) (T, error) {
	var zero T
	a := s.act.node.app
	a.mu.Lock()
	p, err := a.lookupLocked(s, k.name)
	a.mu.Unlock()
	if err != nil {
		return zero, fmt.Errorf("get %q from %q: %w", k.name, s.act.node.name, err)
	}
	if asked := reflect.TypeFor[T](); p.typ != asked {
		return zero, &typeError{name: k.name, instance: s.act.node.name, held: p.typ, asked: asked}
	}
	value, _ := p.value.(T) // a nil interface value stays the zero T
	return value, nil
}

// Changed tells the kernel that the outcome of Where conditions on this
// service may have changed. Call it from the provider's scope.
func (k Service[T]) Changed(s *Scope) {
	a := s.act.node.app
	a.mu.Lock()
	defer a.mu.Unlock()
	if p := a.services[address{realm: realmOf(s, k.name), name: k.name}]; p != nil {
		p.epoch++
		a.kickAllLocked()
	}
}

// lookupLocked resolves name from s by walking up the instance tree: each
// instance's own publications, then its bindings. The walk stops where the
// caller's resolution space for name diverges from the instance's.
func (a *App) lookupLocked(s *Scope, name string) (*provision, error) {
	realm := realmOf(s, name)
	if s.act.ended {
		return nil, ErrInactive
	}
	for act := s.act; act != nil; act = act.node.owner {
		// Publications on the instance's sub-scopes count as its own.
		for _, p := range a.provisionsLocked(act) {
			if p.name == name && p.realm == realm {
				return p, nil
			}
		}
		if act.node == a.root {
			if s.act.node.named() != a.root {
				break
			}
			if p := a.services[address{realm: realm, name: name}]; p != nil && a.visibleLocked(p) {
				return p, nil
			}
			return nil, ErrUnavailable
		}
		if realmOf(act.scope, name) != realm {
			break
		}
		if p := act.bound[name]; p != nil {
			return p, nil
		}
	}
	return nil, ErrNotRequired
}
