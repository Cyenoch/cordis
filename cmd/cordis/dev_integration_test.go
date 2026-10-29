//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

const devPlugin = `package main
import ("context"; "fmt"; "os"; "time"
 "github.com/Cyenoch/cordis/contract"
 "github.com/Cyenoch/cordis/sdk")
const version = %q
const logPath = %q
func start(_ context.Context, scope contract.Scope, config MainConfig) error {
 mark := func(event string) error {
  file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
  if err != nil { return err }
  _, err = fmt.Fprintf(file, "%%s %%s %%v\n", version, event, config.Value)
  closeErr := file.Close()
  if err != nil { return err }; return closeErr
 }
 if err := scope.Defer(func(ctx context.Context) error {
  if err := mark("closing"); err != nil { return err }
  if config.Gate != "" {
   tick := time.NewTicker(time.Millisecond); defer tick.Stop()
   for { if _, err := os.Stat(config.Gate); err == nil { break }
    select { case <-ctx.Done(): return ctx.Err(); case <-tick.C: }
   }
  }
  return mark("stop")
 }); err != nil { return err }
 if err := mark("start"); err != nil { return err }
 if config.Fail { return fmt.Errorf("requested initialization failure") }
 return nil
}
func main() {
 if err := sdk.Run(context.Background(), NewMainDefinition(start)); err != nil {
  fmt.Fprintln(os.Stderr, err); os.Exit(1)
 }
}
`

func devSource(t *testing.T) (source, logPath, configFile string) {
	t.Helper()
	source = t.TempDir()
	logPath = filepath.Join(t.TempDir(), "lifecycle")
	configFile = filepath.Join(t.TempDir(), ".cordis-config.json")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, filepath.Join(source, "go.mod"), "module dev-fixture\n\ngo 1.27.3\nrequire github.com/Cyenoch/cordis v0.0.0\nreplace github.com/Cyenoch/cordis => "+strconv.Quote(repo)+"\n")
	writeCLIFile(t, filepath.Join(source, "plugin.json"), fmt.Sprintf(`{
 "id":"dev.test","version":"0.1.0","publisher":"example",
 "runtime":{"kind":"native","artifacts":[{"os":%q,"arch":%q,"path":"plugin"}]},
 "contract":{"definitions":[{"id":"main","config":{"type":"object",
 "properties":{"value":{"type":"integer"},"gate":{"type":"string"},"fail":{"type":"boolean"}},
 "required":["value","gate","fail"],"additionalProperties":false}}]}}`, runtime.GOOS, runtime.GOARCH))
	writeCLIFile(t, filepath.Join(source, "main.go"), fmt.Sprintf(devPlugin, "a", logPath))
	writeCLIFile(t, configFile, `{"value":1,"gate":"","fail":false}`)
	command := exec.CommandContext(t.Context(), "go", "mod", "tidy")
	command.Dir = source
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("prepare independent dev module: %v\n%s", err, output)
	}
	return source, logPath, configFile
}

