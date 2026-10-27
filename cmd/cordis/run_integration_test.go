//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

func TestRunCallsDeclaredMinorTwoFromIndependentPlugin(t *testing.T) {
	source := t.TempDir()
	binary := "chain"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	command := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(source, binary), ".")
	command.Dir = "../../examples/plugins/chain"
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build author module: %s: %v", out, err)
	}
	manifest := contract.Manifest{Schema: contract.PackageSchema, ID: "example.cli", Version: "1.0.0", Publisher: "example", Protocol: contract.Protocol{Major: 1}, DefaultDefinition: "provider", Definitions: []contract.DefinitionSpec{{ID: "provider", ConfigSchema: json.RawMessage(`{"type":"object","properties":{"label":{"type":"string"}}}`), Requires: []contract.Consumer{}, Provides: []contract.Service{{Name: "chain.a", Major: 1, Minor: 2}}}}, Runtime: contract.RuntimeSpec{Kind: "native", Artifacts: []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: binary}}}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "plugin.zip")
	if _, err = packagehost.Pack(t.Context(), source, archive); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(t.TempDir(), "cordis")
	if runtime.GOOS == "windows" {
		cli += ".exe"
	}
	buildCLI := exec.CommandContext(t.Context(), "go", "build", "-o", cli, ".")
	if out, err := buildCLI.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %s: %v", out, err)
	}
	data := t.TempDir()
	configuration := filepath.Join(data, "config.toml")
	input := filepath.Join(data, "input.yaml")
	writeCLIFile(t, configuration, "label = 'from-file'\n")
	writeCLIFile(t, input, "method: execute\ninput:\n  operation: stats\n")
	for _, tt := range []struct {
		name, stdin, label string
		flags              []string
	}{
		{name: "inline JSON", flags: []string{"-input", `{"method":"execute","input":{"operation":"stats"}}`}},
		{name: "TOML config and YAML input", label: "from-file", flags: []string{"-config-file", configuration, "-input-file", input}},
		{name: "JSON config on stdin", stdin: `{"label":"from-stdin"}`, label: "from-stdin", flags: []string{"-config-file", "-", "-config-format", "json", "-input-file", input}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"run", "-dev-unsigned", "-definition", "provider", "-service", "chain.a"}, tt.flags...)
			args = append(args, archive)
			command := exec.CommandContext(t.Context(), cli, args...)
			command.Stdin = strings.NewReader(tt.stdin)
			var out, diagnostics bytes.Buffer
			command.Stdout, command.Stderr = &out, &diagnostics
			if err := command.Run(); err != nil {
				t.Fatalf("run: %v: %s", err, &diagnostics)
			}
			var result struct {
				Acquired, Released, Live int
				Label                    string
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err, out.String())
			}
			if result.Acquired != 0 || result.Released != 0 || result.Live != 0 || result.Label != tt.label {
				t.Fatal(result)
			}
		})
	}
}
