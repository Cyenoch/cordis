//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/examples/pluginmeta"
	"github.com/Cyenoch/cordis/packagehost"
)

func TestDevProjectNativeGroupSwitching(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	projectRoot := t.TempDir()
	for _, name := range []string{"provider", "consumer"} {
		directory := filepath.Join(projectRoot, name)
		if err := os.CopyFS(directory, os.DirFS(filepath.Join(repo, "examples", "plugins", name))); err != nil {
			t.Fatal(err)
		}
		manifestFile := filepath.Join(directory, "plugin.toml")
		data, err := os.ReadFile(manifestFile)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(data), `os = "darwin"`, `os = "`+runtime.GOOS+`"`)
		text = strings.ReplaceAll(text, `arch = "arm64"`, `arch = "`+runtime.GOARCH+`"`)
		writeCLIFile(t, manifestFile, text)
		command := exec.CommandContext(t.Context(), "go", "mod", "edit", "-replace=github.com/Cyenoch/cordis="+repo)
		command.Dir = directory
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("prepare dev module: %v\n%s", err, output)
		}
	}
	observed := filepath.Join(t.TempDir(), "observed")
	projectFile := filepath.Join(projectRoot, "project.json")
	writeProject := func(output string) {
		t.Helper()
		project := map[string]any{
			"packages": []any{
				map[string]string{"directory": "consumer", "goPackage": "."},
				map[string]string{"directory": "provider", "goPackage": "."},
			},
			"entries": []any{
				map[string]any{"id": "consumer", "package": "example.consumer", "config": map[string]string{"output": output}},
				map[string]string{"id": "provider", "package": "example.provider"},
			},
		}
		raw, err := json.Marshal(project)
		if err != nil {
			t.Fatal(err)
		}
		writeCLIFile(t, projectFile, string(raw))
	}
	writeProject(observed)
	temporary := t.TempDir()
	for _, variable := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(variable, temporary)
	}
	ctx, cancel := context.WithCancel(t.Context())
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := execute(ctx, []string{"dev", "-dev-unsigned", "-project", projectFile}, strings.NewReader(""), writer, io.Discard)
		writer.Close()
		done <- err
	}()
	updates := make(chan devUpdate, 32)
	go func() {
		defer close(updates)
		decoder := json.NewDecoder(reader)
		for {
			var update devUpdate
			if err := decoder.Decode(&update); err != nil {
				return
			}
			select {
			case updates <- update:
			case <-ctx.Done():
				return
			}
		}
	}()
	finished := false
	t.Cleanup(func() {
		cancel()
		reader.Close()
		if finished {
			return
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("development group did not stop")
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
					t.Fatal("development output ended")
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
		if update.Error != "" || len(update.Instances) != 2 {
			t.Fatalf("development group not ready: %+v", update)
		}
		for _, instance := range update.Instances {
			if instance.Status.State != contract.Active {
				t.Fatalf("instance not active: %+v", instance)
			}
		}
		return update
	}
	waitMessage := func(value string) {
		t.Helper()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			if data, err := os.ReadFile(observed); err == nil && string(data) == value {
				return
			}
			select {
			case <-timer.C:
				t.Fatalf("consumer did not observe provider message %q", value)
			case <-ticker.C:
			}
		}
	}
	first := active()
	waitMessage("hello")
	provider := filepath.Join(projectRoot, "provider", "main.go")
	code, err := os.ReadFile(provider)
	if err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, provider, "package main\ninvalid Go")
	next("build_failed")
	if err := os.Remove(observed); err != nil {
		t.Fatal(err)
	}
	waitMessage("hello")
	writeCLIFile(t, provider, strings.Replace(string(code), `"hello"`, `"updated"`, 1))
	second := active()
	if second.Generation <= first.Generation {
		t.Fatal("new group reused old generation")
	}
	for n := range first.Instances {
		if first.Instances[n].Status.Identity == second.Instances[n].Status.Identity {
			t.Fatal("only part of the group restarted")
		}
	}
	waitMessage("updated")
	writeCLIFile(t, projectFile, "{")
	next("build_failed")
	if err := os.Remove(observed); err != nil {
		t.Fatal(err)
	}
	waitMessage("updated")
	writeProject("relative-path-rejected-by-plugin")
	failed := next("runtime")
	foundFailed := false
	for _, instance := range failed.Instances {
		foundFailed = foundFailed || instance.Status.State == contract.Failed
	}
	if failed.Error == "" || !foundFailed {
		t.Fatalf("new initializer failure not visible: %+v", failed)
	}
	writeProject(observed)
	active()
	waitMessage("updated")
	cancel()
	err = <-done
	finished = true
	if err != nil {
		t.Fatalf("dev group exit: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(temporary, "cordis-dev-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("group cleanup retained candidate directories: %v %v", matches, err)
	}
}

func TestDevPackageSelectionHonorsRequiredAndOptionalDependencies(t *testing.T) {
	for _, scenario := range []string{"required", "missing", "incompatible", "cycle", "optional-cycle"} {
		t.Run(scenario, func(t *testing.T) {
			key, trustFile := cliTrust(t, "example.author", "example.a", "example.b")
			trust, err := loadTrust(trustFile)
			if err != nil {
				t.Fatal(err)
			}
			h, err := cordis.Open(cordis.Options{Directory: t.TempDir(), Trust: trust})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := h.Close(t.Context()); err != nil {
					t.Error(err)
				}
			}()
			packages := map[string]packagehost.Package{}
			for _, id := range []string{"example.a", "example.b"} {
				m := pluginmeta.Counter()
				m.ID = id
				if id == "example.a" {
					dependency := contract.Dependency{ID: "example.b", Range: ">=1.0.0 <2.0.0"}
					if scenario == "missing" {
						dependency.ID = "example.absent"
					} else if scenario == "incompatible" {
						dependency.Range = ">=2.0.0 <3.0.0"
					}
					m.Dependencies = []contract.Dependency{dependency}
				} else if scenario == "cycle" || scenario == "optional-cycle" {
					m.Dependencies = []contract.Dependency{{ID: "example.a", Range: ">=1.0.0 <2.0.0"}}
				}
				if scenario == "optional-cycle" {
					m.OptionalDependencies, m.Dependencies = m.Dependencies, nil
				}
				archive, _ := cliArchive(t, m, key, []byte("not executed"))
				pkg, err := h.Install(t.Context(), archive)
				if err != nil {
					t.Fatal(err)
				}
				packages[id] = pkg
			}
			err = selectDevPackages(t.Context(), h, packages)
			wantSuccess := scenario == "required" || scenario == "optional-cycle"
			if (err == nil) != wantSuccess {
				t.Fatalf("dependency selection result: %v", err)
			}
			if !wantSuccess && errors.Is(err, context.Canceled) {
				t.Fatalf("dependency validation did not finish: %v", err)
			}
			if len(h.Instances()) != 0 {
				t.Fatal("preflight started plugin code")
			}
		})
	}
}
