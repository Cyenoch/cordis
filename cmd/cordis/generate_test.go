package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicOutputKeepsExistingFileOnUnwritableDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "api.go")
	old := []byte("package previous\n")
	if err := os.WriteFile(path, old, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	probe, err := os.CreateTemp(dir, "probe-*")
	if err == nil {
		probe.Close()
		os.Remove(probe.Name())
		t.Skip("filesystem/user does not enforce directory write permissions")
	}
	if err := atomicOutput(path, []byte("package replacement\n")); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, old) {
		t.Fatalf("old output changed: %q, %v", got, err)
	}
}
