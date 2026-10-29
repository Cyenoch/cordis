//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/examples/pluginmeta"
	"github.com/Cyenoch/cordis/packagehost"
)

func cliTrust(t *testing.T, publisher string, ids ...string) (ed25519.PrivateKey, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "trust.json")
	data, err := json.Marshal(map[string]any{
		"keys":       map[string]any{"test": map[string]any{"publicKey": hex.EncodeToString(public), "publisher": publisher}},
		"publishers": map[string][]string{publisher: ids},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	return private, file
}

func cliArchive(t *testing.T, manifest contract.Manifest, key ed25519.PrivateKey, artifact []byte) (string, contract.PackageRef) {
	t.Helper()
	source := t.TempDir()
	manifest.Runtime = contract.RuntimeSpec{Kind: "native", Artifacts: []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: "plugin"}}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "plugin"), artifact, 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "plugin.zip")
	if _, err := packagehost.Pack(t.Context(), source, archive); err != nil {
		t.Fatal(err)
	}
	pkg, err := packagehost.Sign(t.Context(), archive, "test", key)
	if err != nil {
		t.Fatal(err)
	}
	return archive, pkg.Ref
}

func refText(ref contract.PackageRef) string {
	return fmt.Sprintf("%s@%s#%s", ref.ID, ref.Version, ref.Digest)
}

