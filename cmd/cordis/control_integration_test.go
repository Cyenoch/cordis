//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/examples/pluginmeta"
	"github.com/Cyenoch/cordis/packagehost"
)

func controlStore(t *testing.T) (string, string, contract.PackageRef) {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "chain")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	build.Dir = "../../examples/plugins/chain"
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build chain: %v %s", err, output)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	manifest := pluginmeta.Chain()
	key, trust := cliTrust(t, manifest.Publisher, manifest.ID)
	archive, ref := cliArchive(t, manifest, key, data)
	directory := t.TempDir()
	for _, args := range [][]string{
		{"install", "-store", directory, "-trust", trust, archive},
		{"select", "-store", directory, "-trust", trust, refText(ref)},
	} {
		if err := execute(t.Context(), args, strings.NewReader(""), io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	return directory, trust, ref
}

// The helper runs the real host command in a separate process; closing stdin
// supplies portable cancellation, including on Windows without a console.
func TestControlHostProcess(t *testing.T) {
	if os.Getenv("CORDIS_TEST_CONTROL_HOST") != "1" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("CORDIS_TEST_HOST_ARGS")), &args); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	if err := execute(ctx, args, os.Stdin, os.Stdout, os.Stderr); err != nil {
		t.Fatal(err)
	}
}

func TestLocalControlAcrossProcesses(t *testing.T) {
	directory, trust, ref := controlStore(t)
	run := func(args ...string) {
		t.Helper()
		if err := execute(t.Context(), args, strings.NewReader(""), io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []struct{ id, definition, parent, isolate, config string }{
		{"healthy", "provider", "", "", `{"label":"saved"}`},
		{"owner", "optional-parent", "healthy", "", `{}`},
		{"failed", "provider", "", "chain.a@1", `{"label":"fail-initialize"}`},
		{"waiting", "consumer", "", "chain.b@1", `{}`},
		{"off", "provider", "", "chain.a@1", `{"label":"disabled-config"}`},
	} {
		args := []string{"enable", "-store", directory, "-trust", trust, "-package", ref.ID, "-definition", entry.definition,
			"-parent", entry.parent, "-config", entry.config}
		if entry.isolate != "" {
			args = append(args, "-isolate", entry.isolate)
		}
		run(append(args, entry.id)...)
	}
	run("disable", "-store", directory, "off")
	path := filepath.Join(t.TempDir(), "connection.json")
	args, _ := json.Marshal([]string{"host", "-store", directory, "-trust", trust, "-control-file", path})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	process := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestControlHostProcess$")
	process.Env = append(os.Environ(), "CORDIS_TEST_CONTROL_HOST=1", "CORDIS_TEST_HOST_ARGS="+string(args))
	stdin, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	process.Stdout, process.Stderr = io.Discard, &diagnostics
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if err := process.Wait(); err != nil {
			t.Errorf("host process: %v\n%s", err, &diagnostics)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("connection not removed: %v", err)
		}
		store, err := packagehost.Open(directory, packagehost.TrustPolicy{})
		if err != nil {
			t.Errorf("host retained Store: %v", err)
		} else {
			store.Close()
		}
	})
	var connection controlConnection
	until(t, func() bool {
		var err error
		connection, err = readControlConnection(path)
		return err == nil
	})
	binary := buildManagementCLI(t)
	control := func(action, id string, failed bool) controlResponse {
		t.Helper()
		args := []string{"control", "-connection", path, "-timeout", "10s", action}
		if id != "" {
			args = append(args, id)
		}
		command := exec.CommandContext(t.Context(), binary, args...)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if (err != nil) != failed {
			t.Fatalf("control %s %s: %v %s\n%s", action, id, err, &stderr, output)
		}
		var result controlResponse
		if err := json.Unmarshal(output, &result); err != nil {
			t.Fatalf("decode control: %v %s", err, output)
		}
		return result
	}
	until(t, func() bool {
		response, err := requestControl(t.Context(), connection, "status", "", time.Second)
		return err == nil && response.Status != nil &&
			controlState(response, "healthy") == contract.Active && controlState(response, "owner.optional") == contract.Active &&
			controlState(response, "failed") == contract.Failed && controlState(response, "waiting") == contract.Waiting
	})
	status := control("status", "", false)
	dynamic := false
	for _, instance := range status.Status.Instances {
		if instance.Entry.ID == "owner.optional" {
			dynamic = instance.Origin == "dynamic" && instance.Owner != nil && instance.Owner.Entry == "owner"
		}
	}
	if !dynamic {
		t.Fatal("online status omitted dynamic ownership")
	}
	if response := control("stop", "owner.optional", true); response.Phase != "rejected" {
		t.Fatal("dynamic child accepted for CLI mutation")
	}
	if output, err := exec.CommandContext(t.Context(), binary, "status", "-store", directory).CombinedOutput(); err == nil {
		t.Fatalf("offline status bypassed live Store lock: %s", output)
	}
	enabled := control("enable", "off", false)
	var settings struct{ Label string }
	if err := json.Unmarshal(enabled.Status.Entries[0].Config, &settings); err != nil ||
		controlState(enabled, "off") != contract.Active || settings.Label != "disabled-config" {
		t.Fatalf("enable lost saved configuration: %+v %v", enabled.Status, err)
	}
	before := control("status", "failed", false).Status.Instances[0].Status.Identity
	if response := control("retry", "failed", true); response.Phase != "unknown" {
		t.Fatalf("post-admission initialization failure claimed rejection: %+v", response)
	}
	if after := control("status", "failed", false); after.Status.Instances[0].Status.Identity == before {
		t.Fatal("retry did not create a new activation")
	}
	if response := control("stop", "failed", true); response.Phase != "unknown" {
		t.Fatalf("historical failure was treated as uncommitted: %+v", response)
	}
	if controlState(control("status", "failed", false), "failed") != contract.Stopped {
		t.Fatal("failed Stop did not expose its committed state")
	}
	control("disable", "failed", true)
	control("stop", "healthy", false)
	stopped := control("status", "", false)
	if controlState(stopped, "healthy") != contract.Stopped || controlState(stopped, "owner") != contract.Stopped {
		t.Fatal("Stop did not retire the ownership tree")
	}
	control("resume", "healthy", false)
	control("resume", "owner", false)
	control("disable", "healthy", false)
	disabled := control("status", "", false)
	for _, entry := range disabled.Status.Entries {
		if (entry.ID == "healthy" || entry.ID == "owner") && entry.Enabled {
			t.Fatal("disable lost subtree intent")
		}
	}
	if controlState(disabled, "healthy") != "" || controlState(disabled, "owner") != "" {
		t.Fatal("disabled entries still claimed runtime registrations")
	}
	control("enable", "healthy", false)
	control("enable", "owner", false)
}