func TestDevNativeEditingAndFailureRecovery(t *testing.T) {
	source, logPath, configFile := devSource(t)
	gate := filepath.Join(t.TempDir(), "release")
	configuration := func(value any, gate string, fail bool) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"value": value, "gate": gate, "fail": fail})
		if err != nil {
			t.Fatal(err)
		}
		writeCLIFile(t, configFile, string(raw))
	}
	configuration(1, gate, false)
	writeCLIFile(t, filepath.Join(source, "main.go"), "package main\ninvalid Go")
	temp := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, temp)
	}
	ctx, cancel := context.WithCancel(t.Context())
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := execute(ctx, []string{"dev", "-dev-unsigned", "-go-package", ".", "-config-file", configFile,
			"-shutdown-timeout", "15s", source}, strings.NewReader(""), writer, io.Discard)
		done <- err
		close(done)
		writer.Close()
	}()
	updates := make(chan devUpdate, 128)
	go func() {
		defer close(updates)
		decoder := json.NewDecoder(reader)
		for {
			var update devUpdate
			if decoder.Decode(&update) != nil {
				return
			}
			select {
			case updates <- update:
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() {
		// Release an interrupted assertion's blocked cleanup before joining dev.
		os.WriteFile(gate, nil, 0600)
		cancel()
		reader.Close()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("dev did not terminate")
		}
	})
	next := func(event string) devUpdate {
		t.Helper()
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		for {
			select {
			case update, ok := <-updates:
				if !ok {
					t.Fatalf("dev stopped before %s: %v", event, <-done)
				}
				if update.Event == event {
					return update
				}
			case <-timer.C:
				t.Fatalf("dev did not report %s", event)
			}
		}
	}
	active := func() devUpdate {
		t.Helper()
		update := next("runtime")
		if update.Error != "" || len(update.Instances) != 1 || update.Instances[0].Status.State != contract.Active {
			t.Fatalf("candidate failed to publish: %+v", update)
		}
		return update
	}
	lifecycle := func() string {
		t.Helper()
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if failure := next("build_failed"); failure.Error == "" {
		t.Fatal("initial invalid source succeeded")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("initial compile failure started a plugin: %v", err)
	}
	writeCLIFile(t, filepath.Join(source, "main.go"), fmt.Sprintf(devPlugin, "a", logPath))
	first := active()
	if got := lifecycle(); got != "a start 1\n" {
		t.Fatalf("first activation: %q", got)
	}
	writeCLIFile(t, filepath.Join(source, "main.go"), "package main\ninvalid Go")
	next("build_failed")
	if got := lifecycle(); got != "a start 1\n" {
		t.Fatalf("compile failure stopped old activation: %q", got)
	}
	configuration("invalid", "", false)
	writeCLIFile(t, filepath.Join(source, "main.go"), fmt.Sprintf(devPlugin, "b", logPath))
	for failure := next("build_failed"); !strings.Contains(failure.Error, "/value"); failure = next("build_failed") {
		t.Logf("input changed during failed build: %s", failure.Error)
	}
	if got := lifecycle(); got != "a start 1\n" {
		t.Fatalf("configuration failure stopped old activation: %q", got)
	}
	// Pack must reject a source hardlink without disturbing the live candidate.
	link := filepath.Join(source, "linked-resource")
	if err := os.Link(logPath, link); err != nil {
		t.Fatal(err)
	}
	configuration(2, "", false)
	for failure := next("build_failed"); !strings.Contains(failure.Error, "hard-linked"); failure = next("build_failed") {
		t.Logf("input changed during failed build: %s", failure.Error)
	}
	if got := lifecycle(); got != "a start 1\n" {
		t.Fatalf("Pack failure stopped old activation: %q", got)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	waitDevMarker(t, logPath, "a closing 1\n")
	writeCLIFile(t, filepath.Join(source, "main.go"), fmt.Sprintf(devPlugin, "c", logPath))
	writeCLIFile(t, gate, "")
	second := active()
	if second.Generation <= first.Generation || second.Instances[0].Status.Identity == first.Instances[0].Status.Identity {
		t.Fatal("edit reused the old activation")
	}
	if got := lifecycle(); got != "a start 1\na closing 1\na stop 1\nc start 2\n" {
		t.Fatalf("stale candidate started during close, or configuration changed: %q", got)
	}
	configuration(3, "", true)
	failed := next("runtime")
	if failed.Error == "" || len(failed.Instances) != 1 || failed.Instances[0].Status.State != contract.Failed {
		t.Fatalf("initialization failure not visible: %+v", failed)
	}
	configuration(4, "", false)
	active()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("dev cancellation: %v", err)
	}
	if got := lifecycle(); !strings.HasSuffix(got, "c start 4\nc closing 4\nc stop 4\n") {
		t.Fatalf("last process not drained: %q", got)
	}
	author, err := packagehost.LoadAuthor(source)
	if err != nil || author.Manifest.Version != "0.1.0" {
		t.Fatalf("dev changed author version: %+v %v", author, err)
	}
	dirs, err := filepath.Glob(filepath.Join(temp, "cordis-dev-*"))
	if err != nil || len(dirs) != 0 {
		t.Fatalf("completed dev retained temporary stores: %v %v", dirs, err)
	}
}

func waitDevMarker(t *testing.T, path, marker string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), marker) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("plugin did not report %q; got %q", marker, data)
		case <-ticker.C:
		}
	}
}

