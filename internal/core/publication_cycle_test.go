package core_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func TestPublicationRejectsChildOverrideOfParentDependency(t *testing.T) {
	e := core.New()
	defer func() { _ = e.Close(context.Background()) }()
	if err := e.ConfigureRealm("tenant", "", nil); err != nil {
		t.Fatal(err)
	}
	service := contract.Service{Name: "value", Major: 1}
	provider := contract.Definition{Provides: []contract.Service{service}, Start: func(_ context.Context, s contract.Scope) error {
		return s.Provide(service, func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`0`), nil })
	}}
	root, err := e.Add(contract.Entry{ID: "root", Enabled: true}, provider, contract.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if err = root.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	var starts atomic.Int32
	parent, err := e.Add(contract.Entry{ID: "parent", Realm: "tenant", Enabled: true}, contract.Definition{Requires: []contract.Requirement{{Name: "value", Major: 1}}, Start: func(context.Context, contract.Scope) error { starts.Add(1); return nil }}, contract.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if err = parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	child, err := e.Add(contract.Entry{ID: "child", Parent: "parent", Realm: "tenant", Enabled: true}, provider, contract.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Wait(t.Context()); err == nil {
		t.Fatal("accepted cyclic publication")
	}
	if parent.Status().State != contract.Active || starts.Load() != 1 {
		t.Fatalf("parent restarted: state=%s starts=%d", parent.Status().State, starts.Load())
	}
}
