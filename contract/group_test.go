package contract_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func TestGroupCloseConcurrentSharesReverseSerialRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var group contract.Group
		failure := errors.New("session close failed")
		entered, release := make(chan struct{}), make(chan struct{})
		order := []string{}
		registerGroupCleanup(t, &group, func(context.Context) error {
			order = append(order, "connection")
			return nil
		})
		registerGroupCleanup(t, &group, func(context.Context) error {
			order = append(order, "session begin")
			close(entered)
			<-release
			order = append(order, "session end")
			return failure
		})
		results := make(chan error, 2)
		for range 2 {
			go func() { results <- group.Close(context.Background()) }()
		}
		<-entered
		synctest.Wait()
		if !slices.Equal(order, []string{"session begin"}) {
			t.Fatalf("earlier resource released before session completed: %v", order)
		}
		select {
		case err := <-results:
			t.Fatalf("close returned before release: %v", err)
		default:
		}
		var rejected atomic.Bool
		err := group.Defer(func(context.Context) error { rejected.Store(true); return nil })
		var coded *contract.Error
		if !errors.As(err, &coded) || coded.Code != contract.NotReady {
			t.Fatalf("registration during close: %v", err)
		}
		close(release)
		first, second := <-results, <-results
		if !errors.Is(first, failure) || first != second {
			t.Fatalf("close results do not share the cleanup failure: %v / %v", first, second)
		}
		if !slices.Equal(order, []string{"session begin", "session end", "connection"}) {
			t.Fatalf("cleanup order: %v", order)
		}
		if err := group.Close(context.Background()); err != first {
			t.Fatalf("repeated close did not preserve result: %v", err)
		}
		if err := group.Defer(func(context.Context) error { rejected.Store(true); return nil }); err == nil {
			t.Fatal("registration after close succeeded")
		}
		if rejected.Load() || len(order) != 3 {
			t.Fatalf("rejected or completed cleanup ran again: %v", order)
		}
	})
}

func TestGroupCloseCanceledWaiterDoesNotReleaseOrCancelCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var group contract.Group
		entered, release := make(chan struct{}), make(chan struct{})
		var released atomic.Bool
		type contextKey struct{}
		ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), contextKey{}, "binding"), time.Hour)
		defer cancel()
		registerGroupCleanup(t, &group, func(context.Context) error {
			released.Store(true)
			return nil
		})
		registerGroupCleanup(t, &group, func(cleanupCtx context.Context) error {
			close(entered)
			<-release
			if cleanupCtx.Err() != nil || cleanupCtx.Done() != nil {
				return errors.New("caller cancellation reached cleanup")
			}
			if _, ok := cleanupCtx.Deadline(); ok {
				return errors.New("caller deadline reached cleanup")
			}
			if cleanupCtx.Value(contextKey{}) != "binding" {
				return errors.New("cleanup lost context values")
			}
			return nil
		})
		canceled := make(chan error, 1)
		go func() { canceled <- group.Close(ctx) }()
		<-entered
		waiting := make(chan error, 1)
		go func() { waiting <- group.Close(context.Background()) }()
		synctest.Wait()
		cancel()
		if err := <-canceled; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled wait: %v", err)
		}
		synctest.Wait()
		if released.Load() {
			t.Fatal("cancellation released dependency before blocked cleanup completed")
		}
		select {
		case err := <-waiting:
			t.Fatalf("another waiter incorrectly completed: %v", err)
		default:
		}
		close(release)
		if err := <-waiting; err != nil {
			t.Fatal(err)
		}
		if !released.Load() {
			t.Fatal("cleanup did not finish after canceled caller left")
		}
		if err := group.Close(ctx); err != nil {
			t.Fatalf("completed close did not prefer cached result over cancellation: %v", err)
		}
	})
}

