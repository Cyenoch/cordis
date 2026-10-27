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

func TestPolicyGrantRevocationDoesNotRevokeOtherCapabilities(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("first")
	if err := policy.Activate(owner, []contract.Capability{{ID: "one"}, {ID: "two"}}); err != nil {
		t.Fatal(err)
	}
	first, err := policy.Grant(owner, "one", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Grant(owner, "two", nil); err != nil {
		t.Fatal(err)
	}
	broker := access.NewBroker(policy)
	for _, id := range []string{"one", "two"} {
		if err := broker.Register(id, allowScope, func(context.Context, contract.Identity, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`true`), nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Revoke(); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Call(t.Context(), owner, "one", json.RawMessage(`{}`)); err == nil {
		t.Fatal("revoked capability admitted")
	}
	if _, err := broker.Call(t.Context(), owner, "two", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyScopeRejectsWithoutExecutingAndRetainsHostCause(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("first")
	activate(t, policy, owner)
	if _, err := policy.Grant(owner, "network", json.RawMessage(`{"host":"example.test"}`)); err != nil {
		t.Fatal(err)
	}
	broker := access.NewBroker(policy)
	failure := errors.New("scope denied with host-only detail")
	var handled atomic.Bool
	if err := broker.Register("network", func(_ context.Context, _ contract.Identity, declaration, granted, input json.RawMessage) error {
		if string(declaration) != `{"host":"example.test"}` || string(granted) != `{"host":"example.test"}` {
			t.Fatal("host scope metadata not supplied")
		}
		return failure
	}, func(context.Context, contract.Identity, json.RawMessage) (json.RawMessage, error) {
		handled.Store(true)
		return json.RawMessage(`null`), nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := broker.Call(t.Context(), owner, "network", json.RawMessage(`{"host":"other.test"}`))
	if !errors.Is(err, failure) {
		t.Fatalf("scope rejection lost cause: %v", err)
	}
	if handled.Load() {
		t.Fatal("scope denied handler executed")
	}
	var denial *contract.Error
	if !errors.As(err, &denial) || denial.Code != contract.Denied {
		t.Fatalf("scope denial code missing: %v", err)
	}
}
