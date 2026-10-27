package access

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
)

type namespaceInvoker func(context.Context, string, json.RawMessage) (json.RawMessage, error)

func (f namespaceInvoker) InvokeHost(ctx context.Context, capability string, input json.RawMessage) (json.RawMessage, error) {
	return f(ctx, capability, input)
}

func typedNamespaceFixture(t *testing.T) (*Broker, *NamespaceStore, contract.Identity, *Grant) {
	t.Helper()
	p := NewPolicy()
	owner := contract.Identity{Package: "example.state", Entry: "one", Activation: "current"}
	if err := p.Activate(owner, []contract.Capability{{ID: "state", Scope: json.RawMessage(`{"keys":"own"}`)}}); err != nil {
		t.Fatal(err)
	}
	grant, err := p.Grant(owner, "state", json.RawMessage(`{"keys":"own"}`))
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenNamespaceStore(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Configure(owner.Package, NamespaceConfig{Name: "host-selected", QuotaBytes: 32}); err != nil {
		t.Fatal(err)
	}
	broker := NewBroker(p)
	if err := RegisterNamespace(broker, store, "state", func(
		_ context.Context, got contract.Identity, declared, granted, input json.RawMessage,
	) error {
		if got != owner || !bytes.Equal(declared, granted) {
			return errors.New("unexpected identity or scope")
		}
		var request contract.NamespaceRequest
		if err := contract.DecodeData(input, &request); err != nil {
			return err
		}
		if request.Key == "forbidden" {
			return errors.New("key is outside host scope")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return broker, store, owner, grant
}

// This runs the real SDK over a real endpoint; only the host business handler is
// supplied by the test. The hook controls the gap after storage, before reply.
func namespaceSDK(t *testing.T, owner contract.Identity, handler wire.Handler) (*sdk.NamespaceClient, *wire.Endpoint) {
	t.Helper()
	left, right := net.Pipe()
	served := make(chan error, 1)
	invokers := make(chan contract.HostInvoker, 1)
	go func() {
		served <- sdk.ServeTransport(t.Context(), contract.Definition{
			ID: "state",
			Start: func(_ context.Context, scope contract.Scope) error {
				invoker, ok := scope.(contract.HostInvoker)
				if !ok {
					return contract.Fail(contract.Incompatible, "SDK lacks host invocation")
				}
				invokers <- invoker
				return nil
			},
		}, right)
	}()
	host, err := wire.New(wire.Options{
		Transport: left, Identity: owner, Handler: handler, FrameBytes: contract.MaxFrameBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Close(); err != nil {
			t.Error(err)
		}
		host.Wait()
		if err := <-served; err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})
	input, err := json.Marshal(contract.Initialize{
		Identity: owner, Protocol: contract.Protocol{Major: 1}, Definition: "state",
		Config: json.RawMessage(`{}`), FrameBytes: contract.MaxFrameBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Call(t.Context(), "initialize", input); err != nil {
		t.Fatal(err)
	}
	return sdk.NewNamespaceClient(<-invokers, "state"), host
}

func namespaceHost(broker *Broker, owner contract.Identity) wire.Handler {
	return func(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
		if method != "host.invoke" {
			return nil, contract.Fail(contract.Invalid, "unexpected method")
		}
		var call contract.HostCall
		if err := contract.Decode(input, &call); err != nil {
			return nil, err
		}
		result, err := broker.Call(ctx, owner, call.Capability, call.Input)
		return result, (*Sink)(nil).PublicError(err)
	}
}

func TestNamespaceSDKBytesOutcomesAndHostMaintenance(t *testing.T) {
	broker, store, owner, _ := typedNamespaceFixture(t)
	client, _ := namespaceSDK(t, owner, namespaceHost(broker, owner))
	value := []byte{0, 0xff, 0x80, '\n', 1}
	if err := client.Write(t.Context(), "k", value); err != nil {
		t.Fatal(err)
	}
	got, err := client.Read(t.Context(), "k")
	if err != nil || !bytes.Equal(got, value) {
		t.Fatalf("bytes = %x, %v", got, err)
	}
	if err := client.Write(t.Context(), "k", make([]byte, 32)); !errors.Is(err, ErrQuota) {
		t.Fatalf("quota = %v", err)
	}
	got, err = client.Read(t.Context(), "k")
	if err != nil || !bytes.Equal(got, value) {
		t.Fatalf("failed write changed value: %x, %v", got, err)
	}
	if err := client.Delete(t.Context(), "k"); err != nil {
		t.Fatal(err)
	}
	result, err := client.Do(t.Context(), contract.NamespaceRequest{Operation: contract.NamespaceRead, Key: "k"})
	if result.Outcome != contract.NamespaceMissing || !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing = %#v, %v", result, err)
	}
	if err := client.Write(t.Context(), "empty", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := client.Read(t.Context(), "empty"); err != nil || len(got) != 0 {
		t.Fatalf("empty bytes confused with missing: %x, %v", got, err)
	}
	approve := func(context.Context, string, string) error { return nil }
	if err := store.Migrate(t.Context(), "host-selected", 1, nil, nil); err == nil {
		t.Fatal("migration bypassed host approval")
	}
	if err := store.Migrate(t.Context(), "host-selected", 1, approve,
		func(_ context.Context, _ uint32, values map[string][]byte) (map[string][]byte, error) {
			values["host"] = []byte("migrated")
			return values, nil
		}); err != nil {
		t.Fatal(err)
	}
	if got, err := client.Read(t.Context(), "host"); err != nil || string(got) != "migrated" {
		t.Fatalf("host migration = %q, %v", got, err)
	}
	if err := store.DeleteNamespace(t.Context(), "host-selected", nil); err == nil {
		t.Fatal("deletion bypassed host approval")
	}
	if err := store.DeleteNamespace(t.Context(), "host-selected", approve); err != nil {
		t.Fatal(err)
	}
	result, err = client.Do(t.Context(), contract.NamespaceRequest{Operation: contract.NamespaceRead, Key: "host"})
	var denied *contract.Error
	if result.Outcome != contract.NamespaceDenied || !errors.As(err, &denied) || denied.Code != contract.Denied {
		t.Fatalf("unbound package = %#v, %v", result, err)
	}
}

func TestNamespaceBrokerDoesNotTrustGuestIdentityOrBypassScope(t *testing.T) {
	broker, store, owner, grant := typedNamespaceFixture(t)
	var calls int
	handler := broker.handlers["state"]
	handle := handler.handle
	handler.handle = func(ctx context.Context, id contract.Identity, input json.RawMessage) (json.RawMessage, error) {
		calls++
		return handle(ctx, id, input)
	}
	broker.handlers["state"] = handler
	for _, extra := range []string{`"namespace":"other"`, `"package":"other"`, `"owner":{}`} {
		input := json.RawMessage(`{"operation":"write","key":"k","value":"ZXZpbA==",` + extra + `}`)
		if _, err := broker.Call(t.Context(), owner, "state", input); err == nil {
			t.Fatalf("accepted guest identity: %s", input)
		}
	}
	request := json.RawMessage(`{"operation":"write","key":"forbidden","value":"ZXZpbA=="}`)
	if _, err := broker.Call(t.Context(), owner, "state", request); err == nil {
		t.Fatal("scope rejection ignored")
	}
	if err := grant.Revoke(); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Call(t.Context(), owner, "state", json.RawMessage(`{"operation":"write","key":"k"}`)); err == nil {
		t.Fatal("revoked grant bypassed")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.spaces["host-selected"].Values) != 0 {
		t.Fatal("denied request accessed storage")
	}
	if calls != 0 {
		t.Fatalf("denied requests entered storage handler %d times", calls)
	}
}

func TestNamespaceBrokerOnlyMapsTrustedPackage(t *testing.T) {
	broker, store, owner, _ := typedNamespaceFixture(t)
	other := contract.Identity{Package: "other.package", Entry: "one", Activation: "other"}
	if err := broker.policy.Activate(other, []contract.Capability{{ID: "state"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.policy.Grant(other, "state", nil); err != nil {
		t.Fatal(err)
	}
	otherBroker := NewBroker(broker.policy)
	if err := RegisterNamespace(otherBroker, store, "state", func(
		context.Context, contract.Identity, json.RawMessage, json.RawMessage, json.RawMessage,
	) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Give the second package the same capability and host scope semantics;
	// neither an existing namespace name nor another owner's package is input.
	call := func(input string) contract.NamespaceResult {
		t.Helper()
		data, err := otherBroker.Call(t.Context(), other, "state", json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		var result contract.NamespaceResult
		if err := contract.DecodeData(data, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := call(`{"operation":"read","key":"k"}`); result.Outcome != contract.NamespaceDenied {
		t.Fatalf("unbound package accessed namespace: %#v", result)
	}
	if err := store.Configure(other.Package, NamespaceConfig{Name: "other-host-name", QuotaBytes: 32}); err != nil {
		t.Fatal(err)
	}
	if result := call(`{"operation":"write","key":"k","value":"b3RoZXI="}`); result.Outcome != contract.NamespaceOK {
		t.Fatalf("own mapping failed: %#v", result)
	}
	if _, err := store.Read(t.Context(), owner, "host-selected", "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other package mutated first package: %v", err)
	}
	if got, err := store.Read(t.Context(), other, "other-host-name", "k"); err != nil || string(got) != "other" {
		t.Fatalf("wrong mapped namespace: %q, %v", got, err)
	}
}

func TestNamespaceSDKCommitReplyLost(t *testing.T) {
	for _, action := range []string{"cancel", "revoke", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			broker, store, owner, grant := typedNamespaceFixture(t)
			committed, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			handler := broker.handlers["state"]
			handle := handler.handle
			handler.handle = func(ctx context.Context, id contract.Identity, input json.RawMessage) (json.RawMessage, error) {
				calls.Add(1)
				result, err := handle(ctx, id, input)
				close(committed)
				<-release
				return result, err
			}
			broker.handlers["state"] = handler
			client, host := namespaceSDK(t, owner, namespaceHost(broker, owner))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type reply struct {
				result contract.NamespaceResult
				err    error
			}
			done := make(chan reply, 1)
			go func() {
				result, err := client.Do(ctx, contract.NamespaceRequest{Operation: contract.NamespaceWrite, Key: "k", Value: []byte("once")})
				done <- reply{result: result, err: err}
			}()
			<-committed
			if got, err := store.Read(t.Context(), owner, "host-selected", "k"); err != nil || string(got) != "once" {
				t.Fatalf("write not committed at barrier: %q, %v", got, err)
			}
			wantCause := context.Canceled
			switch action {
			case "cancel":
				cancel()
			case "revoke":
				if err := grant.Revoke(); err != nil {
					t.Fatal(err)
				}
			case "disconnect":
				wantCause = io.EOF
				if err := host.Close(); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			got := <-done
			var unknown *contract.SubmissionUnknownError
			if got.result.Outcome != "" || !errors.As(got.err, &unknown) || !errors.Is(got.err, wantCause) {
				t.Fatalf("lost reply = %#v, %v; want cause %v", got.result, got.err, wantCause)
			}
			if calls.Load() != 1 {
				t.Fatalf("automatically retried %d times", calls.Load())
			}
			if action != "disconnect" {
				if _, err := host.Call(t.Context(), "health", json.RawMessage(`{}`)); err != nil {
					t.Fatalf("late result broke session: %v", err)
				}
			}
		})
	}
}

func TestNamespaceSDKValidFailureOutcomesDoNotLeakLocalCause(t *testing.T) {
	for _, outcome := range []contract.NamespaceOutcome{contract.NamespaceNotCommitted, contract.NamespaceCommitted, contract.NamespaceRecoveryRequired} {
		t.Run(string(outcome), func(t *testing.T) {
			if outcome == contract.NamespaceCommitted && runtime.GOOS == "windows" {
				t.Skip("Windows directory synchronization has no separate failure boundary")
			}
			broker, store, owner, _ := typedNamespaceFixture(t)
			secret := errors.New("private /host/credential/log")
			var local error
			directory := filepath.Dir(store.path)
			if outcome == contract.NamespaceCommitted {
				secret = os.ErrNotExist
				// The log is already open at this boundary. Keep the real write
				// and fsync, but make the subsequent directory sync fail.
				store.commitObserver = func() { store.path = filepath.Join(directory, "missing", "log") }
				local = store.Write(t.Context(), owner, "host-selected", "probe", []byte("value"))
				store.path = filepath.Join(directory, "namespaces.log")
			} else {
				if outcome == contract.NamespaceRecoveryRequired {
					store.failure = secret
					_, local = store.Read(t.Context(), owner, "host-selected", "k")
				} else {
					// Opening a directory as the log fails before any write.
					store.path = filepath.Dir(store.path)
					local = store.Write(t.Context(), owner, "host-selected", "k", []byte("value"))
				}
			}
			client, _ := namespaceSDK(t, owner, namespaceHost(broker, owner))
			result, err := client.Do(t.Context(), contract.NamespaceRequest{Operation: contract.NamespaceWrite, Key: "k", Value: []byte("value")})
			if result.Outcome != outcome || err == nil {
				t.Fatalf("outcome = %#v, %v", result, err)
			}
			if outcome == contract.NamespaceCommitted {
				reopened, err := OpenNamespaceStore(broker.policy, directory)
				if err != nil {
					t.Fatal(err)
				}
				if got, err := reopened.Read(t.Context(), owner, "host-selected", "k"); err != nil || string(got) != "value" {
					t.Fatalf("committed result lost data: %q, %v", got, err)
				}
			}
			if bytes.Contains([]byte(err.Error()), []byte("/host/")) || errors.Is(err, secret) {
				t.Fatalf("wire leaked local cause: %v", err)
			}
			switch outcome {
			case contract.NamespaceNotCommitted:
				var typed *contract.NotCommittedError
				if !errors.As(err, &typed) {
					t.Fatalf("not-committed type lost: %v", err)
				}
			case contract.NamespaceCommitted:
				var typed *CommittedError
				if !errors.As(err, &typed) || !errors.As(local, &typed) || !errors.Is(local, secret) {
					t.Fatalf("committed type or local cause lost: %v, %v", err, local)
				}
			case contract.NamespaceRecoveryRequired:
				var typed *RecoveryError
				if !errors.As(err, &typed) || !errors.As(local, &typed) || !errors.Is(local, secret) {
					t.Fatalf("recovery type or local cause lost: %v, %v", err, local)
				}
			}
		})
	}
}

func TestNamespaceClientSubmissionEvidence(t *testing.T) {
	request := contract.NamespaceRequest{Operation: contract.NamespaceWrite, Key: "k", Value: []byte("v")}
	for _, scenario := range []string{
		"pre-canceled", "invalid-key", "invoke-error", "malformed", "invalid-outcome", "late-valid",
	} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var calls int
			cause := errors.New("transport disconnected")
			client := sdk.NewNamespaceClient(namespaceInvoker(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				calls++
				switch scenario {
				case "invoke-error":
					return nil, fmt.Errorf("port: %w", cause)
				case "malformed":
					return json.RawMessage(`{"outcome":"committed"`), nil
				case "invalid-outcome":
					return json.RawMessage(`{"outcome":"submission-unknown"}`), nil
				case "late-valid":
					cancel()
					return json.RawMessage(`{"outcome":"committed"}`), nil
				default:
					t.Fatal("locally rejected request invoked host")
					return nil, nil
				}
			}), "state")
			input := request
			if scenario == "pre-canceled" {
				cancel()
			}
			if scenario == "invalid-key" {
				input.Key = string([]byte{0xff})
			}
			result, err := client.Do(ctx, input)
			if scenario == "pre-canceled" || scenario == "invalid-key" {
				var notCommitted *contract.NotCommittedError
				if result.Outcome != contract.NamespaceNotCommitted || !errors.As(err, &notCommitted) || calls != 0 {
					t.Fatalf("unsent = %#v, %v, %d calls", result, err, calls)
				}
			} else {
				var unknown *contract.SubmissionUnknownError
				if result.Outcome != "" || !errors.As(err, &unknown) || calls != 1 {
					t.Fatalf("unknown = %#v, %v, %d calls", result, err, calls)
				}
			}
			if scenario == "pre-canceled" || scenario == "late-valid" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation cause lost: %v", err)
				}
			}
			if scenario == "invoke-error" && !errors.Is(err, cause) {
				t.Fatalf("transport cause lost: %v", err)
			}
		})
	}
}

func TestNamespaceRecoveryBlocksAllCRUDWithTypedCause(t *testing.T) {
	_, store, owner, _ := typedNamespaceFixture(t)
	cause := os.ErrPermission
	store.failure = cause
	_, readErr := store.Read(t.Context(), owner, "host-selected", "k")
	for _, err := range []error{
		readErr,
		store.Write(t.Context(), owner, "host-selected", "k", []byte("v")),
		store.Delete(t.Context(), owner, "host-selected", "k"),
	} {
		var recovery *RecoveryError
		if !errors.As(err, &recovery) || !errors.Is(err, cause) {
			t.Fatalf("poisoned store lost typed recovery or cause: %v", err)
		}
	}
}

func TestTypedBrokerStrictCodecAndLocalErrors(t *testing.T) {
	configured, _, owner, _ := typedNamespaceFixture(t)
	broker := NewBroker(configured.policy)
	type payload struct {
		Number json.Number `json:"number"`
	}
	var calls int
	var fail error
	var invalidOutput bool
	err := Register(broker, "state", func(
		context.Context, contract.Identity, json.RawMessage, json.RawMessage, json.RawMessage,
	) error {
		return nil
	}, func(_ context.Context, id contract.Identity, input payload) (payload, error) {
		calls++
		if id != owner {
			t.Fatal("typed broker lost trusted identity")
		}
		if invalidOutput {
			input.Number = "NaN"
		}
		return input, fail
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"number":"1"}`, `{"number":9007199254740992}`, `{"number":1,"unexpected":true}`,
	} {
		if _, err := broker.Call(t.Context(), owner, "state", json.RawMessage(raw)); err == nil {
			t.Fatalf("typed codec accepted %s", raw)
		}
	}
	if calls != 0 {
		t.Fatal("invalid payload entered business handler")
	}
	const number = "0.12345678901234567890123456789"
	input := json.RawMessage(`{"number":` + number + `}`)
	data, err := broker.Call(t.Context(), owner, "state", input)
	if err != nil || !bytes.Equal(data, input) {
		t.Fatalf("exact number = %s, %v", data, err)
	}
	bound := namespaceInvoker(func(ctx context.Context, capability string, input json.RawMessage) (json.RawMessage, error) {
		return broker.Call(ctx, owner, capability, input)
	})
	output, err := contract.InvokeHost[payload, payload](t.Context(), bound, "state", payload{Number: number})
	if err != nil || output.Number != number {
		t.Fatalf("shared typed host helper = %#v, %v", output, err)
	}
	invalidOutput = true
	if data, err := broker.Call(t.Context(), owner, "state", input); err == nil || len(data) != 0 {
		t.Fatalf("invalid business output escaped codec: %s, %v", data, err)
	}
	invalidOutput = false
	cause := errors.New("host-only cause")
	fail = &CommittedError{Cause: cause}
	_, err = broker.Call(t.Context(), owner, "state", input)
	var committed *CommittedError
	if !errors.As(err, &committed) || !errors.Is(err, cause) {
		t.Fatalf("typed broker dropped local error chain: %v", err)
	}
	if err := Register(broker, "no-scope", nil, func(context.Context, contract.Identity, int) (int, error) { return 0, nil }); err == nil {
		t.Fatal("typed adapter bypassed scope requirement")
	}
	if err := Register(broker, "bad-type", func(
		context.Context, contract.Identity, json.RawMessage, json.RawMessage, json.RawMessage,
	) error {
		return nil
	}, func(context.Context, contract.Identity, int64) (int, error) { return 0, nil }); err == nil {
		t.Fatal("typed adapter accepted unencoded int64")
	}
}
