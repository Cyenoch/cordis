package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func TestEventDispatchHandledZeroAndParallelErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		var later atomic.Int32
		first := add(t, e, "first", contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
			return s.On("value", func(context.Context, json.RawMessage) (contract.EventResult, error) {
				return contract.EventResult{Handled: true, Value: json.RawMessage(`0`)}, nil
			})
		}})
		if err := first.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		second := add(t, e, "second", contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
			return s.On("value", func(context.Context, json.RawMessage) (contract.EventResult, error) {
				later.Add(1)
				return contract.EventResult{}, nil
			})
		}})
		if err := second.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		results, err := e.Emit(t.Context(), "value", nil, contract.First)
		if err != nil || len(results) != 1 || string(results[0].Value) != `0` || later.Load() != 0 {
			t.Fatalf("handled zero: %+v %v", results, err)
		}
		if _, err := e.Emit(t.Context(), "value", nil, contract.Sequential); err != nil {
			t.Fatal(err)
		}
		if later.Load() != 1 {
			t.Fatal("sequential listener omitted")
		}
		entered, release := make(chan struct{}), make(chan struct{})
		failure := errors.New("listener failed")
		slow := add(t, e, "slow", contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
			return s.On("parallel", func(context.Context, json.RawMessage) (contract.EventResult, error) {
				close(entered)
				<-release
				return contract.EventResult{Handled: true}, nil
			})
		}})
		if err := slow.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		broken := add(t, e, "broken-listener", contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
			return s.On("parallel", func(context.Context, json.RawMessage) (contract.EventResult, error) {
				return contract.EventResult{}, failure
			})
		}})
		if err := broken.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { _, err := e.Emit(t.Context(), "parallel", nil, contract.Parallel); done <- err }()
		<-entered
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("parallel dispatch did not wait all")
		default:
		}
		close(release)
		if err := <-done; !errors.Is(err, failure) {
			t.Fatalf("parallel error lost: %v", err)
		}
	})
}
func TestInstanceCallbacksRemainSerialAndStopRejectsQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		entered := make(chan struct{})
		var calls atomic.Int32
		i := add(t, e, "listener", contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
			handler := func(ctx context.Context, input json.RawMessage) (contract.EventResult, error) {
				if calls.Add(1) > 1 {
					t.Error("queued callback admitted after stop")
				}
				close(entered)
				<-ctx.Done()
				return contract.EventResult{}, ctx.Err()
			}
			if err := s.On("event", handler); err != nil {
				return err
			}
			return s.On("event", handler)
		}})
		if err := i.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		emitted := make(chan error, 1)
		go func() { _, err := e.Emit(t.Context(), "event", nil, contract.Parallel); emitted <- err }()
		<-entered
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("same instance callbacks overlapped")
		}
		if err := i.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := <-emitted; err == nil {
			t.Fatal("dispatch hid canceled callbacks")
		}
		if _, err := e.Emit(t.Context(), "event", nil, contract.Sequential); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 1 {
			t.Fatal("retired listener called")
		}
	})
}
func TestWaitGraphRejectsCrossRootCycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		aService, bService := contract.Service{Name: "a", Major: 1}, contract.Service{Name: "b", Major: 1}
		aReq, bReq := contract.Requirement{Name: "a", Major: 1}, contract.Requirement{Name: "b", Major: 1}
		enteredA, enteredB := make(chan struct{}), make(chan struct{})
		proceedA, proceedB := make(chan struct{}), make(chan struct{})
		makeDefinition := func(service contract.Service, target contract.Requirement, entered, proceed chan struct{}) contract.Definition {
			return contract.Definition{Provides: []contract.Service{service}, Start: func(ctx context.Context, s contract.Scope) error {
				return s.Provide(service, func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
					if string(input) == `"root"` {
						close(entered)
						<-proceed
						return e.Call(ctx, target, json.RawMessage(`"nested"`))
					}
					return json.RawMessage(`0`), nil
				})
			}}
		}
		a := add(t, e, "a", makeDefinition(aService, bReq, enteredA, proceedA))
		b := add(t, e, "b", makeDefinition(bService, aReq, enteredB, proceedB))
		if err := a.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := b.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		doneA, doneB := make(chan error, 1), make(chan error, 1)
		go func() { _, err := e.Call(t.Context(), aReq, json.RawMessage(`"root"`)); doneA <- err }()
		go func() { _, err := e.Call(t.Context(), bReq, json.RawMessage(`"root"`)); doneB <- err }()
		<-enteredA
		<-enteredB
		close(proceedA)
		synctest.Wait()
		close(proceedB)
		var coded *contract.Error
		if err := <-doneB; !errors.As(err, &coded) || coded.Code != contract.Reentrant {
			t.Fatalf("cross-root cycle: %v", err)
		}
		if err := <-doneA; err != nil {
			t.Fatal(err)
		}
	})
}
func TestMiddlewareRejectsSecondNext(t *testing.T) {
	var downstream atomic.Int32
	handler := contract.Chain(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		downstream.Add(1)
		return json.RawMessage(`false`), nil
	}, func(ctx context.Context, input json.RawMessage, next contract.Handler) (json.RawMessage, error) {
		if _, err := next(ctx, input); err != nil {
			return nil, err
		}
		return next(ctx, input)
	})
	_, err := handler(t.Context(), nil)
	var coded *contract.Error
	if !errors.As(err, &coded) || coded.Code != contract.Reentrant || downstream.Load() != 1 {
		t.Fatalf("middleware duplicate delegation: %d %v", downstream.Load(), err)
	}
}
func TestCleanupServiceFailureRetainsAllDisposers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		failure := errors.New("cleanup service failed")
		var last atomic.Bool
		p := add(t, e, "provider", contract.Definition{Provides: []contract.Service{service}, Start: func(ctx context.Context, s contract.Scope) error {
			return s.Provide(service, func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, failure })
		}})
		if err := p.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		c := add(t, e, "consumer", contract.Definition{Requires: []contract.Requirement{requirement}, Start: func(ctx context.Context, s contract.Scope) error {
			if err := s.Defer(func(context.Context) error { last.Store(true); return nil }); err != nil {
				return err
			}
			return s.Defer(func(ctx context.Context) error { _, err := s.Call(ctx, requirement, nil); return err })
		}})
		if err := c.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := p.Stop(t.Context()); !errors.Is(err, failure) {
			t.Fatalf("cleanup failure disappeared: %v", err)
		}
		if !last.Load() {
			t.Fatal("remaining disposer skipped")
		}
		if err := e.Close(t.Context()); !errors.Is(err, failure) {
			t.Fatalf("close diagnostics: %v", err)
		}
	})
}
