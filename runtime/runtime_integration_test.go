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
	"github.com/Cyenoch/cordis/wire"
)

func build(t *testing.T, output, source string) {
	t.Helper()
	command := exec.Command("go", "build", "-o", output, source)
	command.Dir = ".."
	command.Env = os.Environ()
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build real fixture: %v\n%s", err, data)
	}
}

func TestExternalRuntimeIsolation(t *testing.T) {
	root := t.TempDir()
	nativePath := filepath.Join(root, "faultplugin")
	if runtime.GOOS == "windows" {
		nativePath += ".exe"
	}
	build(t, nativePath, "./runtime/testdata/faultplugin")
	// Package extraction deliberately ignores executable ZIP mode.
	if err := os.Chmod(nativePath, 0600); err != nil {
		t.Fatal(err)
	}
	limits := wire.Limits{Deadline: time.Second, Grace: 50 * time.Millisecond}
	adapter := native.New(native.Options{Limits: limits, Grace: 100 * time.Millisecond, StderrBytes: 1024})
	t.Setenv("CORDIS_RUNTIME_TEST_SECRET", "inherited-secret-must-not-leak")
	secretFile := filepath.Join(root, "private-secret")
	if err := os.WriteFile(secretFile, []byte("private secret"), 0600); err != nil {
		t.Fatal(err)
	}
	start := func(t *testing.T, configure ...func(*contract.Launch)) contract.Session {
		t.Helper()
		startup, cancel := context.WithCancel(context.Background())
		identity := contract.Identity{Package: "faultplugin", Entry: "instance", Activation: contract.NewID(), Digest: "fixture"}
		launch := contract.Launch{FrameBytes: contract.MaxFrameBytes,
			Identity:  identity,
			Directory: root, Artifact: nativePath,
			Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				return nil, contract.Fail(contract.Denied, "fixture has no service handler")
			},
		}
		for _, apply := range configure {
			apply(&launch)
		}
		session, err := adapter.Start(startup, launch)
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
	call := func(t *testing.T, session contract.Session, method string, input json.RawMessage) json.RawMessage {
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
	if got := string(call(t, first, "sum", json.RawMessage(`[4,8,15,16,23,42]`))); got != "108" {
		t.Fatalf("sum: %s", got)
	}
	if got := string(call(t, first, "counter", nil)); got != "1" {
		t.Fatalf("first counter: %s", got)
	}
	if got := string(call(t, first, "counter", nil)); got != "2" {
		t.Fatalf("first counter: %s", got)
	}
	if got := string(call(t, second, "counter", nil)); got != "1" {
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
	if err := json.Unmarshal(call(t, second, "probe", probe), &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Secret != "" {
		t.Fatal("parent environment leaked into plugin")
	}
	if observed.Cwd == root || observed.Cwd == "" {
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
	panicOutcome := first.(contract.ProcessSession).Outcome()
	if panicOutcome.Reason != contract.ExitUnexpected || panicOutcome.ExitCode == nil || *panicOutcome.ExitCode == 0 {
		t.Fatalf("panic exit facts: %+v", panicOutcome)
	}
	if runtime.GOOS == "windows" && panicOutcome.Signal != "" {
		t.Fatalf("Windows reported a fabricated signal: %+v", panicOutcome)
	}
	if strings.Contains(first.Err().Error(), "runtime-test-secret") {
		t.Fatal("guest stderr leaked in diagnostics")
	}
	if got := string(call(t, second, "counter", nil)); got != "2" {
		t.Fatalf("other instance was damaged by panic: %s", got)
	}
	for _, fault := range []string{"loop", "block", "malformed"} {
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
		outcome := session.(contract.ProcessSession).Outcome()
		if outcome.Reason != contract.ExitUnexpected || outcome.ExitCode == nil || *outcome.ExitCode != 0 {
			t.Fatalf("zero exit facts: %+v", outcome)
		}
	})
	t.Run("forced-stop", func(t *testing.T) {
		session := start(t)
		call(t, session, "stubborn", nil)
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
		outcome := session.(contract.ProcessSession).Outcome()
		if outcome.Reason != contract.ExitStopped || !outcome.Forced {
			t.Fatalf("forced shutdown facts: %+v", outcome)
		}
	})
	t.Run("host-interruption", func(t *testing.T) {
		previous := adapter
		lifetime, cancel := context.WithCancel(t.Context())
		defer cancel()
		adapter = native.New(native.Options{Lifetime: lifetime, Grace: 100 * time.Millisecond})
		defer func() { adapter = previous }()
		session := start(t)
		cancel()
		awaitReaped(t, session)
		if outcome := session.(contract.ProcessSession).Outcome(); outcome.Reason != contract.ExitInterrupted {
			t.Fatalf("host interruption facts: %+v", outcome)
		}
	})
	t.Run("final-stderr-before-exit", func(t *testing.T) {
		var chunks []string
		session := start(t, func(launch *contract.Launch) {
			launch.Grace = 2 * time.Second
			launch.Stderr = func(record contract.StderrRecord) error {
				if len(chunks) == 0 {
					// A slow diagnostic sink leaves the final write unread when the process exits.
					time.Sleep(200 * time.Millisecond)
				}
				chunks = append(chunks, string(record.Data))
				return nil
			}
		})
		if _, err := session.Call(t.Context(), "exit-after-stderr", nil); err == nil {
			t.Fatal("exit returned a response")
		}
		awaitReaped(t, session)
		if got := strings.Join(chunks, ""); !strings.Contains(got, "final-diagnostic") {
			t.Fatalf("stderr written before exit was dropped at teardown: %q", got)
		}
	})
	t.Run("stderr-forwarding-bounded", func(t *testing.T) {
		forwarded := 0
		truncations := 0
		var owner contract.Identity
		session := start(t, func(launch *contract.Launch) {
			owner = launch.Identity
			launch.Stderr = func(record contract.StderrRecord) error {
				if record.Owner != owner {
					return errors.New("stderr identity mismatch")
				}
				if record.Truncated {
					truncations++
				}
				forwarded += len(record.Data)
				return nil
			}
		})
		for range 2 {
			if got := string(call(t, session, "stderr", nil)); got != "true" {
				t.Fatalf("stderr write did not finish: %s", got)
			}
			if got := string(call(t, session, "health", nil)); got != `{"healthy":true}` {
				t.Fatalf("stderr limit damaged activation: %s", got)
			}
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
		if forwarded != 1024 || truncations != 1 {
			t.Fatalf("forwarded=%d truncations=%d, want 1024 bytes and one truncation", forwarded, truncations)
		}
	})
	t.Run("stderr-without-sink-keeps-draining", func(t *testing.T) {
		session := start(t)
		call(t, session, "stderr", nil)
		call(t, session, "health", nil)
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("stderr-sink-failure-keeps-draining", func(t *testing.T) {
		broken := errors.New("diagnostic sink failure")
		calls := 0
		session := start(t, func(launch *contract.Launch) {
			launch.Stderr = func(contract.StderrRecord) error { calls++; return broken }
		})
		// More than a pipe's capacity must drain even after forwarding fails.
		call(t, session, "stderr", nil)
		call(t, session, "health", nil)
		if err := session.Close(); !errors.Is(err, broken) {
			t.Fatalf("sink failure hidden: %v", err)
		}
		if calls != 1 || !errors.Is(session.Err(), broken) {
			t.Fatalf("sink failure must stop forwarding but keep draining: calls=%d err=%v", calls, session.Err())
		}
	})
	t.Run("stuck-stderr-callback-bounded", func(t *testing.T) {
		release := make(chan struct{})
		session := start(t, func(launch *contract.Launch) {
			launch.Grace = 200 * time.Millisecond
			launch.Stderr = func(contract.StderrRecord) error { <-release; return nil }
		})
		t.Cleanup(func() { close(release) })
		if _, err := session.Call(t.Context(), "exit-after-stderr", nil); err == nil {
			t.Fatal("exit returned a response")
		}
		awaitReaped(t, session)
		if session.Err() == nil || !strings.Contains(session.Err().Error(), "stderr reader did not finish within grace") {
			t.Fatalf("abandoned stderr callback is not observable: %v", session.Err())
		}
	})
	t.Run("managed-descendant", func(t *testing.T) {
		session := start(t)
		marker := filepath.Join(t.TempDir(), "heartbeat")
		payload, err := json.Marshal(map[string]string{"path": marker})
		if err != nil {
			t.Fatal(err)
		}
		call(t, session, "spawn-child", payload)
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
		adapter := native.New(native.Options{Lifetime: lifetime, Grace: 100 * time.Millisecond})
		session, err := adapter.Start(context.Background(), contract.Launch{FrameBytes: contract.MaxFrameBytes,
			Identity: contract.Identity{Activation: contract.NewID()}, Directory: root, Artifact: nativePath,
			Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				return nil, contract.Fail(contract.Denied, "fixture has no service handler")
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
	root := t.TempDir()
	artifact := filepath.Join(root, "artifact")
	if err := os.WriteFile(artifact, []byte("not executable"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runtime.Start(ctx, contract.Launch{FrameBytes: contract.MaxFrameBytes, Directory: root, Artifact: artifact})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("startup cancellation lost: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(outside, []byte("not executable"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Start(context.Background(), contract.Launch{FrameBytes: contract.MaxFrameBytes, Directory: root, Artifact: outside})
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatal("artifact escaped package root")
	}
}
