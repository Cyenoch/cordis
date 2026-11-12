package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A mistyped -store fails instead of reporting, or hosting, an empty store,
// and leaves no directory behind.
func TestStoreCommandsRefuseAMissingStore(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "stat")
	trust := filepath.Join(t.TempDir(), "trust.json")
	if err := os.WriteFile(trust, []byte(`{"keys":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"status", "-store", missing},
		{"list", "-store", missing},
		{"host", "-store", missing, "-trust", trust},
	} {
		err := execute(t.Context(), args, strings.NewReader(""), io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "store "+missing+" does not exist") {
			t.Errorf("%s = %v", args[0], err)
		}
		if _, err := os.Stat(missing); !os.IsNotExist(err) {
			t.Fatalf("%s created the store: %v", args[0], err)
		}
	}
}
