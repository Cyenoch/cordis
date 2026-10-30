package native

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestReservedPackageEnvironmentCannotBeOverridden(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin"), []byte("not executed"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{PackageDirectoryEnv, strings.ToLower(PackageDirectoryEnv)} {
		runtime := New(Options{Environment: map[string]string{key: "wrong directory"}})
		if _, err := runtime.Start(t.Context(), contract.Launch{Directory: dir, Artifact: "plugin"}); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("reserved environment not rejected before execution: %v", err)
		}
	}
}
