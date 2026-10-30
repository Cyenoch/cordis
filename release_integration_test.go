//go:build integration

package cordis_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

func TestNativeReleaseResourceExample(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "resource")
	if err := os.CopyFS(source, os.DirFS("examples/plugins/resource")); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "go", "mod", "edit", "-replace=github.com/Cyenoch/cordis="+root)
	command.Dir = source
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("prepare release example: %v\n%s", err, out)
	}
	author, err := packagehost.LoadAuthor(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range author.Manifest.Runtime.Artifacts {
		if _, err := packagehost.Build(t.Context(), source, packagehost.BuildOptions{
			GoPackage: ".", GOOS: target.OS, GOARCH: target.Arch,
		}); err != nil {
			t.Fatalf("build release target %s/%s: %v", target.OS, target.Arch, err)
		}
	}
	archive := filepath.Join(t.TempDir(), "resource.zip")
	if _, err := packagehost.Pack(t.Context(), source, archive); err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := packagehost.Sign(t.Context(), archive, "author", private)
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := packagehost.Inspect(t.Context(), archive)
	if err != nil || inspected.Ref != signed.Ref {
		t.Fatalf("inspect signed release: %v", err)
	}
	if len(inspected.Files) != len(author.Manifest.Runtime.Artifacts)+4 {
		t.Fatalf("wrong release file count: %d", len(inspected.Files))
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	h, err := cordis.Open(cordis.Options{Directory: t.TempDir(), Trust: packagehost.TrustPolicy{
		Keys:       map[string]packagehost.TrustedKey{"author": {PublicKey: public, Publisher: author.Manifest.Publisher}},
		Publishers: map[string][]string{author.Manifest.Publisher: {author.Manifest.ID}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	p, err := h.Install(t.Context(), archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Select(t.Context(), p.Ref); err != nil {
		t.Fatal(err)
	}
	instance, err := h.Enable(t.Context(), contract.Entry{ID: "resource", Package: p.Ref.ID})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := instance.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	response, err := h.Call(ctx, contract.Requirement{Name: "example.resource", Major: 1},
		json.RawMessage(`{"method":"read","input":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	var content string
	if err := json.Unmarshal(response, &content); err != nil || strings.TrimSpace(content) != "Hello from a signed Cordis package." {
		t.Fatalf("resource not read from installed snapshot: %s %v", response, err)
	}
}
