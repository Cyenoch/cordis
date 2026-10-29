package sdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
)

type sdkTestSession struct {
	host *wire.Endpoint
	done chan struct{}
	err  error
}

func openSDKSession(t *testing.T, definition contract.Definition) *sdkTestSession {
	t.Helper()
	left, right := net.Pipe()
	session := &sdkTestSession{done: make(chan struct{})}
	go func() {
		session.err = sdk.RunTransport(t.Context(), definition, right)
		close(session.done)
	}()
	var err error
	session.host, err = wire.New(wire.Options{FrameBytes: contract.MaxFrameBytes,
		Transport: left, Identity: contract.Identity{Activation: "sdk-test"},
		Handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return nil, contract.Fail(contract.NotReady, "no host test port")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.host.Close()
		session.host.Wait()
		<-session.done
	})
	return session
}

func (s *sdkTestSession) initialize(t *testing.T, id string, budget int) error {
	t.Helper()
	data, err := json.Marshal(contract.Initialize{
		FrameBytes: budget, Identity: contract.Identity{Activation: "sdk-test"},
		Protocol: contract.Protocol{Major: 1}, Definition: id, Config: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.host.Call(t.Context(), "initialize", data)
	return err
}

func TestManagedWorkWaitsForPublicationAndRetiresOutsideBusinessSerial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		entered, returned := make(chan struct{}), make(chan struct{})
		var runs, cleanups atomic.Int32
		var startup, activation context.Context
		var captured contract.Scope
		service := contract.Service{Name: "managed", Major: 1}
		s := openSDKSession(t, contract.Definition{ID: "managed", Start: func(ctx context.Context, scope contract.Scope) error {
			startup, activation, captured = ctx, scope.Context(), scope
			if err := scope.Defer(func(context.Context) error { cleanups.Add(1); return nil }); err != nil {
				return err
			}
			if err := contract.Go(scope, func(ctx context.Context) error {
				runs.Add(1)
				close(started)
				<-ctx.Done()
				close(canceled)
				<-release
				return ctx.Err()
			}); err != nil {
				return err
			}
			return scope.Provide(service, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
				close(entered)
				<-ctx.Done()
				close(returned)
				return nil, ctx.Err()
			})
		}})
		if err := s.initialize(t, "managed", contract.MaxFrameBytes); err != nil {
			t.Fatal(err)
		}
		if startup.Err() != context.Canceled || activation.Err() != nil {
			t.Fatalf("startup=%v activation=%v", startup.Err(), activation.Err())
		}
		synctest.Wait()
		if runs.Load() != 0 {
			t.Fatal("managed work ran before host final publication")
		}
		data, _ := json.Marshal(contract.Invocation{Service: service, Input: json.RawMessage(`{}`)})
		if _, err := s.host.Call(t.Context(), "call", data); err == nil {
			t.Fatal("unpublished service accepted business")
		}
		if err := contract.Go(captured, func(context.Context) error { return nil }); err == nil {
			t.Fatal("work registration remained open after Start")
		}
		for range 2 {
			if _, err := s.host.Call(t.Context(), "activation.publish", json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
		}
		<-started
		businessDone := make(chan error, 1)
		go func() { _, err := s.host.Call(t.Context(), "call", data); businessDone <- err }()
		<-entered // The resident work does not hold the business serial position.
		for range 2 {
			if _, err := s.host.Call(t.Context(), "activation.retire", json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
		}
		<-canceled
		<-returned
		if err := <-businessDone; err == nil {
			t.Fatalf("retired business result = %v", err)
		}
		if cleanups.Load() != 0 || runs.Load() != 1 {
			t.Fatalf("retire released resources or repeated work: cleanups=%d runs=%d", cleanups.Load(), runs.Load())
		}
		if _, err := s.host.Call(t.Context(), "activation.publish", json.RawMessage(`{}`)); err == nil {
			t.Fatal("late publication revived a retired activation")
		}
		if _, err := s.host.Call(t.Context(), "call", data); err == nil {
			t.Fatal("retired service accepted business")
		}
		shutdownCtx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		shutdownDone := make(chan error, 1)
		go func() { _, err := s.host.Call(shutdownCtx, "shutdown", json.RawMessage(`{}`)); shutdownDone <- err }()
		if err := <-shutdownDone; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("uncooperative work was reported drained: %v", err)
		}
		if cleanups.Load() != 0 {
			t.Fatal("resources released before managed work returned")
		}
		close(release)
		synctest.Wait()
		if cleanups.Load() != 1 {
			t.Fatalf("cleanup calls = %d", cleanups.Load())
		}
	})
}

