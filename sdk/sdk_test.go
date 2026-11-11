package sdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
)

type settings struct {
	Label string `json:"label"`
}

// session runs definitions as a guest and returns the host's end.
func session(t *testing.T, ctx context.Context, definitions ...sdk.Definition) (*wire.Endpoint, <-chan error) {
	t.Helper()
	guestIn, hostOut := io.Pipe()
	hostIn, guestOut := io.Pipe()
	guest := &wire.Transport{Reader: guestIn, Writer: guestOut, Closers: []io.Closer{guestIn, guestOut}}
	host := &wire.Transport{Reader: hostIn, Writer: hostOut, Closers: []io.Closer{hostIn, hostOut}}
	endpoint, err := wire.New(wire.Options{FrameBytes: contract.MaxFrameBytes, Transport: host,
		Identity: contract.Identity{Package: "test", Entry: "entry", Activation: "activation"},
		Handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return nil, contract.Fail(contract.NotReady, "no host services in this test")
		}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sdk.RunTransport(ctx, guest, definitions...) }()
	t.Cleanup(func() {
		endpoint.Close()
		guest.Close()
	})
	return endpoint, done
}

func initialize(protocol uint32, definition, config string) json.RawMessage {
	data, _ := json.Marshal(contract.Initialize{FrameBytes: contract.MaxFrameBytes, Inflight: contract.DefaultInflight,
		Identity: contract.Identity{Package: "test", Entry: "entry", Activation: "activation"},
		Protocol: contract.Protocol{Major: protocol}, Definition: definition, Config: json.RawMessage(config),
		Services: []contract.Service{}, Provides: []contract.Service{},
		Features: []string{"cancel", "parent", "events", "children", "subscriptions"}})
	return data
}

func TestRunRejectsInvalidDefinitionSets(t *testing.T) {
	plugin := cordis.Plugin[settings]{Name: "main"}
	for name, definitions := range map[string][]sdk.Definition{
		"none":      nil,
		"duplicate": {sdk.Define(plugin), sdk.Define(plugin)},
		"unnamed":   {sdk.Define(cordis.Plugin[settings]{})},
	} {
		if err := sdk.RunTransport(t.Context(), nil, definitions...); !errors.Is(err, contract.Fail(contract.Invalid, "")) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestInitializeChecksBeforeItStartsThePlugin(t *testing.T) {
	started := false
	plugin := cordis.Plugin[settings]{Name: "main", Start: func(context.Context, *cordis.Scope, settings) error {
		started = true
		return nil
	}}
	for name, test := range map[string]struct {
		input json.RawMessage
		code  contract.Code
	}{
		"another protocol major": {initialize(contract.ProtocolMajor+1, "main", `{}`), contract.Incompatible},
		"unknown definition":     {initialize(contract.ProtocolMajor, "other", `{}`), contract.Invalid},
		"unknown config field":   {initialize(contract.ProtocolMajor, "main", `{"label":"x","extra":1}`), contract.Invalid},
		"config of another type": {initialize(contract.ProtocolMajor, "main", `{"label":1}`), contract.Invalid},
	} {
		t.Run(name, func(t *testing.T) {
			host, _ := session(t, t.Context(), sdk.Define(plugin))
			_, err := host.Call(t.Context(), "initialize", test.input)
			if !errors.Is(err, contract.Fail(test.code, "")) || started {
				t.Fatalf("initialize = %v, started = %v", err, started)
			}
		})
	}
}

func TestCancelingRunCleansUpAndReturns(t *testing.T) {
	cleaned := make(chan string, 2)
	plugin := cordis.Plugin[settings]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, c settings) error {
		s.Defer(func(context.Context) error {
			cleaned <- "cleanup " + c.Label
			return nil
		})
		s.Go(func(ctx context.Context) error {
			<-ctx.Done()
			cleaned <- "work ended"
			return nil
		})
		return nil
	}}
	ctx, cancel := context.WithCancel(t.Context())
	host, done := session(t, ctx, sdk.Define(plugin))
	if _, err := host.Call(t.Context(), "initialize", initialize(contract.ProtocolMajor, "main", `{"label":"x"}`)); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return")
	}
	if first, second := <-cleaned, <-cleaned; first != "work ended" || second != "cleanup x" {
		t.Fatalf("cleanup order: %q, %q", first, second)
	}
}

func TestShutdownAnswersAfterTheCleanups(t *testing.T) {
	cleaned := false
	plugin := cordis.Plugin[settings]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ settings) error {
		s.Defer(func(context.Context) error {
			time.Sleep(20 * time.Millisecond)
			cleaned = true
			return nil
		})
		return nil
	}}
	host, done := session(t, t.Context(), sdk.Define(plugin))
	if _, err := host.Call(t.Context(), "initialize", initialize(contract.ProtocolMajor, "main", `{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Call(t.Context(), "call", json.RawMessage(`{"service":{"name":"x","major":1,"minor":0},"method":"m","input":{}}`)); !errors.Is(err, contract.Fail(contract.NotReady, "")) {
		t.Fatalf("a call of a service the plugin does not provide = %v", err)
	}
	if _, err := host.Call(t.Context(), "shutdown", json.RawMessage(`{}`)); err != nil || !cleaned {
		t.Fatalf("shutdown = %v, cleaned = %v", err, cleaned)
	}
	host.Close()
	if err := <-done; err != nil {
		t.Fatalf("run = %v", err)
	}
}
