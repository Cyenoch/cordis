package main

import (
	"embed"
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
	"github.com/Cyenoch/cordis/schema/gocontract"
)

// sdkGoVersion is the go directive of this module, the oldest Go the SDK
// builds with. init writes it so a plugin asks for no newer Go than it needs.
const sdkGoVersion = "1.27.3"

func initCommand(flags *flag.FlagSet, parse func() (bool, error), out io.Writer) error {
	module := flags.String("module", "", "Go module path for the plugin")
	sdkVersion := flags.String("sdk-version", "", "Cordis module version (default: actual CLI dependency version)")
	id := flags.String("id", "example.plugin", "plugin package ID")
	publisher := flags.String("publisher", "example", "publisher identity")
	if done, err := parse(); done {
		return err
	}
	if flags.NArg() != 1 || *module == "" || strings.ContainsAny(*module, " \t\r\n\\\"") || strings.HasPrefix(*module, "-") {
		return &usageError{message: commandUsage["init"]}
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
	artifact := "plugin"
	if runtime.GOOS == "windows" {
		artifact += ".exe"
	}
	runtimeSource := fmt.Sprintf("[runtime]\nkind = \"native\"\n\n[[runtime.artifacts]]\nos = %q\narch = %q\npath = %q\n", runtime.GOOS, runtime.GOARCH, artifact)
	manifest := "id = " + strconv.Quote(*id) + "\nversion = \"0.1.0\"\npublisher = " + strconv.Quote(*publisher) + "\n\n" + runtimeSource
	files := []struct{ name, content string }{
		{"plugin.toml", manifest},
		{"go.mod", "module " + strconv.Quote(*module) + "\n\ngo " + sdkGoVersion + "\n\nrequire " + sdkModule + " " + *sdkVersion + "\n"},
	}
	dir := flags.Arg(0)
	for _, name := range []string{"plugin.toml", "plugin.yaml", "plugin.yml", "plugin.json", "main.go", "main_test.go", "go.mod", gocontract.GoFile, gocontract.ContractFile} {
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
	for _, name := range []string{"main.go", "main_test.go", gocontract.GoFile, gocontract.ContractFile} {
		content, err := scaffold.ReadFile("scaffold/" + name)
		if err != nil {
			return err
		}
		files = append(files, struct{ name, content string }{name, string(content)})
		if name == gocontract.ContractFile {
			if err := os.WriteFile(filepath.Join(stage, name), content, 0600); err != nil {
				return err
			}
		}
	}
	if _, err = packagehost.LoadAuthor(stage); err != nil {
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

// The scaffold is a package of this module, so it is compiled and tested
// with it; init copies it out, generated bindings included.
//
//go:embed scaffold/main.go scaffold/main_test.go scaffold/cordis_gen.go scaffold/cordis_gen.json
var scaffold embed.FS
