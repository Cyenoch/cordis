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

func TestBrokerRevocationCancelsAdmittedAndRejectsNewCalls(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("first")
	activate(t, policy, owner)
	grant, err := policy.Grant(owner, "network", nil)
	if err != nil {
		t.Fatal(err)
	}
	broker := access.NewBroker(policy)
	entered := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	if err := broker.Register("network", allowScope, func(ctx context.Context, _ contract.Identity, _ json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return json.RawMessage(`{"late":true}`), nil
	}); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { _, err := broker.Call(t.Context(), owner, "network", json.RawMessage(`{}`)); finished <- err }()
	select {
	case <-entered:
	case <-t.Context().Done():
		t.Fatal("handler did not enter")
	}
	if err := grant.Revoke(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-t.Context().Done():
		t.Fatal("handler did not receive cancellation")
	}
	if _, err := broker.Call(t.Context(), owner, "network", json.RawMessage(`{}`)); err == nil {
		t.Fatal("new call admitted")
	}
	status, err := policy.Calls(owner)
	if err != nil || len(status) != 1 || !status[0].Canceled {
		t.Fatalf("calls = %#v, %v", status, err)
	}
	close(release)
	if err := <-finished; err == nil {
		t.Fatal("late handler result published")
	}
	if calls.Load() != 1 {
		t.Fatalf("handler called %d times", calls.Load())
	}
}

func TestBrokerRevocationDuringScopeCheckClosesAdmission(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("first")
	activate(t, policy, owner)
	grant, err := policy.Grant(owner, "network", nil)
	if err != nil {
		t.Fatal(err)
	}
	broker := access.NewBroker(policy)
	checking, release := make(chan struct{}), make(chan struct{})
	var handled atomic.Bool
	if err := broker.Register("network", func(context.Context, contract.Identity, json.RawMessage, json.RawMessage, json.RawMessage) error {
		close(checking)
		<-release
		return nil
	}, func(context.Context, contract.Identity, json.RawMessage) (json.RawMessage, error) {
		handled.Store(true)
		return json.RawMessage(`null`), nil
	}); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { _, err := broker.Call(t.Context(), owner, "network", json.RawMessage(`{}`)); finished <- err }()
	<-checking
	if err := grant.Revoke(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-finished; err == nil {
		t.Fatal("revoked grant admitted")
	}
	if handled.Load() {
		t.Fatal("handler ran after revocation")
	}
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
