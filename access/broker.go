package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/Cyenoch/cordis/contract"
)

// AuthorizationError has a safe message while preserving a host-only cause.
type AuthorizationError struct{ Cause error }

func (e *AuthorizationError) Error() string { return "capability scope rejected" }
func (e *AuthorizationError) Unwrap() []error {
	return []error{contract.Fail(contract.Denied, "capability scope rejected"), e.Cause}
}

// ScopeHandler must authorize each input against BOTH declared and granted scope.
// Nil handlers are rejected: absence of host scope semantics never means allow.
type ScopeHandler func(
	context.Context, contract.Identity, json.RawMessage, json.RawMessage, json.RawMessage,
) error

type HostHandler func(context.Context, contract.Identity, json.RawMessage) (json.RawMessage, error)

type capabilityHandler struct {
	authorize ScopeHandler
	handle    HostHandler
}

type Broker struct {
	policy   *Policy
	mu       sync.RWMutex
	handlers map[string]capabilityHandler
}

// NewBroker creates a broker. It panics if policy is nil.
func NewBroker(policy *Policy) *Broker {
	if policy == nil {
		panic("access: broker requires a policy")
	}
	return &Broker{policy: policy}
}

func (b *Broker) Register(capability string, authorize ScopeHandler, handler HostHandler) error {
	if capability == "" || authorize == nil || handler == nil {
		return contract.Fail(contract.Invalid, "incomplete capability handler")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.handlers == nil {
		b.handlers = make(map[string]capabilityHandler)
	}
	if _, exists := b.handlers[capability]; exists {
		return contract.Fail(contract.Conflict, "capability handler already registered")
	}
	b.handlers[capability] = capabilityHandler{authorize: authorize, handle: handler}
	return nil
}

// Register adapts business data without changing Broker's authorization or
// trusted session identity. Local handler errors retain their original chain.
func Register[I, O any](
	b *Broker,
	capability string,
	authorize ScopeHandler,
	handler func(context.Context, contract.Identity, I) (O, error),
) error {
	if b == nil || handler == nil {
		return contract.Fail(contract.Invalid, "incomplete typed capability handler")
	}
	if err := contract.CheckDataType(reflect.TypeFor[I]()); err != nil {
		return fmt.Errorf("capability input: %w", err)
	}
	if err := contract.CheckDataType(reflect.TypeFor[O]()); err != nil {
		return fmt.Errorf("capability output: %w", err)
	}
	return b.Register(capability, authorize,
		func(ctx context.Context, owner contract.Identity, raw json.RawMessage) (json.RawMessage, error) {
			var input I
			if err := contract.DecodeData(raw, &input); err != nil {
				return nil, err
			}
			output, err := handler(ctx, owner, input)
			if err != nil {
				return nil, err
			}
			return contract.Encode(output)
		})
}

// Call takes owner from the trusted session binding; do not decode it from input.
// Host errors are returned to the host caller, never automatically logged. A wire
// boundary must project errors through PublicError before sending to guests.
func (b *Broker) Call(
	ctx context.Context, owner contract.Identity, capability string, input json.RawMessage,
) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("host call: %w", err)
	}
	if err := contract.CheckJSON(input, 32, 10000); err != nil {
		return nil, contract.Fail(contract.Invalid, "invalid host call input")
	}
	b.mu.RLock()
	handler, exists := b.handlers[capability]
	b.mu.RUnlock()
	if !exists {
		return nil, contract.Fail(contract.Denied, "capability unavailable")
	}
	p := b.policy
	p.mu.Lock()
	a, err := p.activeLocked(owner)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	declared, exists := a.declared[capability]
	grant := a.grants[capability]
	if !exists || grant == nil || grant.revoked {
		p.mu.Unlock()
		return nil, contract.Fail(contract.Denied, "capability not granted")
	}
	// Host callbacks receive independent data, never mutable policy internals.
	declaration := append(json.RawMessage(nil), declared...)
	grantedScope := append(json.RawMessage(nil), grant.scope...)
	p.mu.Unlock()
	request := append(json.RawMessage(nil), input...)
	if err := handler.authorize(ctx, owner, declaration, grantedScope, append(json.RawMessage(nil), request...)); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("authorize host capability: %w", err)
		}
		return nil, &AuthorizationError{Cause: err}
	}
	p.mu.Lock()
	current, err := p.activeLocked(owner)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if current != a || a.grants[capability] != grant || grant.revoked {
		p.mu.Unlock()
		return nil, contract.Fail(contract.Denied, "grant revoked before admission")
	}
	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("host admission: %w", err)
	}
	callCtx, cancel := context.WithCancel(ctx)
	lease := &Lease{policy: p, activation: a, grant: grant, ctx: callCtx, cancel: cancel}
	a.calls[lease] = struct{}{}
	if p.calls == nil {
		p.calls = make(map[*Lease]struct{})
	}
	p.calls[lease] = struct{}{}
	p.mu.Unlock()
	defer lease.release()
	result, err := handler.handle(callCtx, owner, request)
	if err != nil {
		return nil, fmt.Errorf("host capability handler: %w", err)
	}
	// Even an uncooperative handler cannot publish a late result after revocation.
	p.mu.Lock()
	_, activeErr := p.activeLocked(owner)
	revoked := grant.revoked || callCtx.Err() != nil
	p.mu.Unlock()
	if activeErr != nil {
		return nil, activeErr
	}
	if revoked {
		return nil, contract.Fail(contract.Canceled, "host call canceled")
	}
	if err := contract.CheckJSON(result, 32, 10000); err != nil {
		return nil, contract.Fail(contract.Internal, "invalid host result")
	}
	return result, nil
}
