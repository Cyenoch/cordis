package core

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"

	"github.com/Cyenoch/cordis/contract"
)

type actorKey struct{}
type cleanupKey struct{}

// Scope owns one activation's registration snapshot and reverse-order resource group.
type Scope struct{ activation *activation }

func (s *Scope) Identity() contract.Identity { return s.activation.identity }
func (s *Scope) Config() json.RawMessage     { return slices.Clone(s.activation.config) }
func (s *Scope) Context() context.Context    { return s.activation.ctx }
func (s *Scope) Defer(cleanup contract.Cleanup) error {
	if cleanup == nil {
		return contract.Fail(contract.Invalid, "nil cleanup")
	}
	a := s.activation
	e := a.instance.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	// A canceled initialization still owns any resources acquired before returning.
	if a.startFinished && a.retiring {
		return contract.Fail(contract.NotReady, "scope is retiring")
	}
	a.cleanups = append(a.cleanups, cleanup)
	return nil
}
func (s *Scope) Provide(service contract.Service, handler contract.Handler) error {
	if handler == nil || service.Name == "" {
		return contract.Fail(contract.Invalid, "service name and handler are required")
	}
	a := s.activation
	e := a.instance.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if a.retiring || a.startFinished {
		return contract.Fail(contract.NotReady, "registration snapshot is sealed")
	}
	declared := false
	for _, candidate := range a.instance.definition.Provides {
		if candidate == service {
			declared = true
			break
		}
	}
	if !declared {
		return contract.Fail(contract.Invalid, "service is not declared by definition")
	}
	if a.provided[service.Key()] != nil {
		return contract.Fail(contract.Conflict, "duplicate service registration")
	}
	a.provided[service.Key()] = &provider{service: service, handler: handler, owner: a}
	return nil
}
func (s *Scope) Call(ctx context.Context, requirement contract.Requirement, input json.RawMessage) (json.RawMessage, error) {
	a := s.activation
	e := a.instance.engine
	e.mu.Lock()
	p := a.bindings[requirement.Key()]
	if p == nil || !requirement.Accepts(p.service) {
		e.mu.Unlock()
		return nil, contract.Fail(contract.NotReady, "service is not in the activation binding snapshot")
	}
	cleanup := ctx.Value(cleanupKey{}) == a
	if a.retiring && !cleanup {
		e.mu.Unlock()
		return nil, contract.Fail(contract.NotReady, "scope is retiring")
	}
	ctx = context.WithValue(ctx, actorKey{}, a)
	return e.callLocked(ctx, p, input, cleanup)
}
func (e *Engine) Call(ctx context.Context, requirement contract.Requirement, input json.RawMessage) (json.RawMessage, error) {
	e.mu.Lock()
	p := e.resolveLocked(nil, requirement)
	if p == nil {
		e.mu.Unlock()
		return nil, contract.Fail(contract.NotReady, "service is unavailable")
	}
	// Host calls never inherit cleanup bypass authority.
	return e.callLocked(ctx, p, input, false)
}
func (e *Engine) callLocked(ctx context.Context, p *provider, input json.RawMessage, cleanup bool) (output json.RawMessage, err error) {
	target := p.owner
	defer func() {
		if p.service.Name != "" {
			e.callFailure(ctx, target.identity, slog.String("service", p.service.Key()), err)
		}
	}()
	if !target.ready && !cleanup {
		e.mu.Unlock()
		return nil, contract.Fail(contract.NotReady, "provider publication is not acknowledged")
	}
	if target.retiring && !cleanup {
		e.mu.Unlock()
		return nil, contract.Fail(contract.NotReady, "provider is retiring")
	}
	actor, _ := ctx.Value(actorKey{}).(*activation)
	if actor != nil {
		if actor == target || e.pathLocked(target, actor, map[*activation]bool{}) {
			e.mu.Unlock()
			return nil, contract.Fail(contract.Reentrant, "synchronous callback wait cycle")
		}
		if e.edges[actor] == nil {
			e.edges[actor] = map[*activation]int{}
		}
		e.edges[actor][target]++
	}
	target.work.Add(1)
	e.mu.Unlock()
	defer func() {
		if actor != nil {
			e.mu.Lock()
			e.edges[actor][target]--
			if e.edges[actor][target] == 0 {
				delete(e.edges[actor], target)
			}
			e.mu.Unlock()
		}
		target.work.Done()
	}()
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stop func() bool
	if !cleanup {
		stop = context.AfterFunc(target.ctx, cancel)
		defer stop()
	}
	select {
	case <-callCtx.Done():
		return nil, callCtx.Err()
	case target.gate <- struct{}{}:
	}
	defer func() { <-target.gate }()
	if err := callCtx.Err(); err != nil {
		return nil, err
	}
	if !cleanup && target.ctx.Err() != nil {
		return nil, target.ctx.Err()
	}
	// User code executes without the registry lock and retains the synchronous actor.
	callCtx = context.WithValue(callCtx, actorKey{}, target)
	if cleanup {
		callCtx = context.WithValue(callCtx, cleanupKey{}, target)
	}
	output, err = invokeHandler(p.handler, callCtx, input)
	if callCtx.Err() != nil {
		return nil, callCtx.Err()
	}
	return output, err
}
func (e *Engine) pathLocked(from, to *activation, seen map[*activation]bool) bool {
	if from == to {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	for next, count := range e.edges[from] {
		if count > 0 && e.pathLocked(next, to, seen) {
			return true
		}
	}
	for target, count := range e.instanceEdges[from] {
		if count > 0 && target.activation != nil && e.pathLocked(target.activation, to, seen) {
			return true
		}
	}
	return false
}
func invokeHandler(handler contract.Handler, ctx context.Context, input json.RawMessage) (output json.RawMessage, err error) {
	defer recoverCallback(ctx, "service callback", &err)
	return handler(ctx, input)
}
