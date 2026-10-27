//go:build integration

package packagehost

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildActualSDKNativeAndWASMWithoutRunningArtifact(t *testing.T) {
	t.Parallel()
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, goos, goarch, runtime string }{
		{"native", runtime.GOOS, runtime.GOARCH, "kind='native'\n[[runtime.artifacts]]\nos='" + runtime.GOOS + "'\narch='" + runtime.GOARCH + "'\npath='plugin'\n"},
		{"wasm", "wasip1", "wasm", "kind='wasm'\nentry='plugin.wasm'\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			source := "id='demo'\nversion='1.2.3'\npublisher='example'\n[runtime]\n" + tt.runtime + "[[contract.definitions]]\nid='main'\n"
			writeAuthorFile(t, dir, "plugin.toml", source)
			writeAuthorFile(t, dir, "go.mod", "module example.com/buildfixture\n\ngo 1.27.3\n\nrequire github.com/Cyenoch/cordis v0.0.0\nreplace github.com/Cyenoch/cordis => "+filepath.ToSlash(repo)+"\n")
			writeAuthorFile(t, dir, "main.go", `package main
import (
    "context"
    "github.com/Cyenoch/cordis/contract"
    "github.com/Cyenoch/cordis/sdk"
)
func main() {
    panic("the build and pack APIs must never execute this artifact")
    _ = sdk.Serve(context.Background(), NewMainDefinition(func(context.Context, contract.Scope, MainConfig) error { return nil }))
}
`)
			o := BuildOptions{GoPackage: ".", GOOS: tt.goos, GOARCH: tt.goarch}
			if _, err := runGo(context.Background(), dir, o, "mod", "tidy"); err != nil {
				t.Fatal(err)
			}
			d, err := Build(context.Background(), dir, o)
			if err != nil {
				t.Fatal(err)
			}
			if d.SDK.Module != sdkModule || d.SDK.Version != "v0.0.0" || d.SDK.Replace == nil || d.SDK.Replace.Path != repo || len(d.SDK.Features) == 0 {
				t.Fatalf("actual SDK dependency not recorded: %+v", d.SDK)
			}
			archive := filepath.Join(t.TempDir(), "built.zip")
			if _, err := Pack(context.Background(), dir, archive); err != nil {
				t.Fatal(err)
			}
			old, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			writeAuthorFile(t, dir, "plugin.toml", strings.Replace(source, "id='main'", "id='changed'", 1))
			if _, err := Pack(context.Background(), dir, archive); err == nil {
				t.Fatal("old generated code accepted after source change")
			}
			got, err := os.ReadFile(archive)
			if err != nil || string(got) != string(old) {
				t.Fatal("failed pack replaced prior archive")
			}
			if tt.name == "native" {
				writeAuthorFile(t, dir, "plugin.toml", source)
				mismatched := d
				mismatched.SDK.Version = "v99.0.0"
				if err := WriteBuildDescriptor(dir, mismatched); err != nil {
					t.Fatal(err)
				}
				if _, err := Pack(context.Background(), dir, archive); err == nil || !strings.Contains(err.Error(), "SDK module does not match") {
					t.Fatalf("accepted incorrect actual SDK identity: %v", err)
				}
				if err := WriteBuildDescriptor(dir, d); err != nil {
					t.Fatal(err)
				}
				// A generated file outside the built package must not masquerade
				// as proof that the artifact contains the current declarations.
				o.GeneratedOutput = "unused/cordis_gen.go"
				if _, err := Build(context.Background(), dir, o); err == nil || !strings.Contains(err.Error(), "compiles generated contract") {
					t.Fatalf("accepted uncompiled generated declarations: %v", err)
				}
			}
		})
	}
}
