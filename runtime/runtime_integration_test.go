//go:build integration

package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/runtime/native"
	"github.com/Cyenoch/cordis/runtime/wasm"
	"github.com/Cyenoch/cordis/wire"
)

func build(t *testing.T, output, source string, wasi bool) {
	t.Helper()
	command := exec.Command("go", "build", "-o", output, source)
	command.Dir = ".."
	command.Env = os.Environ()
	if wasi {
		command.Env = append(command.Env, "GOOS=wasip1", "GOARCH=wasm")
	}
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build real fixture: %v\n%s", err, data)
	}
}

func TestExternalRuntimeIsolation(t *testing.T) {
	root := t.TempDir()
	nativePath := filepath.Join(root, "faultplugin")
	workerPath := filepath.Join(root, "cordis-worker")
	if runtime.GOOS == "windows" {
		nativePath += ".exe"
		workerPath += ".exe"
	}
	wasmPath := filepath.Join(root, "faultplugin.wasm")
	build(t, nativePath, "./runtime/testdata/faultplugin", false)
	build(t, wasmPath, "./runtime/testdata/faultplugin", true)
	build(t, workerPath, "./cmd/cordis-worker", false)
	// Package extraction deliberately ignores executable ZIP mode.
	if err := os.Chmod(nativePath, 0600); err != nil {
		t.Fatal(err)
	}
	limits := wire.Limits{Deadline: time.Second, Grace: 50 * time.Millisecond}
	cases := []struct {
		name, artifact string
		runtime        contract.Runtime
	}{
		{"native", nativePath, native.New(native.Options{Limits: limits, Grace: 100 * time.Millisecond, StderrBytes: 1024})},
		{"wasm", wasmPath, wasm.New(workerPath, wasm.Options{Limits: limits, Grace: 100 * time.Millisecond, StderrBytes: 1024})},
	}
	t.Setenv("CORDIS_RUNTIME_TEST_SECRET", "inherited-secret-must-not-leak")
	secretFile := filepath.Join(root, "private-secret")
	if err := os.WriteFile(secretFile, []byte("private secret"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			start := func(t *testing.T) contract.Session {
				t.Helper()
				startup, cancel := context.WithCancel(context.Background())
				identity := contract.Identity{Package: "faultplugin", Entry: "instance", Activation: contract.NewID(), Digest: "fixture"}
				session, err := test.runtime.Start(startup, contract.Launch{FrameBytes: contract.MaxFrameBytes,
					Identity:  identity,
					Directory: root, Artifact: test.artifact,
					Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
						return nil, contract.Fail(contract.Denied, "fixture has no host grants")
					},
				})
				cancel() // Start ctx is NOT the activation's lifetime.
				if err != nil {
					t.Fatalf("start: %v", err)
				}
				t.Cleanup(func() { session.Close() })
				ctx, done := context.WithTimeout(context.Background(), 40*time.Second)
				defer done()
				input, err := json.Marshal(contract.Initialize{FrameBytes: contract.MaxFrameBytes, Identity: identity, Protocol: contract.Protocol{Major: 1}, Definition: "fixture", Config: json.RawMessage(`{}`)})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := session.Call(ctx, "initialize", input); err != nil {
					t.Fatalf("initialize real command: %v", err)
				}
				return session
			}
			call := func(session contract.Session, method string, input json.RawMessage) json.RawMessage {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				output, err := session.Call(ctx, method, input)
				if err != nil {
					t.Fatalf("%s: %v", method, err)
				}
				return output
			}
			first, second := start(t), start(t)
			if got := string(call(first, "sum", json.RawMessage(`[4,8,15,16,23,42]`))); got != "108" {
				t.Fatalf("sum: %s", got)
			}
			if got := string(call(first, "counter", nil)); got != "1" {
				t.Fatalf("first counter: %s", got)
			}
			if got := string(call(first, "counter", nil)); got != "2" {
				t.Fatalf("first counter: %s", got)
			}
			if got := string(call(second, "counter", nil)); got != "1" {
				t.Fatalf("separate instance shares state: %s", got)
			}
			probe, err := json.Marshal(map[string]string{"path": secretFile})
			if err != nil {
				t.Fatal(err)
			}
			var observed struct {
				Secret         string `json:"secret"`
				FileAccessible bool   `json:"fileAccessible"`
				Cwd            string `json:"cwd"`
			}
			if err := json.Unmarshal(call(second, "probe", probe), &observed); err != nil {
				t.Fatal(err)
			}
			if observed.Secret != "" {
				t.Fatal("parent environment leaked into plugin")
			}
			if test.name == "wasm" && observed.FileAccessible {
				t.Fatal("WASI module could read un-preopened host path")
			}
			if test.name == "native" && (observed.Cwd == root || observed.Cwd == "") {
				t.Fatal("native command lacks private cwd")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, err = first.Call(ctx, "panic", nil)
			cancel()
			if err == nil {
				t.Fatal("panic succeeded")
			}
			awaitReaped(t, first)
			if first.Err() == nil {
				t.Fatal("panic exit lost its failure reason")
			}
			if strings.Contains(first.Err().Error(), "runtime-test-secret") {
				t.Fatal("guest stderr leaked in diagnostics")
			}
			if got := string(call(second, "counter", nil)); got != "2" {
				t.Fatalf("other instance was damaged by panic: %s", got)
			}
			for _, fault := range []string{"loop", "block", "malformed", "stderr"} {
				t.Run(fault, func(t *testing.T) {
					session := start(t)
					ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
					_, err := session.Call(ctx, fault, nil)
					cancel()
					if err == nil {
						t.Fatal("fault unexpectedly succeeded")
					}
					awaitReaped(t, session)
					if session.Err() == nil {
						t.Fatal("fault lost diagnostic")
					}
					if strings.Contains(session.Err().Error(), "runtime-test-secret") {
						t.Fatal("untrusted guest output leaked")
					}
				})
			}
			t.Run("unexpected-zero-exit", func(t *testing.T) {
				session := start(t)
				if _, err := session.Call(t.Context(), "exit-zero", nil); err == nil {
					t.Fatal("unexpected exit returned success")
				}
				awaitReaped(t, session)
				if session.Err() == nil {
					t.Fatal("unexpected successful process exit lost activation failure")
				}
			})
			if test.name == "native" {
				t.Run("managed-descendant", func(t *testing.T) {
					session := start(t)
					marker := filepath.Join(t.TempDir(), "heartbeat")
					payload, err := json.Marshal(map[string]string{"path": marker})
					if err != nil {
						t.Fatal(err)
					}
					call(session, "spawn-child", payload)
					if err := session.Close(); err != nil {
						t.Fatal(err)
					}
					before, err := os.ReadFile(marker)
					if err != nil {
						t.Fatal(err)
					}
					time.Sleep(50 * time.Millisecond)
					after, err := os.ReadFile(marker)
					if err != nil {
						t.Fatal(err)
					}
					if string(before) != string(after) {
						t.Fatal("managed child continues writing after process reaping")
					}
				})
			}
			t.Run("close-active-call", func(t *testing.T) {
				session := start(t)
				result := make(chan error, 1)
				go func() { _, err := session.Call(context.Background(), "loop", nil); result <- err }()
				if err := session.Close(); err != nil {
					t.Fatalf("forced close: %v", err)
				}
				awaitReaped(t, session)
				select {
				case err := <-result:
					if err == nil {
						t.Fatal("closed call succeeded")
					}
				case <-time.After(2 * time.Second):
					t.Fatal("close stranded a call")
				}
				if err := session.Close(); err != nil {
					t.Fatalf("repeated close: %v", err)
				}
			})
			t.Run("explicit-lifetime", func(t *testing.T) {
				lifetime, stop := context.WithCancel(context.Background())
				defer stop()
				var adapter contract.Runtime
				if test.name == "native" {
					adapter = native.New(native.Options{Lifetime: lifetime, Grace: 100 * time.Millisecond})
				} else {
					adapter = wasm.New(workerPath, wasm.Options{Lifetime: lifetime, Grace: 100 * time.Millisecond})
				}
				session, err := adapter.Start(context.Background(), contract.Launch{FrameBytes: contract.MaxFrameBytes,
					Identity: contract.Identity{Activation: contract.NewID()}, Directory: root, Artifact: test.artifact,
					Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
						return nil, contract.Fail(contract.Denied, "no grants")
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { session.Close() })
				stop()
				awaitReaped(t, session)
				if !errors.Is(session.Err(), context.Canceled) {
					t.Fatalf("lifetime cause lost: %v", session.Err())
				}
			})
			if err := second.Close(); err != nil {
				t.Fatalf("close healthy activation: %v", err)
			}
		})
	}
}

