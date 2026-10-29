//go:build integration

package packagehost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverDiagnosesAllSourcesWithoutTrustOrInstallation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := sourceFixture(t, manifestFixture("demo", "1.0.0"))
	archive := filepath.Join(t.TempDir(), "one.zip")
	p, err := Pack(ctx, dir, archive)
	if err != nil {
		t.Fatal(err)
	}
	changed := sourceFixture(t, manifestFixture("demo", "1.0.0"))
	if err := os.WriteFile(filepath.Join(changed, "plugin"), []byte("different"), 0600); err != nil {
		t.Fatal(err)
	}
	sources := []Source{
		{Path: archive}, {Path: dir, Directory: true},
		{Path: filepath.Join(t.TempDir(), "missing.zip")}, {Path: changed, Directory: true},
	}
	results, err := Discover(ctx, sources)
	if err != nil || len(results) != len(sources) {
		t.Fatalf("discover: %+v, %v", results, err)
	}
	for i, result := range results {
		if result.Source != sources[i] || result.Package.Directory != "" || result.Err == nil {
			t.Fatalf("wrong source diagnostic: %+v", result)
		}
		if i != 2 && !strings.Contains(result.Err.Error(), "same-version content conflict") {
			t.Fatal(result.Err)
		}
	}
	if results[0].Package.Ref != p.Ref || results[1].Package.Ref != p.Ref {
		t.Fatal("directory discovery did not use the same integrity identity")
	}
	if _, err := os.Stat(filepath.Join(dir, IntegrityFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("discovery modified developer source", err)
	}
	s := openFixture(t, t.TempDir(), TrustPolicy{})
	results, err = s.Discover(ctx, sources[:2])
	if err != nil || results[0].Err != nil || results[1].Err != nil {
		t.Fatalf("identical candidates rejected: %+v, %v", results, err)
	}
	installed, err := s.Installed()
	if err != nil || len(installed) != 0 {
		t.Fatal("discovery installed content", installed, err)
	}
	if _, err := s.Install(ctx, archive); err == nil {
		t.Fatal("discovery granted unsigned content trust")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Discover(canceled, sources); !errors.Is(err, context.Canceled) {
		t.Fatal("discovery ignored cancellation", err)
	}
}
