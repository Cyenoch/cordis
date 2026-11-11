package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/Cyenoch/cordis/schema/gocontract"
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

// init copies the scaffold's bindings out as they are committed, so they must
// be what generating from the scaffold's source gives.
func TestScaffoldBindingsAreCurrent(t *testing.T) {
	p, err := gocontract.Load(t.Context(), "scaffold", gocontract.Target{})
	if err != nil {
		t.Fatal(err)
	}
	source, err := p.Source()
	if err != nil {
		t.Fatal(err)
	}
	declared, err := p.Contract()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]byte{gocontract.GoFile: source, gocontract.ContractFile: declared} {
		if got, err := os.ReadFile(filepath.Join("scaffold", name)); err != nil || !bytes.Equal(got, want) {
			t.Errorf("scaffold/%s is stale (%v); run cordis generate cmd/cordis/scaffold", name, err)
		}
	}
}
