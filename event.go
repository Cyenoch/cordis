package cordis

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
)

// ListenOption adjusts how a listener, handler or middleware is registered.
type ListenOption uint8

const (
	// Prepend puts the callback before those already registered.
	Prepend ListenOption = iota + 1
	// Once removes the callback when it is first admitted.
	Once
	// Global makes the callback reachable regardless of Scope.Filter.
	Global
)

type handlerKind uint8

const (
	kindEvent handlerKind = iota
	kindQuery
	kindChain
)

type handlerKey struct {
	kind handlerKind
	name string
}

type handler struct {
	act    *activation
	scope  *Scope
	fn     any
	once   bool
	global bool
}

// register adds a callback owned by the scope's activation.
func (a *App) register(s *Scope, key handlerKey, fn any, options []ListenOption) (stop func()) {
	h := &handler{act: s.act, scope: s, fn: fn}
	prepend := false
	for _, option := range options {
		switch option {
		case Prepend:
			prepend = true
		case Once:
			h.once = true
		case Global:
			h.global = true
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if s.act.closed {
		return func() {}
	}
	if prepend {
		a.handlers[key] = slices.Insert(a.handlers[key], 0, h)
	} else {
		a.handlers[key] = append(a.handlers[key], h)
	}
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.removeLocked(key, h)
	}
}

func (a *App) removeLocked(key handlerKey, h *handler) bool {
	list := a.handlers[key]
	i := slices.Index(list, h)
	if i < 0 {
		return false
	}
	list = slices.Delete(slices.Clone(list), i, i+1)
	if len(list) == 0 {
		delete(a.handlers, key)
	} else {
		a.handlers[key] = list
	}
	return true
}

// reachable snapshots the callbacks a dispatch from s may call. The filter
// is caller code, so it runs after the lock is released.
func (a *App) reachable(s *Scope, key handlerKey) []*handler {
	a.mu.Lock()
	list := slices.DeleteFunc(slices.Clone(a.handlers[key]), func(h *handler) bool { return h.act.closed })
	a.mu.Unlock()
	if s.filter == nil {
		return list
	}
	return slices.DeleteFunc(list, func(h *handler) bool { return !h.global && !s.filter(h.scope) })
}

// enter admits one call into h. It returns the callback context and a
// function to call when the callback has returned, or false when the
// callback is gone.
func (a *App) enter(ctx context.Context, key handlerKey, h *handler) (context.Context, func(), bool) {
	a.mu.Lock()
	if h.act.closed || !slices.Contains(a.handlers[key], h) {
		a.mu.Unlock()
		return nil, nil, false
	}
	if h.once {
		a.removeLocked(key, h)
	}
	h.act.inflight++
	a.mu.Unlock()
	callers := append(slices.Clone(chain(ctx)), h.act.node)
	callCtx, cancel := context.WithCancel(context.WithValue(ctx, chainKey{}, callers))
	detach := context.AfterFunc(h.act.ctx, cancel)
	return callCtx, func() {
		detach()
		cancel()
		a.mu.Lock()
		h.act.inflight--
		a.broadcastLocked()
		a.mu.Unlock()
	}, true
}

func mismatch(name string, h *handler, want any) error {
	return &HandlerError{Name: name, Instance: h.act.node.info(), Err: fmt.Errorf("%w: registered as %T, dispatched as %T", ErrTypeMismatch, h.fn, want)}
}

// Event is a typed key for notifications with payload T.
type Event[T any] struct{ name string }

// NewEvent returns the key for the event called name.
func NewEvent[T any](name string) Event[T] { return Event[T]{name: name} }

// On registers fn for the scope's instance and returns a function that
// removes it. fn may be called concurrently.
func (e Event[T]) On(s *Scope, fn func(context.Context, T) error, options ...ListenOption) (stop func()) {
	return s.act.node.app.register(s, handlerKey{kindEvent, e.name}, fn, options)
}

// Emit calls the listeners one by one in registration order and stops at the
// first error.
func (e Event[T]) Emit(ctx context.Context, s *Scope, payload T) error {
	a := s.act.node.app
	key := handlerKey{kindEvent, e.name}
	for _, h := range a.reachable(s, key) {
		if err := e.call(ctx, a, key, h, payload); err != nil {
			return err
		}
	}
	return nil
}

// EmitParallel calls all listeners concurrently, waits for them and joins
// their errors.
func (e Event[T]) EmitParallel(ctx context.Context, s *Scope, payload T) error {
	a := s.act.node.app
	key := handlerKey{kindEvent, e.name}
	listeners := a.reachable(s, key)
	errs := make([]error, len(listeners))
	var group sync.WaitGroup
	for i, h := range listeners {
		group.Go(func() { errs[i] = e.call(ctx, a, key, h, payload) })
	}
	group.Wait()
	return errors.Join(errs...)
}

func (e Event[T]) call(ctx context.Context, a *App, key handlerKey, h *handler, payload T) error {
	fn, ok := h.fn.(func(context.Context, T) error)
	if !ok {
		return mismatch(e.name, h, fn)
	}
	callCtx, leave, admitted := a.enter(ctx, key, h)
	if !admitted {
		return nil
	}
	defer leave()
	if err := a.guard("listener", h.act.node.name, func() error { return fn(callCtx, payload) }); err != nil {
		return &HandlerError{Name: e.name, Instance: h.act.node.info(), Err: err}
	}
	return nil
}

// Query is a typed key for requests answered by the first handler that
// claims them.
type Query[In, Out any] struct{ name string }

// NewQuery returns the key for the query called name.
func NewQuery[In, Out any](name string) Query[In, Out] { return Query[In, Out]{name: name} }

// Handle registers fn for the scope's instance. fn reports ok when it has
// answered; the zero Out is a valid answer.
func (q Query[In, Out]) Handle(s *Scope, fn func(context.Context, In) (Out, bool, error), options ...ListenOption) (stop func()) {
	return s.act.node.app.register(s, handlerKey{kindQuery, q.name}, fn, options)
}

// Ask calls the handlers in registration order until one answers.
func (q Query[In, Out]) Ask(ctx context.Context, s *Scope, in In) (out Out, ok bool, err error) {
	a := s.act.node.app
	key := handlerKey{kindQuery, q.name}
	for _, h := range a.reachable(s, key) {
		fn, typed := h.fn.(func(context.Context, In) (Out, bool, error))
		if !typed {
			return out, false, mismatch(q.name, h, fn)
		}
		callCtx, leave, admitted := a.enter(ctx, key, h)
		if !admitted {
			continue
		}
		err = a.guard("handler", h.act.node.name, func() (err error) {
			out, ok, err = fn(callCtx, in)
			return err
		})
		leave()
		if err != nil {
			var zero Out
			return zero, false, &HandlerError{Name: q.name, Instance: h.act.node.info(), Err: err}
		}
		if ok {
			return out, true, nil
		}
	}
	var zero Out
	return zero, false, nil
}

// Handler is the downstream of a middleware, and the final step of a chain.
type Handler[In, Out any] func(context.Context, In) (Out, error)

// Middleware wraps the rest of a chain. It may change the input, return
// without calling next, or wrap next's result. next may be called at most
// once.
type Middleware[In, Out any] func(ctx context.Context, in In, next Handler[In, Out]) (Out, error)

// Chain is a typed key for a middleware chain.
type Chain[In, Out any] struct{ name string }

// NewChain returns the key for the chain called name.
func NewChain[In, Out any](name string) Chain[In, Out] { return Chain[In, Out]{name: name} }

// Use registers middleware for the scope's instance.
func (c Chain[In, Out]) Use(s *Scope, middleware Middleware[In, Out], options ...ListenOption) (stop func()) {
	return s.act.node.app.register(s, handlerKey{kindChain, c.name}, middleware, options)
}

// Do runs the chain in registration order and ends with final.
func (c Chain[In, Out]) Do(ctx context.Context, s *Scope, in In, final Handler[In, Out]) (Out, error) {
	a := s.act.node.app
	key := handlerKey{kindChain, c.name}
	list := a.reachable(s, key)
	var run func(int) Handler[In, Out]
	run = func(index int) Handler[In, Out] {
		return func(ctx context.Context, in In) (out Out, err error) {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			if index == len(list) {
				return final(ctx, in)
			}
			h := list[index]
			middleware, typed := h.fn.(Middleware[In, Out])
			if !typed {
				return out, mismatch(c.name, h, middleware)
			}
			callCtx, leave, admitted := a.enter(ctx, key, h)
			if !admitted {
				return run(index+1)(ctx, in)
			}
			defer leave()
			var called atomic.Bool
			var passed atomic.Pointer[error] // error handed up by the downstream
			downstream := run(index + 1)
			next := func(ctx context.Context, in In) (Out, error) {
				if !called.CompareAndSwap(false, true) {
					var zero Out
					return zero, fmt.Errorf("chain %q: next called more than once", c.name)
				}
				out, err := downstream(ctx, in)
				if err != nil {
					passed.Store(&err)
				}
				return out, err
			}
			err = a.guard("middleware", h.act.node.name, func() (err error) {
				out, err = middleware(callCtx, in, next)
				return err
			})
			// An error the middleware merely hands up keeps its original
			// location; an error of its own is located here.
			if err != nil && !sameError(err, passed.Load()) {
				err = &HandlerError{Name: c.name, Instance: h.act.node.info(), Err: err}
			}
			return out, err
		}
	}
	return run(0)(ctx, in)
}

// sameError reports whether err is the very error stored in other. Error
// values of uncomparable types are never the same.
func sameError(err error, other *error) (same bool) {
	if other == nil {
		return false
	}
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return err == *other
}
