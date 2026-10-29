package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func TestDynamicEventOrderAndFailedPublication(t *testing.T) {
	e := core.New()
	failure := errors.New("initialization failed")
	defer func() {
		if err := e.Close(t.Context()); !errors.Is(err, failure) {
			t.Fatalf("close failure: %v", err)
		}
	}()
	failed := add(t, e, "failed", contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
		if _, err := s.On(ctx, "change", contract.SubscriptionOptions{}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
			t.Error("failed activation published an event listener")
			return contract.EventResult{}, nil
		}); err != nil {
			return err
		}
		return failure
	}})
	if err := failed.Wait(t.Context()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	var scope contract.Scope
	active := add(t, e, "active", contract.Definition{Start: func(_ context.Context, s contract.Scope) error { scope = s; return nil }})
	if err := active.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	var order []string
	listen := func(name string, prepend, handled bool) contract.Subscription {
		t.Helper()
		sub, err := scope.On(t.Context(), "change", contract.SubscriptionOptions{Prepend: prepend}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
			order = append(order, name)
			return contract.EventResult{Handled: handled, Value: json.RawMessage(`false`)}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return sub
	}
	listen("A", false, false)
	b := listen("B", true, true)
	listen("C", true, false)
	results, err := e.Emit(t.Context(), "change", nil, contract.First)
	if err != nil || len(results) != 2 || !results[1].Handled || string(results[1].Value) != "false" || !slices.Equal(order, []string{"C", "B"}) {
		t.Fatalf("first dispatch: %v %v %v", order, results, err)
	}
	if err := b.Cancel(t.Context()); err != nil {
		t.Fatal(err)
	}
	listen("B-again", false, false)
	order = nil
	results, err = e.Emit(t.Context(), "change", nil, contract.Sequential)
	if err != nil || len(results) != 3 || !slices.Equal(order, []string{"C", "A", "B-again"}) {
		t.Fatalf("new registration order: %v %v %v", order, results, err)
	}
}

func TestConcurrentOnceConsumesEvenFailingCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		entered, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		var once contract.Subscription
		failure := errors.New("once failed")
		active := add(t, e, "once", contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
			var err error
			once, err = s.On(ctx, "once", contract.SubscriptionOptions{Once: true}, func(ctx context.Context, _ json.RawMessage) (contract.EventResult, error) {
				calls.Add(1)
				close(entered)
				if err := once.Cancel(ctx); err != nil {
					t.Error(err)
				}
				<-release
				return contract.EventResult{}, failure
			})
			return err
		}})
		if err := active.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		var group sync.WaitGroup
		var failures atomic.Int32
		for range 8 {
			group.Go(func() {
				_, err := e.Emit(t.Context(), "once", nil, contract.Parallel)
				if errors.Is(err, failure) {
					failures.Add(1)
				} else if err != nil {
					t.Error(err)
				}
			})
		}
		<-entered
		synctest.Wait()
		if err := once.Cancel(t.Context()); err != nil {
			t.Fatal(err)
		}
		close(release)
		group.Wait()
		if calls.Load() != 1 || failures.Load() != 1 {
			t.Fatalf("once calls=%d failures=%d", calls.Load(), failures.Load())
		}
		if results, err := e.Emit(t.Context(), "once", nil, contract.Sequential); err != nil || len(results) != 0 {
			t.Fatalf("consumed once retried: %v %v", results, err)
		}
	})
}

func TestSelfCancelRejectsQueuedEventsAndStopWaitsAdmitted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		entered, cancelNow, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
		var scope contract.Scope
		var sub contract.Subscription
		var calls atomic.Int32
		active := add(t, e, "self", contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
			scope = s
			var err error
			sub, err = s.On(ctx, "self", contract.SubscriptionOptions{}, func(ctx context.Context, _ json.RawMessage) (contract.EventResult, error) {
				calls.Add(1)
				close(entered)
				<-cancelNow
				if err := sub.Cancel(ctx); err != nil {
					t.Error(err)
				}
				close(canceled)
				<-release
				return contract.EventResult{}, nil
			})
			return err
		}})
		if err := active.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		emitted := make(chan error, 2)
		go func() { _, err := e.Emit(t.Context(), "self", nil, contract.Sequential); emitted <- err }()
		<-entered
		go func() { _, err := e.Emit(t.Context(), "self", nil, contract.Sequential); emitted <- err }()
		synctest.Wait()
		close(cancelNow)
		<-canceled
		stopped := make(chan error, 1)
		go func() { stopped <- active.Stop(t.Context()) }()
		synctest.Wait()
		select {
		case <-stopped:
			t.Fatal("stop did not wait for admitted callback")
		default:
		}
		if _, err := scope.On(t.Context(), "late", contract.SubscriptionOptions{}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
			return contract.EventResult{}, nil
		}); contract.ErrorCode(err) != contract.NotReady {
			t.Fatalf("late subscription: %v", err)
		}
		close(release)
		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
		<-emitted
		<-emitted
		if calls.Load() != 1 {
			t.Fatalf("queued callback ran after cancellation: %d", calls.Load())
		}
	})
}

func TestSubscriptionLimitReleasesCancelAndOnceSlots(t *testing.T) {
	e := core.New()
	defer closeEngine(t, e)
	var scope contract.Scope
	active := add(t, e, "bounded", contract.Definition{Start: func(_ context.Context, s contract.Scope) error { scope = s; return nil }})
	if err := active.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	handler := func(context.Context, json.RawMessage) (contract.EventResult, error) {
		return contract.EventResult{}, nil
	}
	for range 2 {
		subs := make([]contract.Subscription, contract.MaxSubscriptions)
		for n := range subs {
			var err error
			subs[n], err = scope.On(t.Context(), "bounded", contract.SubscriptionOptions{Once: true}, handler)
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := scope.On(t.Context(), "overflow", contract.SubscriptionOptions{}, handler); contract.ErrorCode(err) != contract.Denied {
			t.Fatalf("limit: %v", err)
		}
		if err := subs[0].Cancel(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := scope.On(t.Context(), "bounded", contract.SubscriptionOptions{Once: true}, handler); err != nil {
			t.Fatalf("cancel did not release slot: %v", err)
		}
		if results, err := e.Emit(t.Context(), "bounded", nil, contract.Sequential); err != nil || len(results) != contract.MaxSubscriptions {
			t.Fatalf("listeners damaged by limit: %d %v", len(results), err)
		}
		for _, sub := range subs {
			if err := sub.Cancel(t.Context()); err != nil {
				t.Fatalf("inactive cancellation: %v", err)
			}
		}
	}
}

func TestDynamicEventsKeepCrossInstanceCycleDetection(t *testing.T) {
	e := core.New()
	defer closeEngine(t, e)
	for _, pair := range [][2]string{{"a", "b"}, {"b", "a"}} {
		i := add(t, e, pair[0], contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
			_, err := s.On(ctx, pair[0], contract.SubscriptionOptions{}, func(ctx context.Context, raw json.RawMessage) (contract.EventResult, error) {
				_, err := s.Emit(ctx, pair[1], raw, contract.Sequential)
				return contract.EventResult{}, err
			})
			return err
		}})
		if err := i.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Emit(t.Context(), "a", nil, contract.Sequential); contract.ErrorCode(err) != contract.Reentrant {
		t.Fatalf("event cycle: %v", err)
	}
}
