package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/wire"
)

func subscriptionClient(t *testing.T, handler wire.Handler) (*wire.Endpoint, *Client) {
	t.Helper()
	left, right := net.Pipe()
	clients := make(chan *Client, 1)
	done := make(chan error, 1)
	go func() {
		done <- RunTransport(t.Context(), contract.Definition{ID: "events", Start: func(_ context.Context, scope contract.Scope) error {
			clients <- scope.(*Client)
			return nil
		}}, right)
	}()
	host, err := wire.New(wire.Options{Transport: left, Identity: contract.Identity{Activation: "events"}, FrameBytes: contract.MinFrameBytes, Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		host.Close()
		host.Wait()
		if err := <-done; err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})
	raw, _ := json.Marshal(contract.Initialize{Identity: contract.Identity{Activation: "events"}, Protocol: contract.Protocol{Major: 1}, Definition: "events", Config: json.RawMessage(`{}`), FrameBytes: contract.MinFrameBytes})
	if _, err := host.Call(t.Context(), "initialize", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Call(t.Context(), "activation.publish", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	return host, <-clients
}

func TestCancelConfirmationPreservesAlreadyReceivedCallback(t *testing.T) {
	for _, once := range []bool{false, true} {
		t.Run(map[bool]string{false: "regular", true: "once"}[once], func(t *testing.T) {
			host, client := subscriptionClient(t, func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
				if method == "event.unsubscribe" {
					return json.RawMessage(`{"inactive":true}`), nil
				}
				return json.RawMessage(`{}`), nil
			})
			var calls atomic.Int32
			handle, err := client.On(t.Context(), "event", contract.SubscriptionOptions{Once: once}, func(ctx context.Context, _ json.RawMessage) (contract.EventResult, error) {
				calls.Add(1)
				return contract.EventResult{Handled: true, Value: json.RawMessage(`false`)}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			sub := handle.(*subscription)
			// Hold the business gate while the receive loop captures the
			// callback and processes cancellation on its control path.
			client.serial <- struct{}{}
			released := false
			t.Cleanup(func() {
				if !released {
					<-client.serial
				}
			})
			queued := make(chan struct{})
			delivered := make(chan error, 1)
			raw, _ := json.Marshal(contract.EventDelivery{ID: sub.spec.ID, Input: json.RawMessage(`null`)})
			go func() {
				_, err := host.CallWithReceipt(t.Context(), "event", raw, func(sent bool) {
					if !sent {
						t.Error("delivery was not queued")
					}
					close(queued)
				})
				delivered <- err
			}()
			<-queued
			// This response proves the earlier event frame passed Prepare.
			if _, err := host.Call(t.Context(), "health", json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			if err := handle.Cancel(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := handle.Cancel(t.Context()); err != nil {
				t.Fatal(err)
			}
			client.mu.Lock()
			retained := len(client.events) != 0 || sub.handler != nil
			client.mu.Unlock()
			if retained || calls.Load() != 0 {
				t.Fatalf("cancel retained registration or entered blocked callback: retained=%v calls=%d", retained, calls.Load())
			}
			<-client.serial
			released = true
			if err := <-delivered; err != nil || calls.Load() != 1 {
				t.Fatalf("admitted callback lost after cancel: calls=%d err=%v", calls.Load(), err)
			}
			if _, err := host.Call(t.Context(), "event", raw); contract.ErrorCode(err) != contract.Invalid {
				t.Fatalf("unknown subscription was delivered: %v", err)
			}
		})
	}
}

func TestUncertainSubscriptionControlClosesSession(t *testing.T) {
	for _, operation := range []string{"subscribe", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			host, client := subscriptionClient(t, func(ctx context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
				if method == "event."+map[string]string{"subscribe": "subscribe", "cancel": "unsubscribe"}[operation] {
					close(entered)
					<-release
					return nil, ctx.Err()
				}
				return json.RawMessage(`{}`), nil
			})
			t.Cleanup(func() { close(release) })
			var handle contract.Subscription
			if operation == "cancel" {
				var err error
				handle, err = client.On(t.Context(), "event", contract.SubscriptionOptions{}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
					return contract.EventResult{}, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			result := make(chan error, 1)
			go func() {
				if handle != nil {
					result <- handle.Cancel(ctx)
				} else {
					_, err := client.On(ctx, "event", contract.SubscriptionOptions{}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
						return contract.EventResult{}, nil
					})
					result <- err
				}
			}()
			<-entered
			cancel()
			if err := <-result; err == nil {
				t.Fatal("uncertain control reported success")
			}
			<-host.Done()
			<-client.closeDone
			client.mu.Lock()
			retained := len(client.events)
			client.mu.Unlock()
			if retained != 0 {
				t.Fatalf("session cleanup retained %d subscriptions", retained)
			}
		})
	}
}

func TestHostRetiresUndeliveredOnceReference(t *testing.T) {
	host, client := subscriptionClient(t, func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})
	handle, err := client.On(t.Context(), "once", contract.SubscriptionOptions{Once: true}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
		t.Error("undelivered once executed")
		return contract.EventResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sub := handle.(*subscription)
	for _, id := range []string{"other/1", "events/01", ""} {
		raw, _ := json.Marshal(contract.SubscriptionCancel{ID: id})
		if _, err := host.Call(t.Context(), "event.unsubscribe", raw); contract.ErrorCode(err) != contract.Invalid {
			t.Fatalf("invalid retirement %q: %v", id, err)
		}
	}
	raw, _ := json.Marshal(contract.SubscriptionCancel{ID: sub.spec.ID})
	if _, err := host.Call(t.Context(), "event.unsubscribe", raw); err != nil {
		t.Fatal(err)
	}
	if err := handle.Cancel(t.Context()); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	retained := len(client.events) != 0 || sub.handler != nil
	client.mu.Unlock()
	if retained {
		t.Fatal("undelivered once retained its callback")
	}
	if _, err := host.Call(t.Context(), "activation.retire", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.On(t.Context(), "late", contract.SubscriptionOptions{}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
		return contract.EventResult{}, nil
	}); contract.ErrorCode(err) != contract.NotReady {
		t.Fatalf("retired SDK registered a listener: %v", err)
	}
}

func TestUnsentSubscriptionDoesNotCloseHealthySession(t *testing.T) {
	host, client := subscriptionClient(t, func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})
	handler := func(context.Context, json.RawMessage) (contract.EventResult, error) {
		return contract.EventResult{}, nil
	}
	if _, err := client.On(t.Context(), strings.Repeat("x", contract.MinFrameBytes), contract.SubscriptionOptions{}, handler); contract.ErrorCode(err) != contract.Invalid {
		t.Fatalf("oversized registration: %v", err)
	}
	if _, err := client.On(t.Context(), "valid", contract.SubscriptionOptions{}, handler); err != nil {
		t.Fatalf("unsent registration damaged the session: %v", err)
	}
	if _, err := host.Call(t.Context(), "health", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	retained := len(client.events)
	client.mu.Unlock()
	if retained != 1 {
		t.Fatalf("unsent registration retained callback: %d", retained)
	}
}
