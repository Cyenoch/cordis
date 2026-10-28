package access_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Cyenoch/cordis/access"
	"github.com/Cyenoch/cordis/contract"
)

func requireAccessCode(t *testing.T, err error, code contract.Code) {
	t.Helper()
	var coded *contract.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func TestConstructorsRequirePolicy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		construct func()
	}{
		{name: "broker", construct: func() { access.NewBroker(nil) }},
		{name: "contributions", construct: func() { access.NewContributionStore(nil) }},
		{name: "tasks", construct: func() { access.NewTaskManager(nil) }},
		{name: "namespace", construct: func() { access.NewNamespaceStore(nil) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("constructor accepted nil policy")
				}
			}()
			test.construct()
		})
	}
}

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

func TestPolicyRetirementCancelsAdmittedCalls(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"revoke", "replace"} {
		t.Run(action, func(t *testing.T) {
			policy := access.NewPolicy()
			owner := identity("first")
			activate(t, policy, owner)
			if _, err := policy.Grant(owner, "network", nil); err != nil {
				t.Fatal(err)
			}
			broker := access.NewBroker(policy)
			entered := make(chan context.Context, 1)
			release := make(chan struct{})
			releaseCall := sync.OnceFunc(func() { close(release) })
			defer releaseCall()
			if err := broker.Register("network", allowScope, func(ctx context.Context, _ contract.Identity, _ json.RawMessage) (json.RawMessage, error) {
				entered <- ctx
				<-release
				return json.RawMessage(`true`), nil
			}); err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() {
				_, err := broker.Call(t.Context(), owner, "network", json.RawMessage(`{}`))
				finished <- err
			}()
			callCtx := <-entered
			if action == "revoke" {
				if err := policy.Revoke(owner); err != nil {
					t.Fatal(err)
				}
			} else {
				replacement := identity("replacement")
				activate(t, policy, replacement)
				if err := policy.Check(replacement); err != nil {
					t.Fatalf("replacement is not active: %v", err)
				}
			}
			if !errors.Is(callCtx.Err(), context.Canceled) {
				t.Fatalf("retired call not canceled: %v", callCtx.Err())
			}
			status := policy.Calls(owner)
			if len(status) != 1 || !status[0].Canceled {
				t.Fatalf("retired calls = %#v", status)
			}
			_, err := broker.Call(t.Context(), owner, "network", json.RawMessage(`{}`))
			requireAccessCode(t, err, contract.Denied)
			releaseCall()
			requireAccessCode(t, <-finished, contract.Denied)
			if status := policy.Calls(owner); len(status) != 0 {
				t.Fatalf("returned call retained lease: %#v", status)
			}
		})
	}
}

