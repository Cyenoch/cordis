//go:build integration

package sdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/runtime/native"
)

func TestNativeCommandAuthorSDK(t *testing.T) {
	dir := t.TempDir()
	plugin := filepath.Join(dir, "counter")
	if runtime.GOOS == "windows" {
		plugin += ".exe"
	}
	build := exec.Command("go", "build", "-o", plugin, ".")
	build.Dir = "../examples/plugins/counter"
	build.Env = os.Environ()
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("author build %v: %s", err, data)
	}
	identity := contract.Identity{Package: "example.counter", Entry: "one", Activation: contract.NewID(), Digest: "fixture"}
	session, err := native.New().Start(t.Context(), contract.Launch{FrameBytes: contract.MaxFrameBytes, Identity: identity, Directory: dir, Artifact: plugin, Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"message":"host callback"}`), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Error(err)
		}
	}()
	startup, startupCancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer startupCancel()
	input, err := json.Marshal(contract.Initialize{FrameBytes: contract.MaxFrameBytes, Identity: identity, Protocol: contract.Protocol{Major: 1}, Definition: "counter", Config: json.RawMessage(`{"initial":4}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Call(startup, "initialize", input); err != nil {
		t.Fatalf("native SDK initialize: %v", err)
	}
	if _, err := session.Call(startup, "activation.publish", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	startupCancel()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	input, err = json.Marshal(contract.Invocation{Service: contract.Service{Name: "example.counter", Major: 1}, Input: json.RawMessage(`{"method":"execute","input":{"operation":"get","delta":0}}`)})
	if err != nil {
		t.Fatal(err)
	}
	data, err := session.Call(ctx, "call", input)
	if err != nil {
		t.Fatalf("native SDK callback: %v", err)
	}
	var result struct {
		Value  int `json:"value"`
		Events int `json:"events"`
	}
	if err := contract.Decode(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Value != 4 {
		t.Fatalf("wrong guest result: %s", data)
	}
	waitInput, err := json.Marshal(contract.Invocation{Service: contract.Service{Name: "example.counter", Major: 1}, Input: json.RawMessage(`{"method":"execute","input":{"operation":"wait","delta":0}}`)})
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, stop := context.WithTimeout(t.Context(), 100*time.Millisecond)
	_, err = session.Call(waitCtx, "call", waitInput)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("native cancellation: %v", err)
	}
	if _, err := session.Call(ctx, "health", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("cancellation froze native control loop: %v", err)
	}
	if _, err := session.Call(ctx, "shutdown", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}
