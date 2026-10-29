package sdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
	"net"
	"sync/atomic"
	"testing"
)

func TestEventDeliveryTargetsOneRegisteredListener(t *testing.T) {
	left, right := net.Pipe()
	var later atomic.Int32
	registered := make(chan string, 2)
	done := make(chan error, 1)
	go func() {
		done <- sdk.RunTransport(t.Context(), contract.Definition{ID: "audit", Start: func(ctx context.Context, s contract.Scope) error {
			if _, err := s.On(ctx, "audit", contract.SubscriptionOptions{}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
				return contract.EventResult{}, contract.Fail(contract.Invalid, "first failed")
			}); err != nil {
				return err
			}
			_, subscribeErr := s.On(ctx, "audit", contract.SubscriptionOptions{}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
				later.Add(1)
				return contract.EventResult{}, nil
			})
			return subscribeErr
		}}, right)
	}()
	host, err := wire.New(wire.Options{FrameBytes: contract.MaxFrameBytes, Transport: left, Identity: contract.Identity{Activation: "audit"}, Handler: func(_ context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
		if method != "event.subscribe" {
			return nil, contract.Fail(contract.Invalid, "unexpected")
		}
		var spec contract.SubscriptionSpec
		if err := contract.Decode(input, &spec); err != nil {
			return nil, err
		}
		registered <- spec.ID
		return json.RawMessage(`{}`), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		host.Close()
		host.Wait()
		if err := <-done; err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	}()
	raw, _ := json.Marshal(contract.Initialize{FrameBytes: contract.MaxFrameBytes, Identity: contract.Identity{Activation: "audit"}, Protocol: contract.Protocol{Major: 1}, Definition: "audit", Config: json.RawMessage(`{}`)})
	if _, err := host.Call(t.Context(), "initialize", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Call(t.Context(), "activation.publish", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(contract.EventDelivery{ID: <-registered, Input: json.RawMessage(`{}`)})
	_, err = host.Call(t.Context(), "event", raw)
	if contract.ErrorCode(err) != contract.Invalid || later.Load() != 0 {
		t.Fatalf("delivery crossed listener identities: %v, later=%d", err, later.Load())
	}
	raw, _ = json.Marshal(contract.EventDelivery{ID: <-registered, Input: json.RawMessage(`{}`)})
	if _, err := host.Call(t.Context(), "event", raw); err != nil || later.Load() != 1 {
		t.Fatalf("second listener delivery: %v, later=%d", err, later.Load())
	}
	if _, err := host.Call(t.Context(), "shutdown", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}
