package access_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Cyenoch/cordis/access"
	"github.com/Cyenoch/cordis/contract"
)

func identity(activation string) contract.Identity {
	return contract.Identity{Package: "example.plugin", Entry: "entry", Activation: activation, Digest: "digest"}
}

func activate(t *testing.T, policy *access.Policy, owner contract.Identity) {
	t.Helper()
	if err := policy.Activate(owner, []contract.Capability{{ID: "network", Scope: json.RawMessage(`{"host":"example.test"}`)}}); err != nil {
		t.Fatal(err)
	}
}

func allowScope(context.Context, contract.Identity, json.RawMessage, json.RawMessage, json.RawMessage) error {
	return nil
}

func TestBrokerDefaultsDenyAndPreservesHostFailure(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("first")
	activate(t, policy, owner)
	broker := access.NewBroker(policy)
	failure := errors.New("host credential must not cross wire")
	var handled atomic.Bool
	if err := broker.Register("network", allowScope, func(context.Context, contract.Identity, json.RawMessage) (json.RawMessage, error) {
		handled.Store(true)
		return nil, failure
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Call(t.Context(), owner, "network", json.RawMessage(`{}`)); err == nil || handled.Load() {
		t.Fatal("ungranted handler ran")
	}
	if _, err := policy.Grant(owner, "undeclared", nil); err == nil {
		t.Fatal("undeclared grant accepted")
	}
	if _, err := policy.Grant(owner, "network", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Call(t.Context(), owner, "network", json.RawMessage(`{}`)); !errors.Is(err, failure) {
		t.Fatalf("host failure lost: %v", err)
	}
}
