package cordis

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

func dependencyHost(t *testing.T) (*Host, contract.PackageRef, contract.PackageRef, contract.PackageRef) {
	return dependencyHostRuntime(t, false)
}

func TestUninstallCascadeRetiresActiveReverseDependencies(t *testing.T) {
	h, target, _, dependent := dependencyHost(t)
	for _, entry := range []contract.Entry{{ID: "target", Package: target.ID}, {ID: "dependent", Package: dependent.ID}} {
		i, err := h.Enable(t.Context(), entry)
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Uninstall(t.Context(), target, false); err == nil {
		t.Fatal("uninstall ignored active reverse dependency")
	}
	if _, err := h.UninstallCascade(t.Context(), target, false, func(_ context.Context, plan CascadePlan) error {
		if len(plan.Entries) != 2 {
			t.Fatalf("cascade omitted active reverse dependency: %+v", plan)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(h.Instances()) != 0 {
		t.Fatal("cascade retained active registrations", h.Instances())
	}
	entries, err := h.Store().Entries()
	if err != nil || len(entries) != 2 {
		t.Fatal(entries, err)
	}
	for _, entry := range entries {
		if entry.Enabled {
			t.Fatal("cascade did not persist disable intent", entry)
		}
	}
	if _, err := h.Store().Selected(target.ID); err == nil {
		t.Fatal("uninstalled target remained selected")
	}
	if _, err := h.Store().Get(dependent); err != nil {
		t.Fatal("cascade uninstalled dependent code", err)
	}
}

func TestCascadeParentSkipsDisabledUninstalledChild(t *testing.T) {
	h, parentPackage, _, childPackage := dependencyHost(t)
	parent, err := h.Enable(t.Context(), contract.Entry{ID: "target", Package: parentPackage.ID, Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	child, err := h.Enable(t.Context(), contract.Entry{ID: "child", Parent: "target", Package: childPackage.ID, Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := h.Disable(t.Context(), "child"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Uninstall(t.Context(), childPackage, false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.UninstallCascade(t.Context(), parentPackage, false, func(context.Context, CascadePlan) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func dependencyHostRuntime(t *testing.T, nativeV2 bool) (*Host, contract.PackageRef, contract.PackageRef, contract.PackageRef) {
	t.Helper()
	trust := packagehost.TrustPolicy{DevDigests: map[string]bool{}, Publishers: map[string][]string{"publisher": {"a", "b"}}}
	type fixture struct {
		m    contract.Manifest
		path string
		ref  contract.PackageRef
	}
	var fixtures []fixture
	for _, pair := range [][2]string{{"a", "1.0.0"}, {"a", "2.0.0"}, {"b", "1.0.0"}} {
		m := contract.Manifest{Schema: contract.PackageSchema, ID: pair[0], Version: pair[1], Publisher: "publisher", Protocol: contract.Protocol{Major: 1}, Definitions: []contract.DefinitionSpec{{ID: "main", ConfigSchema: json.RawMessage(`{"type":"object","properties":{}}`)}}, DefaultDefinition: "main", Runtime: contract.RuntimeSpec{Kind: "wasm", Entry: "plugin.wasm"}}
		if nativeV2 && pair[0] == "a" && pair[1] == "2.0.0" {
			m.Runtime = contract.RuntimeSpec{Kind: "native", Artifacts: []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: "plugin.wasm"}}}
		}
		if pair[0] == "b" {
			m.Dependencies = []contract.Dependency{{ID: "a", Range: "^1.0.0"}}
		}
		source := t.TempDir()
		raw, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(source, "plugin.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "plugin.wasm"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "plugin.zip")
		p, err := packagehost.Pack(t.Context(), source, path)
		if err != nil {
			t.Fatal(err)
		}
		trust.DevDigests[p.Ref.Digest] = true
		fixtures = append(fixtures, fixture{m, path, p.Ref})
	}
	h, err := Open(Options{FrameBytes: contract.MaxFrameBytes, Directory: t.TempDir(), Trust: trust, Runtimes: map[string]contract.Runtime{"wasm": fixtureRuntime{}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	for _, f := range fixtures {
		if _, err := h.Install(t.Context(), f.path); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []fixture{fixtures[0], fixtures[2]} {
		if err := h.Select(t.Context(), f.ref); err != nil {
			t.Fatal(err)
		}
	}
	return h, fixtures[0].ref, fixtures[1].ref, fixtures[2].ref
}

func TestCascadeChecksRuntimeBeforeRetiringDependents(t *testing.T) {
	h, a1, a2, b := dependencyHostRuntime(t, true)
	target, err := h.Enable(t.Context(), contract.Entry{ID: "target", Package: a1.ID, Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	i, err := h.Enable(t.Context(), contract.Entry{ID: "dependent", Package: b.ID, Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	err = h.ReplaceCascade(t.Context(), a2, packagehost.ChangeOptions{}, func(context.Context, CascadePlan) error { return nil })
	selected, _ := h.Store().Selected(a1.ID)
	t.Logf("replace err=%v selected=%s target=%s dependent=%s", err, selected.Ref.Version, target.Status().State, i.Status().State)
	if err == nil || i.Status().State != contract.Active || target.Status().State != contract.Active || selected.Ref != a1 {
		t.Fatal("invalid target runtime retired instances or changed selection")
	}
}

func TestCascadeRetiresTargetOwnedByBlockedDependent(t *testing.T) {
	h, a1, a2, b := dependencyHost(t)
	i, err := h.Enable(t.Context(), contract.Entry{ID: "dependent", Package: b.ID, Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	target, err := h.Enable(t.Context(), contract.Entry{ID: "target", Package: a1.ID, Parent: "dependent", Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	plan, err := h.PlanCascade(t.Context(), a2, packagehost.SelectPackage)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("plan=%+v", plan)
	err = h.ReplaceCascade(t.Context(), a2, packagehost.ChangeOptions{}, func(context.Context, CascadePlan) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if target.Status().State != contract.Stopped {
		t.Fatal("target below retired parent remained active")
	}
	if _, err = h.Status("target"); err == nil {
		t.Fatal("retired target was recreated without its parent")
	}
	entries, err := h.Store().Entries()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Enabled {
			t.Fatal("retired ownership subtree remained enabled", entry)
		}
	}
}

func TestCascadeDurableEnabledUnrestoredPreflight(t *testing.T) {
	h, a1, a2, b := dependencyHost(t)
	if err := h.Store().SaveEntry(t.Context(), contract.Entry{ID: "unrestored", Package: b.ID, Definition: "main", Enabled: true, Config: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := h.Select(t.Context(), a2); err == nil {
		t.Fatal("select ignored durable enabled")
	}
	if err := h.Replace(t.Context(), a2, packagehost.ChangeOptions{}); err == nil {
		t.Fatal("replace ignored durable enabled")
	}
	if _, err := h.Uninstall(t.Context(), a1, false); err == nil {
		t.Fatal("uninstall ignored durable enabled")
	}
	if err := h.ReplaceCascade(t.Context(), a2, packagehost.ChangeOptions{}, func(_ context.Context, p CascadePlan) error {
		if len(p.Entries) != 1 || p.Entries[0].ID != "unrestored" {
			t.Fatalf("wrong unrestored plan: %+v", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCascadeDisableBuiltinParentIncludesUnrestoredEnabledChildren(t *testing.T) {
	h, a1, _, _ := dependencyHost(t)
	parent, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "builtin-parent"}, contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := h.Store().RegisterParent(t.Context(), "builtin-parent"); err != nil {
		t.Fatal(err)
	}
	entry := contract.Entry{ID: "unrestored-child", Package: a1.ID, Parent: "builtin-parent", Definition: "main", Enabled: true, Config: json.RawMessage(`{}`)}
	if err := h.Store().SaveEntry(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	if err := h.Disable(t.Context(), "builtin-parent"); err != nil {
		t.Fatal(err)
	}
	entries, err := h.Store().Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Enabled {
		t.Fatalf("disabled parent left enabled durable orphan: %+v", entries)
	}
}