func TestGroupCloseAggregatesAsyncFailureAndPanicsWithoutSkippingCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var group contract.Group
		failure, panicCause := errors.New("async close failed"), errors.New("broken disposer")
		entered, release := make(chan struct{}), make(chan struct{})
		order := []int{}
		registerGroupCleanup(t, &group, func(context.Context) error { order = append(order, 1); return nil })
		registerGroupCleanup(t, &group, func(context.Context) error { order = append(order, 2); panic(panicCause) })
		registerGroupCleanup(t, &group, func(context.Context) error { order = append(order, 3); panic("string panic") })
		registerGroupCleanup(t, &group, func(context.Context) error {
			close(entered)
			<-release
			order = append(order, 4)
			return failure
		})
		result := make(chan error, 1)
		go func() { result <- group.Close(context.Background()) }()
		<-entered
		synctest.Wait()
		if len(order) != 0 {
			t.Fatalf("cleanup overlapped asynchronous disposer: %v", order)
		}
		close(release)
		err := <-result
		if !errors.Is(err, failure) || !errors.Is(err, panicCause) || !strings.Contains(err.Error(), "string panic") {
			t.Fatalf("lost cleanup errors or panics: %v", err)
		}
		if !slices.Equal(order, []int{4, 3, 2, 1}) {
			t.Fatalf("failure skipped or reordered cleanup: %v", order)
		}
		if again := group.Close(context.Background()); again != err || len(order) != 4 {
			t.Fatalf("failed release retried: %v / %v", again, order)
		}
	})
}

func TestNewGroupScopeOwnsNestedResourcesWhenInitializationFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine := core.New()
		startErr, cleanupErr := errors.New("initialization failed"), errors.New("session cleanup failed")
		service := contract.Service{Name: "resource-provider", Major: 1}
		requirement := contract.Requirement{Name: service.Name, Major: 1}
		var calls atomic.Int32
		provider, err := engine.Add(
			contract.Entry{ID: "provider", Enabled: true},
			contract.Definition{Provides: []contract.Service{service}, Start: func(_ context.Context, scope contract.Scope) error {
				return scope.Provide(service, func(context.Context, json.RawMessage) (json.RawMessage, error) {
					calls.Add(1)
					return json.RawMessage(`null`), nil
				})
			}},
			contract.Identity{},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := provider.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		order := []string{}
		var group *contract.Group
		instance, err := engine.Add(
			contract.Entry{ID: "consumer", Enabled: true},
			contract.Definition{Requires: []contract.Requirement{requirement}, Start: func(_ context.Context, scope contract.Scope) error {
				owned, err := contract.NewGroup(scope)
				if err != nil {
					return err
				}
				group = owned
				if err := group.Defer(func(ctx context.Context) error {
					order = append(order, "connection")
					_, err := scope.Call(ctx, requirement, json.RawMessage(`null`))
					return err
				}); err != nil {
					return err
				}
				session, err := contract.NewGroup(group)
				if err != nil {
					return err
				}
				if err := session.Defer(func(context.Context) error {
					order = append(order, "session")
					return cleanupErr
				}); err != nil {
					return err
				}
				return startErr
			}},
			contract.Identity{},
		)
		if err != nil {
			t.Fatal(err)
		}
		err = instance.Wait(t.Context())
		if !errors.Is(err, startErr) || !errors.Is(err, cleanupErr) {
			t.Fatalf("scope lost initialization or group cleanup failure: %v", err)
		}
		if instance.Status().State != contract.Failed || !strings.Contains(instance.Status().Error, cleanupErr.Error()) {
			t.Fatalf("failure diagnostics: %+v", instance.Status())
		}
		if !slices.Equal(order, []string{"session", "connection"}) || calls.Load() != 1 {
			t.Fatalf("nested cleanup order or Scope cleanup binding lost: %v, calls=%d", order, calls.Load())
		}
		if err := group.Close(t.Context()); !errors.Is(err, cleanupErr) || len(order) != 2 {
			t.Fatalf("scope-owned release ran again: %v / %v", err, order)
		}
		if err := engine.Close(t.Context()); !errors.Is(err, startErr) || !errors.Is(err, cleanupErr) {
			t.Fatalf("engine lost owned cleanup failure: %v", err)
		}
	})
}

func TestNewGroupRejectsClosedOwnerWithoutTransferringCleanup(t *testing.T) {
	var owner contract.Group
	if err := owner.Defer(nil); err == nil {
		t.Fatal("nil cleanup accepted")
	}
	if err := owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	group, err := contract.NewGroup(&owner)
	var coded *contract.Error
	if group != nil || !errors.As(err, &coded) || coded.Code != contract.NotReady {
		t.Fatalf("closed owner accepted new group: %v / %v", group, err)
	}
}

func registerGroupCleanup(t *testing.T, group *contract.Group, cleanup contract.Cleanup) {
	t.Helper()
	if err := group.Defer(cleanup); err != nil {
		t.Fatal(err)
	}
}