func awaitReaped(t *testing.T, session contract.Session) {
	t.Helper()
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not terminate and reap within bound")
	}
}

func TestNativeStartupRejectsInvalidPaths(t *testing.T) {
	runtime := native.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runtime.Start(ctx, contract.Launch{FrameBytes: contract.MaxFrameBytes})
	if err == nil {
		t.Fatal("canceled or empty launch accepted")
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(outside, []byte("not executable"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Start(context.Background(), contract.Launch{FrameBytes: contract.MaxFrameBytes, Directory: root, Artifact: outside})
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatal("artifact escaped package root")
	}
}

func TestWASMWorkerLimits(t *testing.T) {
	root := t.TempDir()
	worker := filepath.Join(root, "cordis-worker")
	if runtime.GOOS == "windows" {
		worker += ".exe"
	}
	build(t, worker, "./cmd/cordis-worker", false)
	header := []byte{0, 97, 115, 109, 1, 0, 0, 0}
	types := []byte{1, 4, 1, 96, 0, 0}
	functions := []byte{3, 2, 1, 0}
	exports := []byte{7, 10, 1, 6, '_', 's', 't', 'a', 'r', 't', 0, 0}
	combine := func(parts ...[]byte) []byte {
		var result []byte
		for _, part := range parts {
			result = append(result, part...)
		}
		return result
	}
	fixtures := []struct {
		name    string
		binary  []byte
		options wasm.Options
	}{
		{
			name:    "startup-infinite-loop",
			binary:  combine(header, types, functions, exports, []byte{10, 9, 1, 7, 0, 3, 64, 12, 0, 11, 11}),
			options: wasm.Options{StartTimeout: 100 * time.Millisecond},
		},
		{
			name:    "memory-minimum-exceeds-limit",
			binary:  combine(header, types, functions, []byte{5, 3, 1, 0, 2}, exports, []byte{10, 4, 1, 2, 0, 11}),
			options: wasm.Options{MemoryPages: 1},
		},
		{
			name:   "arbitrary-host-import",
			binary: combine(header, types, []byte{2, 9, 1, 3, 'e', 'n', 'v', 1, 'x', 0, 0}, exports),
		},
		{
			name:    "artifact-byte-limit",
			binary:  combine(header, types, functions, exports, []byte{10, 4, 1, 2, 0, 11}),
			options: wasm.Options{ArtifactBytes: 8},
		},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			path := filepath.Join(root, fixture.name+".wasm")
			if err := os.WriteFile(path, fixture.binary, 0600); err != nil {
				t.Fatal(err)
			}
			session, err := wasm.New(worker, fixture.options).Start(context.Background(), contract.Launch{FrameBytes: contract.MaxFrameBytes,
				Identity: contract.Identity{Activation: contract.NewID()}, Directory: root, Artifact: path,
				Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
					return nil, contract.Fail(contract.Denied, "no host imports")
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { session.Close() })
			awaitReaped(t, session)
			if session.Err() == nil {
				t.Fatal("unsafe WASM command succeeded")
			}
		})
	}
}
