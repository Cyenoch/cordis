//go:build integration

package pluginmeta

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestGettingStarted(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(root, "docs", "getting-started.md"))
	if err != nil {
		t.Fatal(err)
	}
	block := func(language, title string) []byte {
		t.Helper()
		pattern := "(?m)^```" + regexp.QuoteMeta(language) + ` title="` + regexp.QuoteMeta(title) + `"\r?\n([\s\S]*?)^` + "```"
		matches := regexp.MustCompile(pattern).FindAllSubmatch(source, -1)
		if len(matches) != 1 {
			t.Fatalf("expected one %s block titled %q, got %d", language, title, len(matches))
		}
		return matches[0][1]
	}
	run := func(dir, stdin, program string, args ...string) []byte {
		t.Helper()
		command := exec.CommandContext(t.Context(), program, args...)
		command.Dir = dir
		command.Stdin = strings.NewReader(stdin)
		command.Env = append(os.Environ(), "GOWORK=off")
		var diagnostics bytes.Buffer
		command.Stderr = &diagnostics
		out, err := command.Output()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s\n%s", program, args, err, out, &diagnostics)
		}
		return out
	}
	write := func(path string, content []byte) {
		t.Helper()
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	cli, worker := filepath.Join(bin, "cordis"+suffix), filepath.Join(bin, "worker"+suffix)
	run(root, "", "go", "build", "-o", cli, "./cmd/cordis")
	run(root, "", "go", "build", "-o", worker, "./cmd/cordis-worker")
	for _, kind := range []string{"native", "wasm"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "hello")
			run(root, "", cli, "init", "-module", "example.net/cordis-hello", "-sdk-version", "v0.0.0",
				"-id", "example.hello", "-publisher", "example.author", dir)
			run(dir, "", "go", "mod", "edit", "-replace=github.com/Cyenoch/cordis="+root)
			path := filepath.Join(dir, "plugin.toml")
			declaration, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			declaration = bytes.Split(declaration, []byte("[[contract.definitions]]"))[0]
			declaration = bytes.Replace(declaration, []byte("[runtime]"), append(block("toml", "plugin.toml（添加到顶层）"), []byte("\n[runtime]")...), 1)
			if kind == "wasm" {
				declaration = append(bytes.Split(declaration, []byte("[runtime]"))[0], block("toml", "plugin.toml（WASM runtime）")...)
			}
			write(path, declaration)
			for _, file := range []struct{ language, name string }{
				{"go", "main.go"}, {"toml", "contract.toml"}, {"toml", "config.toml"}, {"toml", "input.toml"},
			} {
				write(filepath.Join(dir, file.name), block(file.language, file.name))
			}
			run(dir, "", "go", "mod", "tidy")
			goos, goarch := runtime.GOOS, runtime.GOARCH
			if kind == "wasm" {
				goos, goarch = "wasip1", "wasm"
			}
			run(dir, "", cli, "build", "-go-package", ".", "-goos", goos, "-goarch", goarch, ".")
			run(dir, "", "go", "test", ".")
			archive := dir + ".zip"
			run(dir, "", cli, "pack", ".", archive)
			run(dir, "", cli, "inspect", archive)
			for _, input := range []struct {
				name, stdin string
				args        []string
			}{
				{"TOML files", "", []string{"-config-file", "config.toml", "-input-file", "input.toml"}},
				{"inline JSON", "", []string{"-config", `{"prefix":"hello, "}`, "-input", `{"method":"echo","input":{"text":"Cordis"}}`}},
				{"JSON stdin", `{"prefix":"hello, "}`, []string{"-config-file", "-", "-config-format", "json", "-input-file", "input.toml"}},
			} {
				t.Run(input.name, func(t *testing.T) {
					args := []string{"run", "-dev-unsigned", "-service", "example.echo"}
					if kind == "wasm" {
						args = append(args, "-worker", worker)
					}
					args = append(append(args, input.args...), archive)
					got := run(dir, input.stdin, cli, args...)
					var want, actual bytes.Buffer
					if err := json.Compact(&want, block("json", "调用结果")); err != nil {
						t.Fatal(err)
					}
					if err := json.Compact(&actual, got); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(actual.Bytes(), want.Bytes()) {
						t.Fatalf("tutorial result differs: %s", got)
					}
				})
			}
		})
	}
}
