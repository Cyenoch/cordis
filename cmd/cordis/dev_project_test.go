package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDevSourceRevertedDuringBuildInvalidatesCandidate(t *testing.T) {
	options, directory, _ := projectFixture(t)
	watcher, err := newDevWatcher(options.plan)
	if err != nil {
		t.Fatal(err)
	}
	first := watcher.refresh()
	path := filepath.Join(directory, "provider", "main.go")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// The compiler may read B between polls even when the next scan sees A again.
	if err := os.WriteFile(path, []byte("package main\n// B"), 0600); err != nil {
		t.Fatal(err)
	}
	compiled, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(compiled, []byte("B")) {
		t.Fatalf("compiler input: %q %v", compiled, err)
	}
	if err := os.WriteFile(path, []byte("package main"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := info.ModTime().Add(time.Second)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	current := watcher.refresh()
	if current.err != nil || current.generation == first.generation {
		t.Fatalf("reverted source certified an intermediate candidate: %+v", current)
	}
}

func projectFixture(t *testing.T) (devOptions, string, string) {
	t.Helper()
	directory := t.TempDir()
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"provider", "consumer"} {
		source := filepath.Join(directory, name)
		if err := os.Mkdir(source, 0700); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string]any{
			"id": "example." + name, "version": "1.0.0", "publisher": "example",
			"runtime": map[string]any{"kind": "native", "artifacts": []any{
				map[string]string{"os": runtime.GOOS, "arch": runtime.GOARCH, "path": "plugin"},
			}},
			"contract": map[string]any{"definitions": []any{map[string]string{"id": "main"}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		for path, value := range map[string][]byte{
			filepath.Join(source, "plugin.json"): raw,
			filepath.Join(source, "go.mod"):      []byte("module example.net/" + name),
			filepath.Join(source, "main.go"):     []byte("package main"),
		} {
			if err := os.WriteFile(path, value, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	project := `{
 "packages":[{"directory":"provider","goPackage":"."},{"directory":"consumer","goPackage":"."}],
 "entries":[{"id":"consumer","package":"example.consumer","parent":"provider","config":{"value":"A"}},
            {"id":"provider","package":"example.provider","configFile":"settings.json"}]
}`
	path := filepath.Join(directory, "project.json")
	if err := os.WriteFile(path, []byte(project), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "settings.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	return devOptions{project: path}, directory, project
}

func TestDevProjectPathsAndValidation(t *testing.T) {
	options, directory, original := projectFixture(t)
	plan, err := options.plan()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.packages) != 2 || len(plan.entries) != 2 || plan.packages[0].directory != filepath.Join(directory, "consumer") ||
		!plan.explicit[filepath.Join(directory, "settings.json")] || plan.entries[0].Parent != "provider" {
		t.Fatalf("project was not resolved relative to its file: %+v", plan)
	}
	for _, test := range []struct{ name, input string }{
		{"unknown field", strings.Replace(original, `"packages":`, `"ignored":true,"packages":`, 1)},
		{"duplicate package", strings.Replace(original, `"directory":"consumer"`, `"directory":"provider"`, 1)},
		{"duplicate entry", strings.Replace(original, `"id":"consumer"`, `"id":"provider"`, 1)},
		{"unknown package", strings.Replace(original, `"package":"example.consumer"`, `"package":"example.absent"`, 1)},
		{"missing parent", strings.Replace(original, `"parent":"provider"`, `"parent":"absent"`, 1)},
		{"parent cycle", strings.Replace(original, `"id":"provider"`, `"id":"provider","parent":"consumer"`, 1)},
		{"config sources", strings.Replace(original, `"configFile":"settings.json"`, `"configFile":"settings.json","config":{}`, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(options.project, []byte(test.input), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := options.plan(); err == nil {
				t.Fatal("invalid project accepted")
			}
		})
	}
}

func TestDevPlanCannotUseNewFingerprintForOldConfiguration(t *testing.T) {
	options, _, original := projectFixture(t)
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(options.project, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	injectEdit := false
	watcher, err := newDevWatcher(func() (*devPlan, error) {
		plan, err := options.plan()
		if injectEdit {
			write(strings.Replace(original, `"value":"A"`, `"value":"B"`, 1))
			injectEdit = false
		}
		return plan, err
	})
	if err != nil {
		t.Fatal(err)
	}
	first := watcher.refresh()
	injectEdit = true
	stale := watcher.refresh()
	current := watcher.refresh()
	if first.err != nil || stale.err != nil || current.err != nil {
		t.Fatalf("scan errors: %v %v %v", first.err, stale.err, current.err)
	}
	if stale.generation == current.generation ||
		!bytes.Contains(stale.plan.entries[0].Config, []byte(`"A"`)) ||
		!bytes.Contains(current.plan.entries[0].Config, []byte(`"B"`)) {
		t.Fatal("a new file fingerprint certified an old parsed configuration")
	}
}

func TestDevProjectWatchesOutputsAndRepairsConfiguration(t *testing.T) {
	options, directory, original := projectFixture(t)
	for _, name := range []string{"provider", "consumer"} {
		if err := os.Remove(filepath.Join(directory, name, "go.mod")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte("module example.net/shared"), 0600); err != nil {
		t.Fatal(err)
	}
	watcher, err := newDevWatcher(options.plan)
	if err != nil {
		t.Fatal(err)
	}
	first := watcher.refresh()
	for _, pkg := range first.plan.packages {
		for _, name := range []string{"cordis_gen.go", "build.json", "plugin"} {
			if err := os.WriteFile(filepath.Join(pkg.directory, name), []byte("output"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if next := watcher.refresh(); next.err != nil || next.generation != first.generation {
		t.Fatalf("another package's generated output triggered a rebuild: %+v", next)
	}
	if err := os.WriteFile(options.project, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	broken := watcher.refresh()
	if broken.err == nil {
		t.Fatal("invalid project accepted")
	}
	if err := os.WriteFile(options.project, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	repaired := watcher.refresh()
	if repaired.err != nil || repaired.generation == broken.generation {
		t.Fatalf("project repair was not observed: %+v", repaired)
	}
	configFile := filepath.Join(directory, "settings.json")
	if err := os.WriteFile(configFile, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	broken = watcher.refresh()
	if broken.err == nil {
		t.Fatal("invalid entry configuration accepted")
	}
	if err := os.WriteFile(configFile, []byte(`{"value":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	repaired = watcher.refresh()
	if repaired.err != nil || repaired.generation == broken.generation {
		t.Fatalf("configuration repair was not observed: %+v", repaired)
	}
}

func TestDevProjectRejectsConflictingCLIInputs(t *testing.T) {
	for _, extra := range [][]string{
		{"directory"}, {"-go-package", "."}, {"-config", "{}"}, {"-config-file", "settings.json"},
		{"-config-format", "json"}, {"-definition", "main"},
	} {
		var out, diagnostics bytes.Buffer
		args := append([]string{"dev", "-dev-unsigned", "-project", "project.json"}, extra...)
		if code := exitCode(execute(t.Context(), args, strings.NewReader(""), &out, &diagnostics), &diagnostics); code != 2 {
			t.Fatalf("conflicting inputs accepted: %v (code %d)", args, code)
		}
	}
}

func TestDevProjectUpdatesPackageWatchSet(t *testing.T) {
	options, directory, original := projectFixture(t)
	watcher, err := newDevWatcher(options.plan)
	if err != nil {
		t.Fatal(err)
	}
	first := watcher.refresh()
	project, _, err := readDevProject(options.project)
	if err != nil {
		t.Fatal(err)
	}
	project.Packages = project.Packages[:1]
	project.Entries = project.Entries[1:]
	raw, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(options.project, raw, 0600); err != nil {
		t.Fatal(err)
	}
	removed := watcher.refresh()
	if removed.err != nil || removed.generation == first.generation || len(removed.plan.packages) != 1 {
		t.Fatalf("removal not reflected in plan: %+v", removed)
	}
	if err := os.WriteFile(filepath.Join(directory, "consumer", "main.go"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if after := watcher.refresh(); after.err != nil || after.generation != removed.generation {
		t.Fatalf("removed package still triggers rebuild: %+v", after)
	}
	if err := os.WriteFile(options.project, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	added := watcher.refresh()
	if added.err != nil || added.generation == removed.generation || len(added.plan.packages) != 2 {
		t.Fatalf("added package not watched: %+v", added)
	}
	if err := os.WriteFile(filepath.Join(directory, "consumer", "main.go"), []byte("changed again"), 0600); err != nil {
		t.Fatal(err)
	}
	if after := watcher.refresh(); after.err != nil || after.generation == added.generation {
		t.Fatalf("new package input not watched: %+v", after)
	}
}