func TestPolicyGrantRevocationCancelsOnlyMatchingCalls(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"handle", "capability"} {
		t.Run(mode, func(t *testing.T) {
			policy := access.NewPolicy()
			owner := identity("first")
			other := identity("other")
			other.Entry = "other-entry"
			var targetGrant *access.Grant
			for _, current := range []contract.Identity{owner, other} {
				if err := policy.Activate(current, []contract.Capability{{ID: "one"}, {ID: "two"}}); err != nil {
					t.Fatal(err)
				}
				for _, capability := range []string{"one", "two"} {
					grant, err := policy.Grant(current, capability, nil)
					if err != nil {
						t.Fatal(err)
					}
					if current == owner && capability == "one" {
						targetGrant = grant
					}
				}
			}
			type call struct {
				owner      contract.Identity
				capability string
			}
			type admitted struct {
				call
				ctx context.Context
			}
			type outcome struct {
				call
				err error
			}
			entered := make(chan admitted, 3)
			finished := make(chan outcome, 3)
			release := make(chan struct{})
			releaseCalls := sync.OnceFunc(func() { close(release) })
			defer releaseCalls()
			var handled atomic.Int32
			broker := access.NewBroker(policy)
			for _, capability := range []string{"one", "two"} {
				if err := broker.Register(capability, allowScope, func(
					ctx context.Context, owner contract.Identity, input json.RawMessage,
				) (json.RawMessage, error) {
					handled.Add(1)
					if string(input) != "null" {
						entered <- admitted{call: call{owner: owner, capability: capability}, ctx: ctx}
						<-release
					}
					return json.RawMessage(`true`), nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			target := call{owner: owner, capability: "one"}
			for _, request := range []call{target, {owner: owner, capability: "two"}, {owner: other, capability: "one"}} {
				go func() {
					_, err := broker.Call(t.Context(), request.owner, request.capability, json.RawMessage(`{}`))
					finished <- outcome{call: request, err: err}
				}()
			}
			contexts := make(map[call]context.Context)
			for range 3 {
				select {
				case admission := <-entered:
					contexts[admission.call] = admission.ctx
				case <-t.Context().Done():
					t.Fatal("calls did not enter handlers")
				}
			}
			for range 2 {
				var err error
				if mode == "handle" {
					err = targetGrant.Revoke()
				} else {
					err = policy.RevokeCapability(owner, "one")
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			for request, ctx := range contexts {
				if request == target {
					if !errors.Is(ctx.Err(), context.Canceled) {
						t.Fatalf("revoked call not canceled: %v", ctx.Err())
					}
				} else if err := ctx.Err(); err != nil {
					t.Fatalf("unrelated call canceled: %#v, %v", request, err)
				}
			}
			status := policy.Calls(owner)
			if len(status) != 2 {
				t.Fatalf("calls = %#v", status)
			}
			for _, current := range status {
				if current.Canceled != (current.Capability == "one") {
					t.Fatalf("incorrect cancellation status: %#v", current)
				}
			}
			_, err := broker.Call(t.Context(), owner, "one", json.RawMessage(`null`))
			requireAccessCode(t, err, contract.Denied)
			if handled.Load() != 3 {
				t.Fatal("new call entered revoked handler")
			}
			releaseCalls()
			for range 3 {
				result := <-finished
				if result.call == target {
					requireAccessCode(t, result.err, contract.Canceled)
				} else if result.err != nil {
					t.Fatalf("unrelated result rejected: %#v, %v", result.call, result.err)
				}
			}
		})
	}
}

func TestPolicyGrantRevocationDuringScopeCheckClosesAdmission(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"handle", "capability"} {
		t.Run(mode, func(t *testing.T) {
			policy := access.NewPolicy()
			owner := identity("first")
			activate(t, policy, owner)
			grant, err := policy.Grant(owner, "network", nil)
			if err != nil {
				t.Fatal(err)
			}
			checking, release := make(chan struct{}), make(chan struct{})
			releaseScope := sync.OnceFunc(func() { close(release) })
			defer releaseScope()
			var handled atomic.Bool
			broker := access.NewBroker(policy)
			if err := broker.Register("network", func(
				context.Context, contract.Identity, json.RawMessage, json.RawMessage, json.RawMessage,
			) error {
				close(checking)
				<-release
				return nil
			}, func(context.Context, contract.Identity, json.RawMessage) (json.RawMessage, error) {
				handled.Store(true)
				return json.RawMessage(`true`), nil
			}); err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() {
				_, err := broker.Call(t.Context(), owner, "network", json.RawMessage(`{}`))
				finished <- err
			}()
			<-checking
			if mode == "handle" {
				err = grant.Revoke()
			} else {
				err = policy.RevokeCapability(owner, "network")
			}
			if err != nil {
				t.Fatal(err)
			}
			releaseScope()
			requireAccessCode(t, <-finished, contract.Denied)
			if handled.Load() {
				t.Fatal("handler entered after revocation during scope check")
			}
		})
	}
}

func TestPolicyRevokeCapabilityTracksCurrentGrantAndActivation(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("first")
	activate(t, policy, owner)
	for _, capability := range []string{"network", "undeclared"} {
		requireAccessCode(t, policy.RevokeCapability(owner, capability), contract.NotReady)
	}
	old, err := policy.Grant(owner, "network", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Revoke(); err != nil {
		t.Fatal(err)
	}
	if err := policy.RevokeCapability(owner, "network"); err != nil {
		t.Fatalf("handle revocation not idempotent through lookup: %v", err)
	}
	if _, err := policy.Grant(owner, "network", nil); err != nil {
		t.Fatal(err)
	}
	if err := old.Revoke(); err != nil {
		t.Fatal(err)
	}
	broker := access.NewBroker(policy)
	if err := broker.Register("network", allowScope, func(
		context.Context, contract.Identity, json.RawMessage,
	) (json.RawMessage, error) {
		return json.RawMessage(`true`), nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Call(t.Context(), owner, "network", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("old handle revoked replacement grant: %v", err)
	}
	if err := policy.RevokeCapability(owner, "network"); err != nil {
		t.Fatal(err)
	}
	_, err = broker.Call(t.Context(), owner, "network", json.RawMessage(`{}`))
	requireAccessCode(t, err, contract.Denied)
	replacement := identity("replacement")
	activate(t, policy, replacement)
	if _, err := policy.Grant(replacement, "network", nil); err != nil {
		t.Fatal(err)
	}
	requireAccessCode(t, policy.RevokeCapability(owner, "network"), contract.Denied)
	if _, err := broker.Call(t.Context(), replacement, "network", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("old activation revoked replacement: %v", err)
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
