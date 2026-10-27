package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/Cyenoch/cordis/packagehost"
)

func initCommand(flags *flag.FlagSet, parse func() (bool, error), out io.Writer) error {
	module := flags.String("module", "", "Go module path for the plugin")
	sdkVersion := flags.String("sdk-version", "", "Cordis module version (default: actual CLI dependency version)")
	id := flags.String("id", "example.plugin", "plugin package ID")
	publisher := flags.String("publisher", "example", "publisher identity")
	kind := flags.String("runtime", "native", "artifact runtime: native or wasm; grants no execution permission")
	if done, err := parse(); done {
		return err
	}
	if flags.NArg() != 1 || *module == "" || strings.ContainsAny(*module, " \t\r\n\\\"") || strings.HasPrefix(*module, "-") {
		return &usageError{message: commandUsage["init"]}
	}
	if *kind != "native" && *kind != "wasm" {
		return &usageError{message: "init -runtime must be native or wasm"}
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return fmt.Errorf("init: Go build metadata unavailable")
	}
	if *sdkVersion == "" {
		if dependency, found := cordisModule(info); found {
			*sdkVersion = dependency.Version
		}
	}
	if !strings.HasPrefix(*sdkVersion, "v") || packagehost.ValidateVersion(strings.TrimPrefix(*sdkVersion, "v")) != nil {
		return &usageError{message: "init needs -sdk-version vX.Y.Z when the CLI was built from an unversioned checkout"}
	}
	goVersion := strings.TrimPrefix(info.GoVersion, "go")
	if err := packagehost.ValidateVersion(goVersion); err != nil {
		return fmt.Errorf("init: cannot use Go build version %q in go.mod", info.GoVersion)
	}
	artifact := "plugin"
	if runtime.GOOS == "windows" {
		artifact += ".exe"
	}
	runtimeSource := fmt.Sprintf("[runtime]\nkind = \"native\"\n\n[[runtime.artifacts]]\nos = %q\narch = %q\npath = %q\n", runtime.GOOS, runtime.GOARCH, artifact)
	if *kind == "wasm" {
		runtimeSource = "[runtime]\nkind = \"wasm\"\nentry = \"plugin.wasm\"\n"
	}
	manifest := "id = " + strconv.Quote(*id) + "\nversion = \"0.1.0\"\npublisher = " + strconv.Quote(*publisher) + "\n\n" + runtimeSource + "\n[[contract.definitions]]\nid = \"main\"\n"
	files := []struct{ name, content string }{
		{"plugin.toml", manifest},
		{"main.go", scaffoldMain},
		{"main_test.go", scaffoldTest},
		{"go.mod", "module " + strconv.Quote(*module) + "\n\ngo " + goVersion + "\n\nrequire " + sdkModule + " " + *sdkVersion + "\n"},
	}
	dir := flags.Arg(0)
	for _, name := range []string{"plugin.toml", "plugin.yaml", "plugin.yml", "plugin.json", "main.go", "main_test.go", "go.mod", "cordis_gen.go"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return fmt.Errorf("init: refusing existing file %q", filepath.Join(dir, name))
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	stage, err := os.MkdirTemp("", "cordis-init-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.WriteFile(filepath.Join(stage, "plugin.toml"), []byte(manifest), 0600); err != nil {
		return err
	}
	_, err = packagehost.LoadAuthor(stage)
	if err != nil {
		return fmt.Errorf("init declaration: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for _, source := range files {
		file, err := os.OpenFile(filepath.Join(dir, source.name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("init %s: %w", source.name, err)
		}
		_, writeErr := io.WriteString(file, source.content)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return fmt.Errorf("init %s: %w", source.name, err)
		}
	}
	return writeJSON(out, map[string]string{"directory": dir, "source": "plugin.toml"})
}

const scaffoldMain = `package main

import (
	"context"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

func start(ctx context.Context, scope contract.Scope, config MainConfig) error {
	return nil
}

func main() {
	sdk.Main(NewMainDefinition(start))
}
`

const scaffoldTest = `package main

import (
	"context"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/plugintest"
)

func TestStart(t *testing.T) {
	harness, err := plugintest.New(t.Context(), NewMainDefinition(start), plugintest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := harness.Close(ctx); err != nil {
			t.Error(err)
		}
	})
}
`