func TestUnpublishedActivationNeverRunsWorkOnRetire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var work, cleaned atomic.Int32
		s := openSDKSession(t, contract.Definition{ID: "rejected", Start: func(_ context.Context, scope contract.Scope) error {
			if err := scope.Defer(func(context.Context) error { cleaned.Add(1); return nil }); err != nil {
				return err
			}
			return contract.Go(scope, func(context.Context) error { work.Add(1); return nil })
		}})
		if err := s.initialize(t, "rejected", contract.MaxFrameBytes); err != nil {
			t.Fatal(err)
		}
		if _, err := s.host.Call(t.Context(), "activation.retire", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.host.Call(t.Context(), "activation.publish", json.RawMessage(`{}`)); err == nil {
			t.Fatal("publish after final rejection succeeded")
		}
		if _, err := s.host.Call(t.Context(), "shutdown", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		<-s.done
		if work.Load() != 0 || cleaned.Load() != 1 {
			t.Fatalf("work=%d cleanups=%d", work.Load(), cleaned.Load())
		}
	})
}

func TestManagedFailureClosesSessionAndPreservesCause(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panics], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cause := errors.New("managed failure detail")
				release := make(chan struct{})
				s := openSDKSession(t, contract.Definition{ID: "fails", Start: func(_ context.Context, scope contract.Scope) error {
					return contract.Go(scope, func(context.Context) error {
						<-release
						if panics {
							panic(cause)
						}
						return cause
					})
				}})
				if err := s.initialize(t, "fails", contract.MaxFrameBytes); err != nil {
					t.Fatal(err)
				}
				if _, err := s.host.Call(t.Context(), "activation.publish", json.RawMessage(`{}`)); err != nil {
					t.Fatal(err)
				}
				close(release)
				<-s.host.Done()
				<-s.done
				if !errors.Is(s.err, cause) || !strings.Contains(s.err.Error(), "managed work") {
					t.Fatalf("SDK lost managed error cause: %v", s.err)
				}
			})
		})
	}
}

func TestSDKCleanupPanicDoesNotSkipResourcesAndCloseIsShared(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cause := errors.New("cleanup failed")
		panicked := errors.New("cleanup panicked")
		var order []int
		s := openSDKSession(t, contract.Definition{ID: "cleanup", Start: func(_ context.Context, scope contract.Scope) error {
			for _, n := range []int{1, 2, 3} {
				if err := scope.Defer(func(ctx context.Context) error {
					if ctx.Err() != nil {
						t.Error("cleanup inherited cancellation")
					}
					order = append(order, n)
					if n == 2 {
						panic(panicked)
					}
					if n == 3 {
						return cause
					}
					return nil
				}); err != nil {
					return err
				}
			}
			return nil
		}})
		if err := s.initialize(t, "cleanup", contract.MaxFrameBytes); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err := s.host.Call(t.Context(), "shutdown", json.RawMessage(`{}`)); err == nil {
				t.Fatal("cleanup failures were hidden")
			}
		}
		_ = s.host.Close()
		<-s.done
		if !errors.Is(s.err, cause) || !errors.Is(s.err, panicked) || !reflect.DeepEqual(order, []int{3, 2, 1}) {
			t.Fatalf("cleanup order=%v error=%v", order, s.err)
		}
	})
}

func TestSDKRejectsUnknownDefinitionBeforeStart(t *testing.T) {
	var starts atomic.Int32
	s := openSDKSession(t, contract.Definition{ID: "budget", Start: func(context.Context, contract.Scope) error {
		starts.Add(1)
		return nil
	}})
	var invalid *contract.Error
	if err := s.initialize(t, "unknown", contract.MaxFrameBytes); !errors.As(err, &invalid) || invalid.Code != contract.Invalid {
		t.Fatalf("unknown definition was not rejected: %v", err)
	}
	if starts.Load() != 0 {
		t.Fatal("unknown definition reached author Start")
	}
}

func TestRetiredProviderAcceptsOnlyHostMarkedCleanup(t *testing.T) {
	service := contract.Service{Name: "cleanup-provider", Major: 1}
	session := openSDKSession(t, contract.Definition{ID: "cleanup", Start: func(_ context.Context, scope contract.Scope) error {
		return scope.Provide(service, func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return input, nil
		})
	}})
	if err := session.initialize(t, "cleanup", contract.MaxFrameBytes); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"activation.publish", "activation.retire"} {
		if _, err := session.host.Call(t.Context(), method, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	ordinary, _ := json.Marshal(contract.Invocation{Service: service, Input: json.RawMessage(`"ordinary"`)})
	if _, err := session.host.Call(t.Context(), "call", ordinary); err == nil {
		t.Fatal("retired ordinary business admitted")
	}
	cleanup, _ := json.Marshal(contract.Invocation{Service: service, Input: json.RawMessage(`"cleanup"`), Cleanup: true})
	if out, err := session.host.Call(t.Context(), "call", cleanup); err != nil || string(out) != `"cleanup"` {
		t.Fatalf("cleanup lost binding: %s %v", out, err)
	}
	if _, err := session.host.Call(t.Context(), "shutdown", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}
