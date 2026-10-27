//go:build integration

package packagehost

import (
	"context"
	"reflect"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestReverseDependenciesPlansTransitiveCascadeWithoutMutation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, trust := signingFixture(t)
	trust.Publishers["example"] = []string{"a", "b", "c", "optional", "unrelated"}
	s := openFixture(t, t.TempDir(), trust)
	install := func(id, version string, deps, optional []contract.Dependency) contract.PackageRef {
		t.Helper()
		m := manifestFixture(id, version)
		m.Dependencies, m.OptionalDependencies = deps, optional
		archive, p := archiveFixture(t, m, key)
		if _, err := s.Install(ctx, archive); err != nil {
			t.Fatal(err)
		}
		return p.Ref
	}
	a := install("a", "1.0.0", nil, nil)
	if err := s.Select(ctx, a, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	a2 := install("a", "2.0.0", nil, nil)
	b := install("b", "1.0.0", []contract.Dependency{{ID: "a", Range: "^1.0.0"}}, nil)
	c := install("c", "1.0.0", []contract.Dependency{{ID: "b", Range: "^1.0.0"}}, nil)
	o := install("optional", "1.0.0", nil, []contract.Dependency{{ID: "a", Range: "^1.0.0"}})
	u := install("unrelated", "1.0.0", nil, nil)
	for _, ref := range []contract.PackageRef{b, c, o, u} {
		if err := s.Select(ctx, ref, ChangeOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	enabled := []string{"c", "optional", "a", "b", "b", "unrelated"}
	for _, ref := range []contract.PackageRef{a, a2} {
		action := SelectPackage
		if ref == a {
			action = RemovePackage
		}
		blocked, err := s.ReverseDependencies(ref, enabled, action)
		if err != nil || !reflect.DeepEqual(blocked, []string{"b", "c"}) {
			t.Fatalf("cascade %+v: %v, %v", ref, blocked, err)
		}
	}
	if blocked, err := s.ReverseDependencies(a, enabled, SelectPackage); err != nil || len(blocked) != 0 {
		t.Fatal("same-version selection treated as removal", blocked, err)
	}
	if selected, err := s.Selected("a"); err != nil || selected.Ref != a {
		t.Fatal("plan changed selection", selected, err)
	}
	if err := s.Select(ctx, a2, ChangeOptions{Enabled: enabled}); err == nil {
		t.Fatal("planning silently authorized cascade")
	}
	if err := s.Select(ctx, a2, ChangeOptions{Enabled: []string{"optional", "unrelated"}}); err != nil {
		t.Fatal("explicitly disabled closure still blocked", err)
	}
}
