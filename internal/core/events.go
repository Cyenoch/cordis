package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/Cyenoch/cordis/contract"
)

type subscription struct {
	engine  *Engine
	owner   *activation
	name    string
	order   uint64
	options contract.SubscriptionOptions
	active  bool
	handler contract.EventHandler
	prepare func() contract.EventHandler
}

func (s *subscription) Cancel(ctx context.Context) error {
	e := s.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if !s.active {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.removeSubscriptionLocked(s)
	return nil
}

func (e *Engine) removeSubscriptionLocked(s *subscription) {
	delete(s.owner.subscriptions, s.order)
	e.events = slices.DeleteFunc(e.events, func(current *subscription) bool { return current == s })
	s.active, s.owner, s.handler, s.prepare = false, nil, nil, nil
}

func (e *Engine) publishSubscriptionLocked(sub *subscription) {
	at, _ := slices.BinarySearchFunc(e.events, sub, func(a, b *subscription) int {
		if a.options.Prepend != b.options.Prepend {
			if a.options.Prepend {
				return -1
			}
			return 1
		}
		if a.options.Prepend {
			return cmp.Compare(b.order, a.order)
		}
		return cmp.Compare(a.order, b.order)
	})
	e.events = slices.Insert(e.events, at, sub)
}

func (s *Scope) On(ctx context.Context, name string, options contract.SubscriptionOptions, handler contract.EventHandler) (contract.Subscription, error) {
	return s.subscribe(ctx, name, options, handler, nil)
}

// OnPrepared reserves a native send receipt at admission, under the state lock.
// prepare captures references only; its returned handler runs outside that lock.
func (s *Scope) OnPrepared(ctx context.Context, name string, options contract.SubscriptionOptions, prepare func() contract.EventHandler) (contract.Subscription, error) {
	return s.subscribe(ctx, name, options, nil, prepare)
}

func (s *Scope) subscribe(ctx context.Context, name string, options contract.SubscriptionOptions, handler contract.EventHandler, prepare func() contract.EventHandler) (contract.Subscription, error) {
	if name == "" || handler == nil && prepare == nil {
		return nil, contract.Fail(contract.Invalid, "event name and handler are required")
	}
	a := s.activation
	e := a.instance.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.retiring {
		return nil, contract.Fail(contract.NotReady, "scope is retiring")
	}
	if len(a.subscriptions) >= contract.MaxSubscriptions {
		return nil, contract.Fail(contract.Denied, "active subscription limit reached")
	}
	e.sequence++
	sub := &subscription{engine: e, owner: a, name: name, order: e.sequence,
		options: options, active: true, handler: handler, prepare: prepare}
	a.subscriptions[sub.order] = sub
	if a.committed {
		e.publishSubscriptionLocked(sub)
	}
	return sub, nil
}

func (s *Scope) Emit(ctx context.Context, name string, input json.RawMessage, mode contract.Dispatch) ([]contract.EventResult, error) {
	a := s.activation
	e := a.instance.engine
	e.mu.Lock()
	retiring := a.retiring
	e.mu.Unlock()
	if retiring {
		return nil, contract.Fail(contract.NotReady, "scope is retiring")
	}
	return e.Emit(context.WithValue(ctx, actorKey{}, a), name, input, mode)
}

func (e *Engine) Emit(ctx context.Context, name string, input json.RawMessage, mode contract.Dispatch) ([]contract.EventResult, error) {
	if mode != contract.Sequential && mode != contract.Parallel && mode != contract.First {
		return nil, contract.Fail(contract.Invalid, "unknown event dispatch mode")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	listeners := []*subscription{}
	for _, listener := range e.events {
		if listener.name == name && listener.owner.ready && !listener.owner.retiring {
			listeners = append(listeners, listener)
		}
	}
	e.mu.Unlock()
	results := make([]contract.EventResult, len(listeners))
	dispatched := make([]bool, len(listeners))
	errs := make([]error, len(listeners))
	collect := func(end int) []contract.EventResult {
		out := make([]contract.EventResult, 0, end)
		for n := range end {
			if dispatched[n] {
				out = append(out, results[n])
			}
		}
		return out
	}
	run := func(n int) {
		listener := listeners[n]
		e.mu.Lock()
		if !listener.active {
			e.mu.Unlock()
			return
		}
		owner := listener.owner
		proxy := &provider{owner: owner, handler: func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			// Admission after the business gate lets a callback cancel queued
			// listeners without waiting for itself to release that gate.
			e.mu.Lock()
			if !listener.active {
				e.mu.Unlock()
				return nil, nil
			}
			handler := listener.handler
			if listener.prepare != nil {
				handler = listener.prepare()
			}
			if listener.options.Once {
				e.removeSubscriptionLocked(listener)
			}
			dispatched[n] = true
			e.mu.Unlock()
			var err error
			results[n], err = handler(ctx, raw)
			return nil, err
		}}
		_, err := e.callLocked(ctx, proxy, input, false)
		if err != nil {
			errs[n] = fmt.Errorf("event %s listener %s/%d: %w", name, owner.identity.Entry, listener.order, err)
			e.callFailure(ctx, owner.identity, slog.String("event", name), errs[n])
		}
	}
	if mode == contract.Parallel {
		var group sync.WaitGroup
		limit := make(chan struct{}, 8)
		for n := range listeners {
			select {
			case limit <- struct{}{}:
			case <-ctx.Done():
				group.Wait()
				return collect(len(listeners)), errors.Join(errors.Join(errs...), ctx.Err())
			}
			group.Add(1)
			go func() { defer group.Done(); defer func() { <-limit }(); run(n) }()
		}
		group.Wait()
		return collect(len(listeners)), errors.Join(errs...)
	}
	for n := range listeners {
		run(n)
		if errs[n] != nil {
			return collect(n + 1), errs[n]
		}
		if mode == contract.First && dispatched[n] && results[n].Handled {
			return collect(n + 1), nil
		}
	}
	return collect(len(listeners)), nil
}
