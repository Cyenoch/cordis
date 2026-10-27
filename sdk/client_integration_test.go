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
	"github.com/Cyenoch/cordis/runtime/wasm"
)

func TestWASICommandAuthorSDK(t *testing.T) {
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker")
	if runtime.GOOS == "windows" {
		worker += ".exe"
	}
	plugin := filepath.Join(dir, "counter.wasm")
	build := exec.Command("go", "build", "-o", worker, "./cmd/cordis-worker")
	build.Dir = ".."
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("worker build %v: %s", err, data)
	}
	build = exec.Command("go", "build", "-o", plugin, ".")
	build.Dir = "../examples/plugins/counter"
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("author build %v: %s", err, data)
	}
	identity := contract.Identity{Package: "example.counter", Entry: "one", Activation: contract.NewID(), Digest: "fixture"}
	session, err := wasm.New(worker).Start(t.Context(), contract.Launch{FrameBytes: contract.MaxFrameBytes, Identity: identity, Directory: dir, Artifact: plugin, Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
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
		t.Fatalf("WASI SDK initialize: %v", err)
	}
	if _, err := session.Call(startup, "activation.publish", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	startupCancel()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	input, err = json.Marshal(contract.Invocation{Service: contract.Service{Name: "example.counter", Major: 1}, Input: json.RawMessage(`{"method":"execute","input":{"operation":"host","delta":0}}`)})
	if err != nil {
		t.Fatal(err)
	}
	data, err := session.Call(ctx, "call", input)
	if err != nil {
		t.Fatalf("WASI SDK callback: %v", err)
	}
	var result struct {
		Value  int    `json:"value"`
		Events int    `json:"events"`
		Host   string `json:"host"`
	}
	if err := contract.Decode(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Value != 4 || result.Host != "host callback" {
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
		t.Fatalf("WASI cancellation: %v", err)
	}
	if _, err := session.Call(ctx, "health", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("cancellation froze WASI control loop: %v", err)
	}
	if _, err := session.Call(ctx, "shutdown", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}
