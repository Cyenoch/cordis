//go:build integration

package packagehost

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type maliciousEntry struct {
	name      string
	data      string
	mode      os.FileMode
	encrypted bool
}

func maliciousZIP(t *testing.T, entries []maliciousEntry) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "malicious.zip")
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	for _, entry := range entries {
		h := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		h.SetMode(entry.mode)
		if entry.encrypted {
			h.Flags |= 1
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(entry.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err = errors.Join(zw.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
	return name
}
func TestExtractZIPRejectsMaliciousFixtures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		entries []maliciousEntry
	}{
		{"traversal", []maliciousEntry{{name: "../outside", data: "bad"}}},
		{"absolute", []maliciousEntry{{name: "/outside", data: "bad"}}},
		{"symlink", []maliciousEntry{{name: "link", data: "../outside", mode: os.ModeSymlink | 0777}}},
		{"duplicate", []maliciousEntry{{name: "a", data: "one"}, {name: "a", data: "two"}}},
		{"case conflict", []maliciousEntry{{name: "A", data: "one"}, {name: "a", data: "two"}}},
		{"parent case conflict", []maliciousEntry{{name: "Dir/a", data: "one"}, {name: "dir/b", data: "two"}}},
		{"device", []maliciousEntry{{name: "NUL.txt", data: "bad"}}},
		{"non NFC", []maliciousEntry{{name: "e\u0301.txt", data: "bad"}}},
		{"trailing dot", []maliciousEntry{{name: "bad.", data: "bad"}}},
		{"file directory", []maliciousEntry{{name: "a", data: "one"}, {name: "a/b", data: "two"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "stage")
			if err := os.Mkdir(dest, 0700); err != nil {
				t.Fatal(err)
			}
			if err := extractZIP(context.Background(), maliciousZIP(t, tt.entries), dest, DefaultLimits()); err == nil {
				t.Fatal("accepted malicious archive")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "outside")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("escaped destination: %v", err)
			}
		})
	}
}
func TestExtractZIPEnforcesActualBytesAndRecovers(t *testing.T) {
	t.Parallel()
	l := DefaultLimits()
	l.ExpandedBytes = 8
	l.FileBytes = 8
	bad := maliciousZIP(t, []maliciousEntry{{name: "a", data: strings.Repeat("x", 10000)}})
	if err := extractZIP(context.Background(), bad, t.TempDir(), l); err == nil {
		t.Fatal("accepted oversized content")
	}
	good := maliciousZIP(t, []maliciousEntry{{name: "a", data: "small"}})
	dest := t.TempDir()
	if err := extractZIP(context.Background(), good, dest, l); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "a"))
	if err != nil || string(data) != "small" {
		t.Fatalf("failed valid archive after rejection: %q %v", data, err)
	}
}
func TestImportDirectoryRejectsLinks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := copyDirectory(context.Background(), dir, t.TempDir(), DefaultLimits()); err == nil {
		t.Fatal("copied symlink")
	}
	hard := t.TempDir()
	if err := os.Link(outside, filepath.Join(hard, "linked")); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	if err := copyDirectory(context.Background(), hard, t.TempDir(), DefaultLimits()); err == nil {
		t.Fatal("copied hardlink")
	}
}
func TestPackDeterministicAndSnapshotImmutable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := sourceFixture(t, manifestFixture("demo", "1.0.0"))
	a := filepath.Join(t.TempDir(), "a.zip")
	b := filepath.Join(t.TempDir(), "b.zip")
	p, err := Pack(ctx, dir, a)
	if err != nil {
		t.Fatal(err)
	}
	q, err := Pack(ctx, dir, b)
	if err != nil {
		t.Fatal(err)
	}
	if p.Ref != q.Ref {
		t.Fatal("nondeterministic digest")
	}
	first, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(b)
	if err != nil {
		t.Fatal(err)
	}
	if !equalBytes(first, second) {
		t.Fatal("nondeterministic archive")
	}
	s := openFixture(t, t.TempDir(), TrustPolicy{Publishers: map[string][]string{"example": {"demo"}}, DevDigests: map[string]bool{p.Ref.Digest: true}})
	installed, err := s.ImportDirectory(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Select(ctx, installed.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "plugin.wasm"), []byte("modified source"), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot(ctx, installed.Ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := snap.Close(); err != nil {
			t.Error(err)
		}
	})
	bytes, err := os.ReadFile(filepath.Join(snap.Directory, "plugin.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "fixture artifact" {
		t.Fatal("mutable developer source executed")
	}
}