func TestDevInputsExcludeOutputsAndWatchExternalFiles(t *testing.T) {
	source, _, configFile := devSource(t)
	extra := t.TempDir()
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	watcher, err := newDevWatcher(source, configFile, []string{extra})
	if err != nil {
		t.Fatal(err)
	}
	first := watcher.refresh()
	if first.err != nil {
		t.Fatal(first.err)
	}
	for _, name := range []string{"cordis_gen.go", "build.json", "plugin", ".cordis-build-test"} {
		writeCLIFile(t, filepath.Join(source, name), "tool output")
	}
	if next := watcher.refresh(); next.generation != first.generation || next.err != nil {
		t.Fatalf("tool output triggered rebuild: %+v", next)
	}
	for _, path := range []string{filepath.Join(source, "resource.txt"), configFile, filepath.Join(extra, "dependency.go")} {
		prior := watcher.refresh().generation
		writeCLIFile(t, path, "changed input")
		if next := watcher.refresh(); next.err != nil || next.generation == prior {
			t.Fatalf("input not watched: %s: %+v", path, next)
		}
	}
	raw, err := os.ReadFile(filepath.Join(source, "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(source, "plugin")); err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"path":"plugin"`, `"path":"bin/plugin"`, 1))
	writeCLIFile(t, filepath.Join(source, "plugin.json"), string(raw))
	if err := os.Mkdir(filepath.Join(source, "plugin"), 0700); err != nil {
		t.Fatal(err)
	}
	watcher.refresh()
	writeCLIFile(t, filepath.Join(source, "plugin", "business.go"), "package plugin")
	prior := watcher.refresh()
	writeCLIFile(t, filepath.Join(source, "plugin", "business.go"), "package plugin\nvar Changed = true")
	if next := watcher.refresh(); next.err != nil || next.generation == prior.generation {
		t.Fatalf("old artifact path still excluded after becoming source: %+v", next)
	}
	raw = []byte(strings.Replace(string(raw), `"artifacts":[`, `"artifacts":[{"os":"unsupported","arch":"test","path":"other"},`, 1))
	writeCLIFile(t, filepath.Join(source, "plugin.json"), string(raw))
	if err := checkDevTarget(source); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("multi-target dev not explicitly rejected: %v", err)
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, source)
	}
	if err := watcher.checkTemporaryRoot(); err == nil {
		t.Fatal("accepted compiler/Pack temporary directories inside watched module")
	}
}

func TestDevCandidateRetainsDirectoryUntilCleanupCompletes(t *testing.T) {
	directory := t.TempDir()
	h, err := cordis.Open(cordis.Options{Directory: filepath.Join(directory, "store")})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		if err := closeHost(h, time.Second); err != nil {
			t.Error(err)
		}
	})
	instance, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "slow"}, contract.Definition{
		Start: func(_ context.Context, scope contract.Scope) error {
			return scope.Defer(func(context.Context) error { <-release; return nil })
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	candidate := &devCandidate{host: h, directory: directory}
	if complete, err := candidate.close(10 * time.Millisecond); complete || err == nil {
		t.Fatalf("incomplete close accepted: %t %v", complete, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "store")); err != nil {
		t.Fatalf("incomplete cleanup deleted live Store: %v", err)
	}
	close(release)
	if complete, err := candidate.close(time.Second); !complete || err != nil {
		t.Fatalf("completed cleanup not recognized: %t %v", complete, err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("completed candidate directory retained: %v", err)
	}
}