func until(t *testing.T, check func() bool) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(15 * time.Second)
	for !check() {
		select {
		case <-ticker.C:
		case <-timeout:
			t.Fatal("timed out waiting for control state")
		}
	}
}

func controlState(response controlResponse, id string) contract.State {
	if response.Status != nil {
		for _, instance := range response.Status.Instances {
			if instance.Entry.ID == id {
				return instance.Status.State
			}
		}
	}
	return ""
}

func TestNativeRecoveryAndOnlineManualRetry(t *testing.T) {
	directory, trustFile, ref := controlStore(t)
	trust, err := loadTrust(trustFile)
	if err != nil {
		t.Fatal(err)
	}
	store, err := packagehost.Open(directory, trust)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"target", "healthy"} {
		_, err := store.EnableEntry(t.Context(), contract.Entry{ID: id, Package: ref.ID, Definition: "provider", Isolate: []string{"chain.a@1"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.EnableEntry(t.Context(), contract.Entry{ID: "waiting", Package: ref.ID, Definition: "consumer", Isolate: []string{"chain.b@1"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	options := cordis.Options{Directory: directory, Trust: trust, Recovery: map[string]cordis.RecoveryPolicy{
		"target":  {MaxRetries: 2, InitialDelay: 100 * time.Millisecond, MaxDelay: 150 * time.Millisecond},
		"waiting": {MaxRetries: 1, InitialDelay: 10 * time.Millisecond, MaxDelay: 10 * time.Millisecond},
	}}
	h, err := cordis.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeHost(h, 5*time.Second) })
	restore := func(host *cordis.Host) {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := host.Restore(ctx); done <- err }()
		until(t, func() bool {
			target, _ := host.Status("target")
			healthy, _ := host.Status("healthy")
			waiting, _ := host.Status("waiting")
			return target.State == contract.Active && healthy.State == contract.Active && waiting.State == contract.Waiting
		})
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Waiting restore did not retain cancellation: %v", err)
		}
	}
	restore(h)
	s, err := startControl(t.Context(), h, filepath.Join(t.TempDir(), "connection.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.close() })
	healthy, _ := h.Status("healthy")
	caller, _ := h.Caller("target")
	required := contract.Requirement{Name: "chain.a", Major: 1, MinMinor: 2, MaxMinor: 2}
	if _, err := caller.Call(t.Context(), required, json.RawMessage(`{"method":"execute","input":{"operation":"stats"}}`)); err != nil {
		t.Fatalf("fixture service is not reachable: %v", err)
	}
	for n := 0; n < 3; n++ {
		current, _ := h.Status("target")
		_, err := caller.Call(t.Context(), required, json.RawMessage(`{"method":"execute","input":{"operation":"crash"}}`))
		if err == nil {
			t.Fatal("crash call succeeded")
		}
		if n < 2 {
			until(t, func() bool {
				status, _ := h.Status("target")
				if h.RecoveryStatus()[0].State == cordis.RecoveryCleanupFailed {
					t.Fatalf("crash cleanup failed: %+v", status)
				}
				return status.State == contract.Active && status.Identity != current.Identity
			})
		}
	}
	until(t, func() bool { return h.RecoveryStatus()[0].State == cordis.RecoveryExhausted })
	response, err := requestControl(t.Context(), s.connection, "status", "target", time.Second)
	if err != nil || response.Status == nil || response.Status.Recovery[0].Used != 2 ||
		response.Status.Instances[0].Status.Outcome.ExitCode == nil || *response.Status.Instances[0].Status.Outcome.ExitCode != 7 {
		t.Fatalf("native exhaustion/exit reason missing: %+v %v", response, err)
	}
	response, err = requestControl(t.Context(), s.connection, "retry", "target", 5*time.Second)
	if err != nil || response.Error != "" || controlState(response, "target") != contract.Active || h.RecoveryStatus()[0].Used != 0 {
		t.Fatalf("online manual retry: %+v %v", response, err)
	}
	if after, _ := h.Status("healthy"); after.Identity != healthy.Identity || after.State != contract.Active {
		t.Fatal("recovery restarted an unrelated instance")
	}
	if waiting := h.RecoveryStatus()[1]; waiting.Used != 0 || waiting.State != cordis.RecoveryIdle || waiting.NextRetryAt != nil {
		t.Fatalf("Waiting entry consumed recovery budget: %+v", waiting)
	}
	identity, _ := h.Status("target")
	_, err = caller.Call(t.Context(), required, json.RawMessage(`{"method":"execute","input":{"operation":"unknown"}}`))
	if err == nil {
		t.Fatal("expected ordinary business error")
	}
	time.Sleep(200 * time.Millisecond)
	if after, _ := h.Status("target"); after.Identity != identity.Identity || after.State != contract.Active || h.RecoveryStatus()[0].Used != 0 {
		t.Fatal("business error caused recovery or replay")
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	if err := closeHost(h, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	reopened, err := cordis.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer closeHost(reopened, 5*time.Second)
	restore(reopened)
	if status, _ := reopened.Status("target"); status.Identity == identity.Identity || reopened.RecoveryStatus()[0].Used != 0 {
		t.Fatal(fmt.Sprintf("reopen reused old activation/budget: %+v", status))
	}
	target, _ := reopened.Instance("target")
	if _, err := target.Update(t.Context(), json.RawMessage(`{"label":"fail-initialize"}`)); err == nil {
		t.Fatal("initialization failure was hidden")
	}
	until(t, func() bool {
		info := reopened.RecoveryStatus()[0]
		if info.State == cordis.RecoveryCleanupFailed {
			t.Fatalf("clean initialization failure blocked recovery: %+v", target.Status())
		}
		return info.State == cordis.RecoveryExhausted
	})
	if info := reopened.RecoveryStatus()[0]; info.Used != 2 || target.Status().Outcome.CleanupCode != "" {
		t.Fatalf("native initialization budget/cleanup: %+v %+v", info, target.Status())
	}
}
