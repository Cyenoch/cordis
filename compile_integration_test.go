//go:build integration

package cordis_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The typed keys promise that a mismatch is a build error. These fixtures
// are compiled, not run.
func TestTypedKeysAreCheckedAtCompileTime(t *testing.T) {
	rejected := map[string]string{
		"listener": "func(context.Context, string) error",
		"service":  "does not implement database",
		"setting":  "as string value",
		"update":   "as first value",
	}
	for name, want := range rejected {
		t.Run(name, func(t *testing.T) {
			out, err := exec.CommandContext(t.Context(), "go", "build", "-o", t.TempDir(), "./testdata/compile/"+name).CombinedOutput()
			if err == nil {
				t.Fatal("fixture compiled, want a type error")
			}
			if !strings.Contains(string(out), want) {
				t.Fatalf("build error does not mention %q:\n%s", want, out)
			}
		})
	}
	t.Run("concrete value for interface key", func(t *testing.T) {
		if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", t.TempDir(), "./testdata/compile/concrete").CombinedOutput(); err != nil {
			t.Fatalf("fixture must compile without explicit type arguments: %v\n%s", err, out)
		}
	})
}
