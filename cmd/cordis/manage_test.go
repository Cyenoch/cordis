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

// The trust file approves exact digests, which the store trusts without a
// signature, and rejects a digest that cannot name any content.
func TestTrustFileApprovesExactDigests(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("ab", 32)
	for content, want := range map[string]string{
		`{"digests":{"` + digest + `":true}}`:                  "",
		`{"digests":{"` + strings.ToUpper(digest) + `":true}}`: "is not a lowercase sha256",
		`{"digests":{"abc":true}}`:                             "is not a lowercase sha256",
	} {
		file := filepath.Join(t.TempDir(), "trust.json")
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		trust, err := loadTrust(file)
		if want == "" && (err != nil || !trust.Digests[digest]) || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Fatalf("%s: %+v, %v", content, trust.Digests, err)
		}
	}
}
