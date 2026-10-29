package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type Caller interface {
	Call(context.Context, Requirement, json.RawMessage) (json.RawMessage, error)
}

type Registrar interface {
	Provide(Service, Handler) error
}

type Emitter interface {
	Emit(context.Context, string, json.RawMessage, Dispatch) ([]EventResult, error)
}

// Consumer binds an explicit routing range to a local generated method contract.
// Contract identifies the consumer's methods, not the provider's complete minor.
type Consumer struct {
	Requirement Requirement `json:"requirement"`
	Contract    string      `json:"contract"`
}

func (c Consumer) Check(name string, major, minor uint32, fingerprint string) error {
	r := c.Requirement
	if r.Name != name || r.Major != major || r.MinMinor > r.MaxMinor || minor < r.MinMinor || minor > r.MaxMinor || fingerprint == "" || c.Contract != fingerprint {
		return Fail(Incompatible, "consumer range or method contract does not match generated client")
	}
	return nil
}

func (d DefinitionSpec) Requirements() []Requirement {
	result := make([]Requirement, len(d.Requires))
	for i, consumer := range d.Requires {
		result[i] = consumer.Requirement
	}
	return result
}

type ManagedScope interface {
	Go(func(context.Context) error) error
}

// ActivationScope registers transport lifecycle notifications during Start.
// Publication follows final host commit; retirement precedes dependent cleanup.
type ActivationScope interface {
	OnPublish(func(context.Context) error) error
	OnRetire(func(context.Context) error) error
}

// Go queues explicit concurrent work until final activation publication.
func Go(owner any, work func(context.Context) error) error {
	scope, ok := owner.(ManagedScope)
	if !ok {
		return Fail(Incompatible, "scope does not support managed work")
	}
	return scope.Go(work)
}

// Own closes a resource immediately if its cleanup cannot be registered.
func Own[T interface{ Close() error }](owner interface{ Defer(Cleanup) error }, resource T) (T, error) {
	var zero T
	if owner == nil {
		return zero, errors.Join(Fail(Invalid, "resource owner is required"), resource.Close())
	}
	if err := owner.Defer(func(context.Context) error { return resource.Close() }); err != nil {
		return zero, errors.Join(fmt.Errorf("register resource: %w", err), resource.Close())
	}
	return resource, nil
}
