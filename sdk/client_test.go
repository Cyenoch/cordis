package sdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
)

func TestExternalAuthorInitializationAndShutdown(t *testing.T) {
	left, right := net.Pipe()
	cleaned := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- sdk.RunTransport(t.Context(), contract.Definition{ID: "sample", Start: func(ctx context.Context, scope contract.Scope) error {
			greeting, err := sdk.Call[struct{}, struct {
				Value int `json:"value"`
			}](ctx, scope, contract.Requirement{Name: "greeting", Major: 1}, struct{}{})
			if err != nil {
				return err
			}
			if greeting.Value != 42 {
				return contract.Fail(contract.Invalid, "host result")
			}
			if err := scope.Defer(func(context.Context) error { close(cleaned); return nil }); err != nil {
				return err
			}
			return sdk.Provide(scope, contract.Service{Name: "sample.echo", Major: 1}, func(ctx context.Context, value int) (int, error) {
				if value < 0 {
					<-ctx.Done()
					return 0, ctx.Err()
				}
				return value + greeting.Value, nil
			})
		}}, right)
	}()
	host, err := wire.New(wire.Options{FrameBytes: contract.MaxFrameBytes, Transport: left, Identity: contract.Identity{Package: "example", Entry: "one", Activation: "activation"}, Handler: func(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
		if method != "call" {
			return nil, contract.Fail(contract.Invalid, "unexpected host method")
		}
		return json.RawMessage(`{"value":42}`), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := host.Close(); err != nil {
			t.Error(err)
		}
		host.Wait()
	}()
	init, err := json.Marshal(contract.Initialize{FrameBytes: contract.MaxFrameBytes, Identity: contract.Identity{Package: "example", Entry: "one", Activation: "activation"}, Protocol: contract.Protocol{Major: 1}, Definition: "sample", Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Call(t.Context(), "initialize", init); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Call(t.Context(), "activation.publish", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(contract.Invocation{Service: contract.Service{Name: "sample.echo", Major: 1}, Input: json.RawMessage(`8`)})
	if err != nil {
		t.Fatal(err)
	}
	output, err := host.Call(t.Context(), "call", payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "50" {
		t.Fatalf("typed result %s", output)
	}
	if _, err := host.Call(t.Context(), "shutdown", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cleaned:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not clean resources")
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("author SDK did not exit")
	}
}
