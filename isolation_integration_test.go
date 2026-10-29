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
	"runtime"
	"testing"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/examples/pluginmeta"
	"github.com/Cyenoch/cordis/packagehost"
)

func TestNativeIsolationRestoreAndChildBoundaries(t *testing.T) {
	source := t.TempDir()
	artifact := "plugin"
	if runtime.GOOS == "windows" {
		artifact += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(source, artifact), ".")
	build.Dir = "examples/plugins/chain"
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build native composition example: %v\n%s", err, output)
	}
	manifest := pluginmeta.Chain()
	manifest.Runtime.Artifacts = []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: artifact}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "chain.zip")
	if _, err := packagehost.Pack(t.Context(), source, archive); err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := packagehost.Sign(t.Context(), archive, "composition", private)
	if err != nil {
		t.Fatal(err)
	}
	trust := packagehost.TrustPolicy{
		Keys:       map[string]packagehost.TrustedKey{"composition": {PublicKey: public, Publisher: manifest.Publisher}},
		Publishers: map[string][]string{manifest.Publisher: {manifest.ID}},
	}
	directory := t.TempDir()
	h, err := cordis.Open(cordis.Options{Directory: directory, Trust: trust})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := h.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if _, err := h.Install(ctx, archive); err != nil {
		t.Fatal(err)
	}
	if err := h.Select(ctx, pkg.Ref); err != nil {
		t.Fatal(err)
	}
	enable := func(entry contract.Entry) contract.Instance {
		t.Helper()
		entry.Package = manifest.ID
		instance, err := h.Enable(ctx, entry)
		if err != nil {
			t.Fatal(err)
		}
		if err := instance.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		return instance
	}
	for _, group := range []string{"a", "b"} {
		config := json.RawMessage(`{}`)
		if group == "a" {
			config = json.RawMessage(`{"childIsolate":["chain.b@1"]}`)
		}
		enable(contract.Entry{ID: group, Definition: "optional-parent", Isolate: []string{"chain.a@1", "chain.b@1", "chain.c@1"}, Config: config})
		enable(contract.Entry{ID: group + "-provider", Parent: group, Definition: "provider", Config: json.RawMessage(`{"label":"` + group + `"}`)})
		child, err := h.Instance(group + ".optional")
		if err != nil {
			t.Fatal(err)
		}
		if err := child.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	enable(contract.Entry{ID: "b-consumer", Parent: "b", Definition: "consumer"})
	caller := func(id string) contract.Caller {
		t.Helper()
		view, err := h.Caller(id)
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	requirement := contract.Requirement{Name: "chain.b", Major: 1, MinMinor: 1, MaxMinor: 3}
	statsInput := json.RawMessage(`{"method":"execute","input":{"operation":"stats"}}`)
	label := func(view contract.Caller, name string) string {
		t.Helper()
		req := requirement
		req.Name = name
		output, err := view.Call(ctx, req, statsInput)
		if err != nil {
			t.Fatal(err)
		}
		var stats struct {
			Label string `json:"label"`
		}
		if err := json.Unmarshal(output, &stats); err != nil {
			t.Fatal(err)
		}
		return stats.Label
	}
	aView, bView := caller("a.optional"), caller("b-consumer")
	if label(aView, "chain.b") != "a" || label(bView, "chain.c") != "b" {
		t.Fatal("native consumers crossed isolation groups")
	}
	if _, err := h.Call(ctx, requirement, statsInput); contract.ErrorCode(err) != contract.NotReady {
		t.Fatalf("root call selected an isolated provider: %v", err)
	}
	if _, err := caller("a").Call(ctx, requirement, statsInput); contract.ErrorCode(err) != contract.NotReady {
		t.Fatalf("dynamic child isolation did not cross native protocol: %v", err)
	}
	results, err := h.Emit(ctx, "chain.optional-ready", json.RawMessage(`{}`), contract.Sequential)
	if err != nil || len(results) != 2 || !results[0].Handled || !results[1].Handled {
		t.Fatalf("service isolation changed event broadcast: %+v %v", results, err)
	}
	b, err := h.Instance("b-consumer")
	if err != nil {
		t.Fatal(err)
	}
	originalB := b.Status().Identity
	provider, err := h.Instance("a-provider")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Update(ctx, json.RawMessage(`{"label":"a2"}`)); err != nil {
		t.Fatal(err)
	}
	child, err := h.Instance("a.optional")
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if label(aView, "chain.b") != "a2" || b.Status().Identity != originalB || label(bView, "chain.c") != "b" {
		t.Fatal("native provider update affected the other group")
	}
	if err := h.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := cordis.Open(cordis.Options{Directory: directory, Trust: trust})
	if err != nil {
		t.Fatal(err)
	}
	h = reopened
	if _, err := h.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	child, err = h.Instance("a.optional")
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if label(caller("a.optional"), "chain.b") != "a2" || label(caller("b-consumer"), "chain.c") != "b" {
		t.Fatal("Restore lost persistent or dynamic isolation")
	}
	if _, err := aView.Call(ctx, requirement, statsInput); contract.ErrorCode(err) != contract.NotReady {
		t.Fatalf("old Host view survived replacement: %v", err)
	}
	b, err = h.Instance("b-consumer")
	if err != nil {
		t.Fatal(err)
	}
	originalB = b.Status().Identity
	aView = caller("a.optional")
	if err := h.Disable(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := aView.Call(ctx, requirement, statsInput); contract.ErrorCode(err) != contract.NotReady {
		t.Fatalf("removed native child's view remained usable: %v", err)
	}
	if b.Status().Identity != originalB || label(caller("b-consumer"), "chain.c") != "b" {
		t.Fatal("stopping one native group affected the other")
	}
}
