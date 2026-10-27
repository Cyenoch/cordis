//go:build integration

package conformance_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDocumentationTutorials(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	versionFile, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	version := regexp.MustCompile(`(?m)^go (\S+)$`).FindSubmatch(versionFile)
	if len(version) != 2 {
		t.Fatal("go.mod is missing its Go version")
	}
	cli := filepath.Join(t.TempDir(), "cordis")
	if runtime.GOOS == "windows" {
		cli += ".exe"
	}
	runCommand(t, root, "go", "build", "-o", cli, "./cmd/cordis")
	for _, name := range []string{"composition-tutorial", "access-tutorial", "generated-service"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(root, "docs", name+".md"))
			if err != nil {
				t.Fatal(err)
			}
			block := func(language, title string) string {
				t.Helper()
				pattern := "(?m)^```" + regexp.QuoteMeta(language) + ` title="` + regexp.QuoteMeta(title) + `"\r?\n([\s\S]*?)^` + "```"
				matches := regexp.MustCompile(pattern).FindAllStringSubmatch(string(source), -1)
				if len(matches) != 1 {
					t.Fatalf("expected one %s block titled %q, got %d", language, title, len(matches))
				}
				return matches[0][1]
			}
			directory := t.TempDir()
			moduleName := "example.com/cordis-" + strings.TrimSuffix(name, "-tutorial")
			if name == "generated-service" {
				moduleName = "example.com/cordis-generated"
			}
			module := fmt.Sprintf("module %s\n\ngo %s\n\nrequire github.com/Cyenoch/cordis v0.0.0\nreplace github.com/Cyenoch/cordis => %s\n",
				moduleName, version[1], strconv.Quote(filepath.ToSlash(root)))
			write := func(name, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("go.mod", module)
			write("main.go", block("go", "main.go"))
			if name == "generated-service" {
				write("echo.schema.json", block("json", "echo.schema.json"))
				if err := os.Mkdir(filepath.Join(directory, "echo"), 0700); err != nil {
					t.Fatal(err)
				}
				write("generate.go", block("go", "generate.go"))
				runCommand(t, directory, "go", "run", "generate.go")
			}
			runCommand(t, directory, "go", "mod", "tidy")
			output := runCommand(t, directory, "go", "run", "-race", ".")
			normalize := func(value string) string {
				return strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
			}
			if normalize(output) != normalize(block("text", "预期输出")) {
				t.Fatalf("tutorial output differs from documentation:\n%s", output)
			}
		})
	}
}

func runCommand(t *testing.T, directory, name string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), name, args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
	return string(output)
}
