//go:build integration

package packagehost

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func FuzzExtractZIP(f *testing.F) {
	f.Add([]byte("PK\x05\x06\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"))
	f.Add([]byte("not a zip"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		root := t.TempDir()
		archive := filepath.Join(root, "input.zip")
		dest := filepath.Join(root, "stage")
		if err := os.WriteFile(archive, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(dest, 0700); err != nil {
			t.Fatal(err)
		}
		limits := Limits{ArchiveBytes: 1 << 20, FileBytes: 1 << 20, ExpandedBytes: 2 << 20, Entries: 128}
		err := extractZIP(context.Background(), archive, dest, limits)
		// Invalid input may leave only private staging files for the caller to remove.
		// No archive data can create siblings or overwrite the source archive.
		siblings, readErr := os.ReadDir(root)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, entry := range siblings {
			if entry.Name() != "input.zip" && entry.Name() != "stage" {
				t.Fatalf("extraction escaped stage: %s (%v)", entry.Name(), err)
			}
		}
		after, readErr := os.ReadFile(archive)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !equalBytes(after, data) {
			t.Fatal("extraction overwrote archive")
		}
	})
}
