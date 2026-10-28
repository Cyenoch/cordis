package sdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
)

func TestOversizedRegistrationReportsDisconnectedCleanupFailure(t *testing.T) {
	left, right := net.Pipe()
	done := make(chan error, 1)
	cleanup := errors.New("remote cleanup could not finish")
	go func() {
		done <- sdk.ServeTransport(t.Context(), contract.Definition{ID: "budget", Start: func(_ context.Context, scope contract.Scope) error {
			if err := scope.Defer(func(context.Context) error { return cleanup }); err != nil {
				return err
			}
			return contract.Contribute(scope, "slot", "large", strings.Repeat("x", 8192))
		}}, right)
	}()
	host, err := wire.New(wire.Options{Transport: left, FrameBytes: contract.MinFrameBytes, Identity: contract.Identity{Activation: "a"}, Handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("unexpected")
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close(); host.Wait() })
	input, _ := json.Marshal(contract.Initialize{FrameBytes: contract.MinFrameBytes, Identity: contract.Identity{Activation: "a"}, Protocol: contract.Protocol{Major: 1}, Definition: "budget", Config: json.RawMessage(`{}`)})
	if _, err := host.Call(t.Context(), "initialize", input); err == nil {
		t.Fatal("oversized registration succeeded")
	}
	if err := <-done; !errors.Is(err, cleanup) {
		t.Fatalf("cleanup failure hidden: %v", err)
	}
}

func TestStartReverseCallUsesProposedBudget(t *testing.T) {
	left, right := net.Pipe()
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		done <- sdk.ServeTransport(t.Context(), contract.Definition{ID: "budget", Start: func(ctx context.Context, scope contract.Scope) error {
			close(started)
			_, err := scope.(contract.HostInvoker).InvokeHost(ctx, "large", json.RawMessage(`{}`))
			return err
		}}, right)
	}()
	host, err := wire.New(wire.Options{Transport: left, FrameBytes: contract.MinFrameBytes, Identity: contract.Identity{Activation: "a"}, Handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(strings.Repeat("x", 8192))
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close(); host.Wait() })
	input, _ := json.Marshal(contract.Initialize{FrameBytes: contract.MinFrameBytes, Identity: contract.Identity{Activation: "a"}, Protocol: contract.Protocol{Major: 1}, Definition: "budget", Config: json.RawMessage(`{}`)})
	if _, err := host.Call(t.Context(), "initialize", input); err == nil {
		t.Fatal("initialization survived oversized pre-ready reverse response")
	}
	<-started
	<-done
}
