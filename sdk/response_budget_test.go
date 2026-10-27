package sdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
)

func TestSDKUsesHostBudgetForResultsAndErrors(t *testing.T) {
	for _, budget := range []int{contract.MinFrameBytes, 16384} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			left, right := net.Pipe()
			done := make(chan error, 1)
			count := 0
			service := contract.Service{Name: "budget", Major: 1}
			go func() {
				done <- sdk.ServeTransport(t.Context(), contract.Definition{ID: "budget", Start: func(_ context.Context, scope contract.Scope) error {
					return scope.Provide(service, func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
						switch string(input) {
						case `"large"`:
							count++
							return json.RawMessage(`"` + strings.Repeat("<", budget) + `"`), nil
						case `"error"`:
							count++
							return nil, contract.Fail(contract.Internal, strings.Repeat("private", budget))
						default:
							return json.Marshal(count)
						}
					})
				}}, right)
			}()
			host, err := wire.New(wire.Options{Transport: left, FrameBytes: budget, Identity: contract.Identity{Activation: "sdk-budget"}, Handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				return nil, errors.New("unexpected callback")
			}})
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
			init, _ := json.Marshal(contract.Initialize{FrameBytes: budget, Identity: contract.Identity{Activation: "sdk-budget"}, Protocol: contract.Protocol{Major: 1}, Definition: "budget", Config: json.RawMessage(`{}`)})
			registration, err := host.Call(t.Context(), "initialize", init)
			if err != nil {
				t.Fatal(err)
			}
			var registered contract.Registration
			if err := contract.Decode(registration, &registered); err != nil || registered.FrameBytes != budget {
				t.Fatalf("budget confirmation: %s, %v", registration, err)
			}
			if _, err := host.Call(t.Context(), "activation.publish", json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			for _, input := range []string{`"large"`, `"error"`} {
				request, _ := json.Marshal(contract.Invocation{Service: service, Input: json.RawMessage(input)})
				_, err := host.Call(t.Context(), "call", request)
				coded, ok := errors.AsType[*contract.Error](err)
				if !ok || coded.Code != contract.PluginFailed || strings.Contains(err.Error(), "private") {
					t.Fatalf("oversized result: %v", err)
				}
			}
			request, _ := json.Marshal(contract.Invocation{Service: service, Input: json.RawMessage(`"read"`)})
			if output, err := host.Call(t.Context(), "call", request); err != nil || string(output) != "2" {
				t.Fatalf("effects replayed or session lost: %s, %v", output, err)
			}
			if _, err := host.Call(t.Context(), "shutdown", nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
