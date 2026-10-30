//go:build integration

package main

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

func TestCLIProcesses(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "cordis")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, data)
	}
	run := func(t *testing.T, stdin string, args ...string) (int, string, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, binary, args...)
		command.Stdin = strings.NewReader(stdin)
		var out, diagnostics bytes.Buffer
		command.Stdout, command.Stderr = &out, &diagnostics
		err := command.Run()
		if ctx.Err() != nil {
			t.Fatalf("CLI did not finish: %v", args)
		}
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		return code, out.String(), diagnostics.String()
	}
	t.Run("help version and invalid flags", func(t *testing.T) {
		info, err := buildinfo.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}} {
			if code, out, stderr := run(t, "", args...); code != 0 || !strings.Contains(out, "cordis") || stderr != "" {
				t.Fatalf("%v: code=%d out=%q stderr=%q", args, code, out, stderr)
			}
		}
		for command := range commandUsage {
			for _, flag := range []string{"-h", "--help"} {
				if code, out, stderr := run(t, "", command, flag); code != 0 || !strings.Contains(out, "Usage:") || stderr != "" {
					t.Fatalf("%s %s: code=%d out=%q stderr=%q", command, flag, code, out, stderr)
				}
			}
			if code, out, stderr := run(t, "", command, "-not-a-flag"); code != 2 || out != "" || stderr == "" {
				t.Fatalf("%s invalid flag: code=%d out=%q stderr=%q", command, code, out, stderr)
			}
			if code, out, stderr := run(t, "", command, "-version"); code != 0 || !strings.Contains(out, info.Main.Version) || !strings.Contains(out, info.GoVersion) || stderr != "" {
				t.Fatalf("%s version: code=%d out=%q stderr=%q", command, code, out, stderr)
			}
		}
		if code, _, stderr := run(t, "", "help", "generate"); code != 0 || stderr != "" {
			t.Fatalf("subcommand help: code=%d stderr=%q", code, stderr)
		}
		for _, args := range [][]string{{"unknown"}, {"version", "extra"}, {"build", "."}, {"run", "archive.zip"}} {
			if code, out, stderr := run(t, "", args...); code != 2 || out != "" || stderr == "" {
				t.Fatalf("invalid args %v: code=%d out=%q stderr=%q", args, code, out, stderr)
			}
		}
	})
	t.Run("atomic generation and multiple formats", func(t *testing.T) {
		dir := t.TempDir()
		jsonInput := filepath.Join(dir, "first.json")
		tomlInput := filepath.Join(dir, "second.toml")
		writeCLIFile(t, jsonInput, `{"schema":"cordis-go.contract/v1","definitions":[{"id":"first"}]}`)
		writeCLIFile(t, tomlInput, "schema='cordis-go.contract/v1'\n[[definitions]]\nid='second'\n")
		output := filepath.Join(dir, "api.go")
		old := "package previous\n"
		writeCLIFile(t, output, old)
		if code, out, stderr := run(t, "", "generate", "-output", output, jsonInput, tomlInput); code != 0 || out != "" || stderr != "" {
			t.Fatalf("generate: code=%d out=%q stderr=%q", code, out, stderr)
		}
		generated, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		formatted, err := format.Source(generated)
		if err != nil || !bytes.Equal(generated, formatted) || !bytes.Contains(generated, []byte("NewFirstDefinition")) || !bytes.Contains(generated, []byte("NewSecondDefinition")) {
			t.Fatalf("unformatted or incomplete output: %v\n%s", err, generated)
		}
		if code, out, stderr := run(t, "", "generate", "-output", "-", jsonInput); code != 0 || !strings.Contains(out, "NewFirstDefinition") || stderr != "" {
			t.Fatalf("explicit stdout: code=%d out=%q stderr=%q", code, out, stderr)
		}
		for _, args := range [][]string{
			{"generate", "-output", output, jsonInput, jsonInput},
			{"generate", "-output", output, tomlInput, filepath.Join(dir, "missing.json")},
			{"generate", "-package", "not-a-package", "-output", output, jsonInput},
			{"generate", jsonInput},
		} {
			if code, out, stderr := run(t, "", args...); code == 0 || out != "" || stderr == "" {
				t.Fatalf("failed generation %v: code=%d out=%q stderr=%q", args, code, out, stderr)
			}
			got, err := os.ReadFile(output)
			if err != nil || !bytes.Equal(got, generated) {
				t.Fatal("failed generation damaged old output", err)
			}
		}
		separate := t.TempDir()
		for n, input := range []string{jsonInput, tomlInput} {
			path := filepath.Join(separate, strconv.Itoa(n)+"_gen.go")
			if code, out, stderr := run(t, "", "generate", "-package", "composed", "-output", path, input); code != 0 || out != "" || stderr != "" {
				t.Fatalf("separate generation: code=%d out=%q stderr=%q", code, out, stderr)
			}
		}
		root, err := filepath.Abs("../..")
		if err != nil {
			t.Fatal(err)
		}
		writeCLIFile(t, filepath.Join(separate, "go.mod"), "module cli-composed\n\ngo 1.27.3\n\nrequire github.com/Cyenoch/cordis v0.0.0\nreplace github.com/Cyenoch/cordis => "+strconv.Quote(root)+"\n")
		compile := exec.CommandContext(t.Context(), "go", "test", "-mod=mod", "./...")
		compile.Dir = separate
		compile.Env = append(os.Environ(), "GOWORK=off")
		if output, err := compile.CombinedOutput(); err != nil {
			t.Fatalf("CLI generated files cannot coexist: %v\n%s", err, output)
		}
		blocked := filepath.Join(dir, "blocked.go")
		if err := os.Mkdir(blocked, 0755); err != nil {
			t.Fatal(err)
		}
		writeCLIFile(t, filepath.Join(blocked, "keep"), old)
		if code, out, stderr := run(t, "", "generate", "-output", blocked, jsonInput); code != 1 || out != "" || stderr == "" {
			t.Fatalf("invalid output: code=%d out=%q stderr=%q", code, out, stderr)
		}
		if got, err := os.ReadFile(filepath.Join(blocked, "keep")); err != nil || string(got) != old {
			t.Fatal("failed output damaged existing content", err)
		}
	})
	t.Run("stdin ownership and inline conflicts", func(t *testing.T) {
		for _, args := range [][]string{
			{"-config-file", "-"},
			{"-input-file", "-"},
			{"-config-file", "-", "-config-format", "json", "-input-file", "-", "-input-format", "yaml"},
			{"-config", "{}", "-config-file", "missing.toml"},
			{"-input", "{}", "-input-file", "missing.yaml"},
			{"-config-format", "json"},
			{"-input-format", "json"},
			{"-startup-timeout", "0s"},
		} {
			args = append(append([]string{"run", "-dev-unsigned"}, args...), "missing.zip")
			if code, out, stderr := run(t, "", args...); code != 2 || out != "" || stderr == "" {
				t.Fatalf("input conflict %v: code=%d out=%q stderr=%q", args, code, out, stderr)
			}
		}
		if code, out, stderr := run(t, "{", "run", "-dev-unsigned", "-config-file", "-", "-config-format", "json", "missing.zip"); code != 1 || out != "" || !strings.Contains(stderr, "stdin (-config-file)") {
			t.Fatalf("stdin source: code=%d out=%q stderr=%q", code, out, stderr)
		}
	})
	t.Run("missing provider has finite startup and cancellation", func(t *testing.T) {
		archive := missingProviderArchive(t)
		// The default must cover a cold native startup; waiting it out here adds nothing.
		if code, out, _ := run(t, "", "run", "-h"); code != 0 || !regexp.MustCompile(`-startup-timeout duration\s+.*\(default 30s\)`).MatchString(out) {
			t.Fatalf("default startup timeout: code=%d out=%q", code, out)
		}
		start := time.Now()
		code, out, stderr := run(t, "", "run", "-dev-unsigned", "-startup-timeout", "2s", archive)
		if code != 1 || out != "" || !strings.Contains(stderr, "(timeout 2s)") || !strings.Contains(stderr, "deadline exceeded") || !strings.Contains(stderr, "example.missing") || !strings.Contains(stderr, `"minMinor":1`) || !strings.Contains(stderr, `"maxMinor":3`) || time.Since(start) > 10*time.Second {
			t.Fatalf("finite startup timeout: code=%d out=%q stderr=%q elapsed=%s", code, out, stderr, time.Since(start))
		}
		if len(stderr) > 64<<10 {
			t.Fatalf("unbounded diagnostics: %d bytes", len(stderr))
		}
		if code, out, stderr := run(t, "{}", "run", "-dev-unsigned", "-config-file", "-", "-config-format", "json", "-startup-timeout", "50ms", archive); code != 1 || out != "" || !strings.Contains(stderr, "deadline exceeded") {
			t.Fatalf("configured timeout/stdin: code=%d out=%q stderr=%q", code, out, stderr)
		}
		if runtime.GOOS == "windows" {
			t.Skip("os.Interrupt subprocess delivery is not supported on Windows")
		}
		ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "run", "-dev-unsigned", "-startup-timeout", "1m", archive)
		var stdout bytes.Buffer
		command.Stdout = &stdout
		reader, err := command.StderrPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, 8192)
		if n, err := reader.Read(buffer); n == 0 || err != nil {
			t.Fatalf("waiting diagnostic: n=%d err=%v", n, err)
		}
		start = time.Now()
		if err := command.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		if err := command.Wait(); err == nil || ctx.Err() != nil || time.Since(start) > 3*time.Second || stdout.Len() != 0 {
			t.Fatalf("cancellation: err=%v ctx=%v elapsed=%s stdout=%q", err, ctx.Err(), time.Since(start), &stdout)
		}
	})
	t.Run("init uses author source and never overwrites", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "plugin")
		args := []string{"init", "-module", "example.net/plugin", "-sdk-version", "v0.0.0", dir}
		if code, out, stderr := run(t, "", args...); code != 0 || !json.Valid([]byte(out)) || stderr != "" {
			t.Fatalf("init: code=%d out=%q stderr=%q", code, out, stderr)
		}
		author, err := packagehost.LoadAuthor(dir)
		if err != nil || author.Manifest.DefaultDefinition != "main" || len(author.Contracts) != 1 {
			t.Fatalf("author source: %+v, %v", author, err)
		}
		writeCLIFile(t, filepath.Join(dir, "main.go"), "package custom\n")
		if code, out, stderr := run(t, "", args...); code != 1 || out != "" || !strings.Contains(stderr, "existing file") {
			t.Fatalf("existing init: code=%d out=%q stderr=%q", code, out, stderr)
		}
		if got, err := os.ReadFile(filepath.Join(dir, "main.go")); err != nil || string(got) != "package custom\n" {
			t.Fatal("init damaged existing source", err)
		}
	})
	t.Run("init build test and pack in independent module", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "plugin")
		if code, _, stderr := run(t, "", "init", "-module", "example.net/plugin", "-sdk-version", "v0.0.0", dir); code != 0 {
			t.Fatalf("init: %d %s", code, stderr)
		}
		repo, err := filepath.Abs("../..")
		if err != nil {
			t.Fatal(err)
		}
		modPath := filepath.Join(dir, "go.mod")
		mod, err := os.OpenFile(modPath, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mod.WriteString("\nreplace " + sdkModule + " => " + strconv.Quote(repo) + "\n"); err != nil {
			t.Fatal(err)
		}
		if err := mod.Close(); err != nil {
			t.Fatal(err)
		}
		goCommand := func(args ...string) {
			t.Helper()
			command := exec.CommandContext(t.Context(), "go", args...)
			command.Dir = dir
			if data, err := command.CombinedOutput(); err != nil {
				t.Fatalf("go %v: %v\n%s", args, err, data)
			}
		}
		goCommand("mod", "tidy")
		code, out, stderr := run(t, "", "build", "-go-package", ".", "-goos", runtime.GOOS, "-goarch", runtime.GOARCH, dir)
		if code != 0 || stderr != "" {
			t.Fatalf("build: %d %s", code, stderr)
		}
		var descriptor packagehost.BuildDescriptor
		if err := json.Unmarshal([]byte(out), &descriptor); err != nil || descriptor.SDK.Module != sdkModule || descriptor.SDK.Version != "v0.0.0" || descriptor.SDK.Replace == nil || len(descriptor.Targets) != 1 || descriptor.Targets[0].SHA256 == "" {
			t.Fatalf("actual build description: %+v, %v", descriptor, err)
		}
		if _, err := os.Stat(filepath.Join(dir, packagehost.BuildDescriptorFile)); err != nil {
			t.Fatal(err)
		}
		goCommand("test", ".")
		archive := filepath.Join(t.TempDir(), "plugin.zip")
		if code, _, stderr := run(t, "", "pack", dir, archive); code != 0 {
			t.Fatalf("pack: %d %s", code, stderr)
		}
	})
}

func writeCLIFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestInspectCLIReportsReleaseFiles(t *testing.T) {
	archive := missingProviderArchive(t)
	var output, diagnostics bytes.Buffer
	if err := execute(t.Context(), []string{"inspect", archive}, strings.NewReader(""), &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	var inspection struct {
		Files []packagehost.IntegrityEntry `json:"files"`
	}
	if err := json.Unmarshal(output.Bytes(), &inspection); err != nil {
		t.Fatal(err)
	}
	expected := []string{packagehost.IntegrityFile, "never-started", packagehost.ManifestFile}
	if len(inspection.Files) != len(expected) {
		t.Fatalf("CLI omitted release contents: %s", &output)
	}
	for n, file := range inspection.Files {
		if file.Path != expected[n] || file.Size == 0 || len(file.SHA256) != 64 {
			t.Fatalf("invalid release file output: %+v", file)
		}
	}
}

func missingProviderArchive(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	manifest := contract.Manifest{
		Schema: contract.PackageSchema, ID: "example.waiting", Version: "1.0.0", Publisher: "example",
		Protocol: contract.Protocol{Major: 1}, DefaultDefinition: "main",
		Definitions: []contract.DefinitionSpec{{ID: "main", ConfigSchema: json.RawMessage(`{"type":"object"}`),
			Requires: []contract.Consumer{{Requirement: contract.Requirement{Name: "example.missing", Major: 1, MinMinor: 1, MaxMinor: 3}, Contract: strings.Repeat("a", 64)}},
			Provides: []contract.Service{},
		}},
		Runtime: contract.RuntimeSpec{Kind: "native", Artifacts: []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: "never-started"}}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, filepath.Join(dir, "plugin.json"), string(raw))
	writeCLIFile(t, filepath.Join(dir, "never-started"), "this artifact must not execute without its provider")
	archive := filepath.Join(t.TempDir(), "waiting.zip")
	if _, err := packagehost.Pack(t.Context(), dir, archive); err != nil {
		t.Fatal(err)
	}
	return archive
}