func TestOfflineCLIManagesIntentAndIncompatibleUpgrade(t *testing.T) {
	key, trust := cliTrust(t, "example", "demo")
	m := contract.Manifest{
		Schema: contract.PackageSchema, ID: "demo", Version: "1.0.0", Publisher: "example",
		Protocol: contract.Protocol{Major: 1}, DefaultDefinition: "main",
		Definitions: []contract.DefinitionSpec{{ID: "main", Requires: []contract.Consumer{}, Provides: []contract.Service{},
			ConfigSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)}},
		Dependencies: []contract.Dependency{}, OptionalDependencies: []contract.Dependency{},
	}
	first, firstRef := cliArchive(t, m, key, []byte("not executable; offline operations must not run it"))
	m.Version = "2.0.0"
	m.Definitions[0].ConfigSchema = json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`)
	second, secondRef := cliArchive(t, m, key, []byte("not executable either"))
	directory := filepath.Join(t.TempDir(), "store")
	run := func(args ...string) (string, error) {
		var out, diagnostics bytes.Buffer
		err := execute(t.Context(), args, strings.NewReader(""), &out, &diagnostics)
		return out.String(), err
	}
	must := func(args ...string) string {
		t.Helper()
		out, err := run(args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	deny := filepath.Join(t.TempDir(), "deny.json")
	writeCLIFile(t, deny, `{}`)
	if _, err := run("install", "-store", directory, "-trust", deny, first); err == nil {
		t.Fatal("untrusted signer was accepted")
	}
	must("install", "-store", directory, "-trust", trust, first)
	must("install", "-store", directory, "-trust", trust, second)
	must("select", "-store", directory, "-trust", trust, refText(firstRef))
	must("enable", "-store", directory, "-trust", trust, "-package", "demo", "-isolate", "db@1", "-config", `{"value":"old"}`, "root")
	must("enable", "-store", directory, "-trust", trust, "-package", "demo", "-parent", "root", "-config", `{"value":"child"}`, "child")
	var status struct {
		Offline bool                    `json:"offline"`
		Entries []contract.Entry        `json:"entries"`
		Runs    []packagehost.RunRecord `json:"runs"`
	}
	if err := json.Unmarshal([]byte(must("status", "-store", directory)), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Offline || len(status.Runs) != 0 || len(status.Entries) != 2 {
		t.Fatalf("offline status misrepresents running code: %+v", status)
	}
	must("disable", "-store", directory, "root")
	var entry contract.Entry
	if err := json.Unmarshal([]byte(must("enable", "-store", directory, "-trust", trust, "child")), &entry); err != nil {
		t.Fatal(err)
	}
	var configuration map[string]string
	if err := json.Unmarshal(entry.Config, &configuration); err != nil {
		t.Fatal(err)
	}
	if entry.Parent != "root" || configuration["value"] != "child" {
		t.Fatalf("enable dropped omitted fields: %+v", entry)
	}
	entry = contract.Entry{}
	if err := json.Unmarshal([]byte(must("enable", "-store", directory, "-trust", trust, "root")), &entry); err != nil {
		t.Fatal(err)
	}
	if len(entry.Isolate) != 1 || entry.Isolate[0] != "db@1" {
		t.Fatal("enable dropped existing isolation")
	}
	entry = contract.Entry{}
	if err := json.Unmarshal([]byte(must("enable", "-store", directory, "-trust", trust, "-isolate", "", "root")), &entry); err != nil || len(entry.Isolate) != 0 {
		t.Fatalf("explicit isolation clear: %+v %v", entry, err)
	}
	must("disable", "-store", directory, "root")
	if _, err := run("select", "-store", directory, "-trust", trust, refText(secondRef)); err == nil {
		t.Fatal("incompatible old configuration was silently discarded")
	}
	if _, err := run("remove", "-store", directory, "root"); err == nil {
		t.Fatal("remove orphaned a child")
	}
	must("remove", "-store", directory, "child")
	must("remove", "-store", directory, "root")
	must("select", "-store", directory, "-trust", trust, refText(secondRef))
	must("enable", "-store", directory, "-trust", trust, "-package", "demo", "-config", `{"value":1}`, "root")
	broken := errors.New("broken output")
	err := execute(t.Context(), []string{"enable", "-store", directory, "-trust", trust, "-config", `{"value":2}`, "root"},
		strings.NewReader(""), failedOutput{broken}, io.Discard)
	if !errors.Is(err, broken) || !strings.Contains(err.Error(), "committed") {
		t.Fatalf("post-commit output failure: %v", err)
	}
	store, err := packagehost.Open(directory, packagehost.TrustPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	entries, err := store.Entries()
	if err != nil || len(entries) != 1 || string(entries[0].Config) != `{"value":2}` {
		t.Fatalf("failed output undid committed state: %+v %v", entries, err)
	}
	for _, command := range []string{"status", "list", "disable"} {
		args := []string{command, "-store", directory}
		if command == "disable" {
			args = append(args, "root")
		}
		if _, err := run(args...); err == nil || !strings.Contains(err.Error(), "locked") {
			t.Fatalf("%s bypassed ownership: %v", command, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	binary := buildManagementCLI(t)
	code, diagnostics := brokenStdout(t, binary, "enable", "-store", directory, "-trust", trust, "-config", `{"value":3}`, "root")
	if code != 1 || !strings.Contains(diagnostics, "committed") {
		t.Fatalf("real broken pipe bypassed commit report: code=%d stderr=%s", code, diagnostics)
	}
	reopened, err := packagehost.Open(directory, packagehost.TrustPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	entries, err = reopened.Entries()
	if err != nil || string(entries[0].Config) != `{"value":3}` {
		t.Fatalf("real output failure lost committed intent: %+v %v", entries, err)
	}
}

type failedOutput struct{ err error }

func (w failedOutput) Write([]byte) (int, error) { return 0, w.err }

func buildManagementCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "cordis")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	return binary
}

func brokenStdout(t *testing.T, binary string, args ...string) (int, string) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	var diagnostics bytes.Buffer
	command.Stdout, command.Stderr = writer, &diagnostics
	err = command.Run()
	if ctx.Err() != nil {
		t.Fatalf("CLI failed to exit after broken stdout: %v", ctx.Err())
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.ExitCode(), diagnostics.String()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, diagnostics.String()
}

func TestOfflineCLIProtectsEnabledReverseDependencies(t *testing.T) {
	key, trust := cliTrust(t, "example", "demo", "dep")
	manifest := contract.Manifest{
		Schema: contract.PackageSchema, ID: "dep", Version: "1.0.0", Publisher: "example",
		Protocol: contract.Protocol{Major: 1}, DefaultDefinition: "main",
		Definitions: []contract.DefinitionSpec{{ID: "main", ConfigSchema: json.RawMessage(`{"type":"object"}`),
			Requires: []contract.Consumer{}, Provides: []contract.Service{}}},
		Dependencies: []contract.Dependency{}, OptionalDependencies: []contract.Dependency{},
	}
	first, firstRef := cliArchive(t, manifest, key, []byte("offline"))
	manifest.Version = "2.0.0"
	second, secondRef := cliArchive(t, manifest, key, []byte("offline"))
	manifest.ID, manifest.Version = "demo", "1.0.0"
	manifest.Dependencies = []contract.Dependency{{ID: "dep", Range: "^1.0.0"}}
	consumer, consumerRef := cliArchive(t, manifest, key, []byte("offline"))
	directory := t.TempDir()
	run := func(args ...string) error {
		return execute(t.Context(), args, strings.NewReader(""), io.Discard, io.Discard)
	}
	for _, archive := range []string{first, second, consumer} {
		if err := run("install", "-store", directory, "-trust", trust, archive); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []contract.PackageRef{firstRef, consumerRef} {
		if err := run("select", "-store", directory, "-trust", trust, refText(ref)); err != nil {
			t.Fatal(err)
		}
	}
	if err := run("enable", "-store", directory, "-trust", trust, "-package", "demo", "consumer"); err != nil {
		t.Fatal(err)
	}
	if err := run("select", "-store", directory, "-trust", trust, refText(secondRef)); err == nil {
		t.Fatal("selection ignored enabled reverse dependency")
	}
	if err := run("uninstall", "-store", directory, refText(firstRef)); err == nil {
		t.Fatal("uninstall ignored enabled reverse dependency")
	}
	if err := run("select", "-store", directory, "-trust", trust, "dep@latest"); exitCode(err, io.Discard) != 2 {
		t.Fatalf("imprecise reference was not a usage error: %v", err)
	}
}

func TestForegroundHostReportsPartialStateAndClosesNativeProcesses(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "chain")
	command := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	command.Dir = "../../examples/plugins/chain"
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build native plugin: %v\n%s", err, output)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	m := pluginmeta.Chain()
	key, trust := cliTrust(t, m.Publisher, m.ID)
	archive, ref := cliArchive(t, m, key, data)
	directory := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if err := execute(t.Context(), args, strings.NewReader(""), io.Discard, io.Discard); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	run("install", "-store", directory, "-trust", trust, archive)
	run("select", "-store", directory, "-trust", trust, refText(ref))
	run("enable", "-store", directory, "-trust", trust, "-package", m.ID, "-definition", "provider", "healthy")
	run("enable", "-store", directory, "-trust", trust, "-package", m.ID, "-definition", "provider", "-config", `{"label":"fail-initialize"}`, "failed")
	run("enable", "-store", directory, "-trust", trust, "-package", m.ID, "-definition", "consumer", "waiting")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	reader, writer := io.Pipe()
	ready := make(chan struct{}, 1)
	reports := make(chan bool, 1)
	go func() {
		defer reader.Close()
		decoder := json.NewDecoder(reader)
		hasFailure := false
		for {
			var update struct {
				Instances []contract.InstanceInfo `json:"instances"`
				Restore   []struct {
					ID    string `json:"id"`
					Error string `json:"error"`
				} `json:"restore"`
			}
			if err := decoder.Decode(&update); err != nil {
				reports <- hasFailure
				return
			}
			states := map[string]contract.State{}
			for _, instance := range update.Instances {
				states[instance.Entry.ID] = instance.Status.State
			}
			if states["healthy"] == contract.Active && states["failed"] == contract.Failed && states["waiting"] == contract.Waiting {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
			for _, entry := range update.Restore {
				hasFailure = hasFailure || entry.ID == "failed" && entry.Error != ""
			}
		}
	}()
	var diagnostics bytes.Buffer
	done := make(chan error, 1)
	go func() {
		err := execute(ctx, []string{"host", "-store", directory, "-trust", trust}, strings.NewReader(""), writer, &diagnostics)
		writer.Close()
		done <- err
	}()
	select {
	case <-ready:
		cancel()
	case <-ctx.Done():
		err := <-done
		t.Fatalf("host did not expose independent states: %v\n%s", err, diagnostics.String())
	}
	if err := <-done; err == nil {
		t.Fatal("historical initialization failure was discarded on close")
	}
	if !<-reports {
		t.Fatal("restore entry error was lost from JSON output")
	}
	store, err := packagehost.Open(directory, packagehost.TrustPolicy{})
	if err != nil {
		t.Fatalf("host did not release directory: %v", err)
	}
	defer store.Close()
	runs, err := store.Runs()
	if err != nil || len(runs) != 2 {
		t.Fatalf("native run history: %+v %v", runs, err)
	}
	for _, run := range runs {
		if run.State == "running" || run.State == "interrupted" {
			t.Fatalf("native activation was not closed: %+v", run)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	binary = buildManagementCLI(t)
	if code, diagnostics := brokenStdout(t, binary, "host", "-store", directory, "-trust", trust); code != 1 {
		t.Fatalf("broken output skipped normal host exit: code=%d stderr=%s", code, diagnostics)
	}
	reopened, err := packagehost.Open(directory, packagehost.TrustPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	runs, err = reopened.Runs()
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.State == "running" || run.State == "interrupted" {
			t.Fatalf("broken stdout bypassed activation cleanup: %+v", run)
		}
	}
}
