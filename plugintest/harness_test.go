package plugintest_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/plugintest"
	"github.com/Cyenoch/cordis/schema"
	"github.com/Cyenoch/cordis/sdk"
)

type businessFunc func(context.Context, contract.Requirement, json.RawMessage) (json.RawMessage, error)

func (f businessFunc) Call(ctx context.Context, required contract.Requirement, input json.RawMessage) (json.RawMessage, error) {
	return f(ctx, required, input)
}

type echoFunc func(context.Context, EchoEchoRequest) (EchoEchoResponse, error)

func (f echoFunc) Echo(ctx context.Context, input EchoEchoRequest) (EchoEchoResponse, error) {
	return f(ctx, input)
}

func TestHarnessUsesRealStartConfigurationGeneratedCodecAndPorts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var startup, activation context.Context
		var cleaned, calls atomic.Int32
		dependency := contract.Requirement{Name: "prefix", Major: 1}
		definition := contract.Definition{ID: "echo", Start: func(ctx context.Context, scope contract.Scope) error {
			startup, activation = ctx, scope.Context()
			var config struct {
				Suffix string `json:"suffix"`
			}
			if err := contract.DecodeData(scope.Config(), &config); err != nil {
				return err
			}
			if err := scope.Defer(func(ctx context.Context) error {
				if ctx.Err() != nil || activation.Err() == nil {
					return errors.New("cleanup did not receive an independent context after retirement")
				}
				_, err := sdk.Call[string, string](ctx, scope, dependency, "release")
				if err == nil {
					cleaned.Add(1)
				}
				return err
			}); err != nil {
				return err
			}
			return RegisterEchoServer(scope, echoFunc(func(ctx context.Context, request EchoEchoRequest) (EchoEchoResponse, error) {
				calls.Add(1)
				prefix, err := sdk.Call[string, string](ctx, scope, dependency, string(request.Text))
				if err != nil {
					return "", err
				}
				return EchoEchoResponse(strings.ToUpper(prefix) + config.Suffix), nil
			}))
		}}
		options := plugintest.Options{
			Config: json.RawMessage(`{"suffix":"!"}`),
			Business: businessFunc(func(_ context.Context, required contract.Requirement, input json.RawMessage) (json.RawMessage, error) {
				if required != dependency {
					return nil, errors.New("unexpected dependency")
				}
				var text string
				if err := contract.DecodeData(input, &text); err != nil {
					return nil, err
				}
				return contract.Encode("prefix " + text)
			}),
		}
		h, err := plugintest.New(t.Context(), definition, options)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := h.Close(context.WithoutCancel(t.Context())); err != nil {
				t.Error(err)
			}
		})
		if startup.Err() != context.Canceled || activation.Err() != nil {
			t.Fatalf("startup=%v activation=%v", startup.Err(), activation.Err())
		}
		client, err := NewEchoClient(h, EchoConsumer(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		out, err := client.Echo(t.Context(), EchoEchoRequest{Text: "hello"})
		if err != nil || out != "PREFIX HELLO!" {
			t.Fatalf("generated call = %q, %v", out, err)
		}
		for _, input := range []string{`{"text":""}`, `{"text":"valid","extra":true}`, `{"text":null}`} {
			_, err := h.Call(t.Context(), contract.Requirement{Name: "echo", Major: 1},
				json.RawMessage(`{"method":"echo","input":`+input+`}`))
			if err == nil {
				t.Errorf("generated server accepted %s", input)
			}
		}
		if calls.Load() != 1 {
			t.Fatalf("invalid requests reached business code: calls=%d", calls.Load())
		}
		if err := h.Retire(t.Context()); err != nil {
			t.Fatal(err)
		}
		if cleaned.Load() != 0 {
			t.Fatal("retirement released resources early")
		}
		for range 2 {
			if err := h.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		if cleaned.Load() != 1 {
			t.Fatalf("cleanup calls = %d", cleaned.Load())
		}
	})
}

func TestHarnessRejectsConfigurationBeforeStartAndCleansFailedStart(t *testing.T) {
	configSchema, err := schema.Compile(json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var starts atomic.Int32
	definition := contract.Definition{ID: "config", Validate: configSchema.Validate, Start: func(context.Context, contract.Scope) error {
		starts.Add(1)
		return nil
	}}
	if _, err := plugintest.New(t.Context(), definition, plugintest.Options{Config: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("missing required configuration accepted")
	}
	if starts.Load() != 0 {
		t.Fatal("invalid configuration reached Start")
	}
	cleanupErr := errors.New("cleanup after failed start")
	var cleaned atomic.Int32
	definition.Validate = nil
	definition.Start = func(_ context.Context, scope contract.Scope) error {
		if err := scope.Defer(func(context.Context) error { cleaned.Add(1); return cleanupErr }); err != nil {
			return err
		}
		return errors.New("start failed")
	}
	if _, err := plugintest.New(t.Context(), definition, plugintest.Options{}); !errors.Is(err, cleanupErr) {
		t.Fatalf("failed initialization lost cleanup cause: %v", err)
	}
	if cleaned.Load() != 1 {
		t.Fatalf("failed Start cleanup calls = %d", cleaned.Load())
	}
}

func TestHarnessMissingPortFailsAndManagedCauseIsVisible(t *testing.T) {
	definition := contract.Definition{ID: "needs-host", Start: func(ctx context.Context, scope contract.Scope) error {
		_, err := sdk.Call[struct{}, string](ctx, scope, contract.Requirement{Name: "required", Major: 1}, struct{}{})
		return err
	}}
	if _, err := plugintest.New(t.Context(), definition, plugintest.Options{}); err == nil {
		t.Fatal("missing port returned a fake successful result")
	}
	synctest.Test(t, func(t *testing.T) {
		cause := errors.New("background cause")
		release := make(chan struct{})
		definition.Start = func(_ context.Context, scope contract.Scope) error {
			return contract.Go(scope, func(context.Context) error { <-release; return cause })
		}
		h, err := plugintest.New(t.Context(), definition, plugintest.Options{})
		if err != nil {
			t.Fatal(err)
		}
		close(release)
		if err := h.Wait(t.Context()); !errors.Is(err, cause) {
			t.Fatalf("managed cause lost: %v", err)
		}
		if err := h.Close(t.Context()); !errors.Is(err, cause) {
			t.Fatalf("Close lost managed cause: %v", err)
		}
	})
}
