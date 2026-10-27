package sdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
)

func TestCanceledInitializationDrainsStartBeforeCleaningLateResources(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		left, right := net.Pipe()
		entered := make(chan struct{})
		unwinding := make(chan struct{})
		release := make(chan struct{})
		registered := make(chan error, 1)
		serveDone := make(chan error, 1)
		var starting atomic.Bool
		var overlap atomic.Bool
		var cleanupOrder []string
		var serviceCalls atomic.Int32
		service := contract.Service{Name: "late.resource", Major: 1}
		definition := contract.Definition{ID: "resource", Start: func(ctx context.Context, scope contract.Scope) error {
			starting.Store(true)
			defer starting.Store(false)
			if err := scope.Provide(service, func(context.Context, json.RawMessage) (json.RawMessage, error) {
				serviceCalls.Add(1)
				return json.RawMessage(`{"active":true}`), nil
			}); err != nil {
				return err
			}
			cleanup := func(name string) contract.Cleanup {
				return func(context.Context) error {
					if starting.Load() {
						overlap.Store(true)
					}
					cleanupOrder = append(cleanupOrder, name)
					return nil
				}
			}
			if err := scope.Defer(cleanup("early")); err != nil {
				return err
			}
			close(entered)
			<-ctx.Done()
			close(unwinding)
			<-release
			// A resource acquisition may finish while Start unwinds cancellation.
			// Its cleanup must join the same serialized, reverse-order drain.
			err := scope.Defer(cleanup("late"))
			registered <- err
			return err
		}}
		go func() { serveDone <- sdk.ServeTransport(t.Context(), definition, right) }()
		host, err := wire.New(wire.Options{FrameBytes: contract.MaxFrameBytes,
			Transport: left,
			Identity:  contract.Identity{Activation: "cancel-start"},
			Limits:    wire.Limits{Grace: time.Minute},
			Handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				return nil, contract.Fail(contract.Invalid, "unexpected guest request")
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := host.Close(); err != nil {
				t.Error(err)
			}
			host.Wait()
		}()
		input, err := json.Marshal(contract.Initialize{FrameBytes: contract.MaxFrameBytes,
			Identity: contract.Identity{Activation: "cancel-start"}, Protocol: contract.Protocol{Major: 1},
			Definition: "resource", Config: json.RawMessage(`{}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		initCtx, cancelInit := context.WithCancel(t.Context())
		defer cancelInit()
		initDone := make(chan error, 1)
		go func() {
			_, err := host.Call(initCtx, "initialize", input)
			initDone <- err
		}()
		<-entered
		cancelInit()
		if err := <-initDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("initialize cancellation: %v", err)
		}
		<-unwinding
		shutdownCtx, cancelShutdown := context.WithTimeout(t.Context(), time.Second)
		defer cancelShutdown()
		shutdownDone := make(chan error, 1)
		go func() {
			_, err := host.Call(shutdownCtx, "shutdown", json.RawMessage(`{}`))
			shutdownDone <- err
		}()
		synctest.Wait()
		if len(cleanupOrder) != 0 || overlap.Load() {
			t.Errorf("shutdown cleaned resources while Start was unwinding: %v", cleanupOrder)
		}
		time.Sleep(2 * time.Second)
		if err := <-shutdownDone; !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("shutdown while Start remains blocked: %v", err)
		}
		if len(cleanupOrder) != 0 || overlap.Load() {
			t.Errorf("timed-out shutdown cleaned resources before Start returned: %v", cleanupOrder)
		}
		close(release)
		if err := <-registered; err != nil {
			t.Errorf("late resource could not register cleanup during unwind: %v", err)
		}
		synctest.Wait()
		if overlap.Load() {
			t.Error("resource cleanup ran concurrently with Start")
		}
		if len(cleanupOrder) != 2 || cleanupOrder[0] != "late" || cleanupOrder[1] != "early" {
			t.Errorf("cleanup order = %v, want [late early]", cleanupOrder)
		}
		// The late nil Start result must not resurrect this closed SDK client.
		if _, err := host.Call(t.Context(), "initialize", input); err == nil {
			t.Error("closed client accepted initialization after canceled Start returned success")
		}
		payload, err := json.Marshal(contract.Invocation{Service: service, Input: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := host.Call(t.Context(), "call", payload); err == nil {
			t.Error("canceled initialization published its service")
		}
		if serviceCalls.Load() != 0 {
			t.Error("closed SDK executed a service handler")
		}
		if err := host.Close(); err != nil {
			t.Fatal(err)
		}
		if err := <-serveDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("SDK transport exit: %v", err)
		}
	})
}
