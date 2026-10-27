//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	manifest := contract.Manifest{Schema: contract.PackageSchema, ID: "example.cli", Version: "1.0.0", Publisher: "example", Protocol: contract.Protocol{Major: 1}, DefaultDefinition: "provider", Definitions: []contract.DefinitionSpec{{ID: "provider", Provides: []contract.Service{{Name: "chain.a", Major: 1, Minor: 2}}}}, Runtime: contract.RuntimeSpec{Kind: "native", Artifacts: []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: binary}}}}
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
	var out, diagnostics bytes.Buffer
	if err = execute(t.Context(), []string{"run", "-dev-unsigned", "-definition", "provider", "-service", "chain.a", "-input", `{"operation":"stats"}`, archive}, &out, &diagnostics); err != nil {
		t.Fatalf("run: %v: %s", err, &diagnostics)
	}
	var result struct {
		Acquired, Released, Live int
		Label                    string
	}
	if err = json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err, out.String())
	}
	if result.Acquired != 0 || result.Released != 0 || result.Live != 0 {
		t.Fatal(result)
	}
}
