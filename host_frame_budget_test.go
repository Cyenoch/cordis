package cordis

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

type countedBudgetRuntime struct{ calls *atomic.Int32 }

func (r countedBudgetRuntime) Start(ctx context.Context, launch contract.Launch) (contract.Session, error) {
	r.calls.Add(1)
	return fixtureRuntime{}.Start(ctx, launch)
}

func TestInvalidHostBudgetFailsBeforeRuntimeAndRunRecord(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(map[bool]string{false: "library generic", true: "generic factory"}[wrapped], func(t *testing.T) {
			h, pkg, _ := lifecycleHost(t)
			h.options.FrameBytes = 0
			var starts, selected atomic.Int32
			h.options.Runtimes["wasm"] = countedBudgetRuntime{calls: &starts}
			if wrapped {
				h.options.SessionFactory = func(contract.Manifest) *contract.SessionFactory {
					selected.Add(1)
					return &contract.SessionFactory{Protocol: contract.GenericSession, Open: func(io.ReadWriteCloser, contract.Identity, contract.SessionHandler, int) (contract.Session, error) {
						t.Error("opener called with invalid budget")
						return nil, nil
					}}
				}
			}
			instance, err := h.Enable(t.Context(), contract.Entry{ID: "invalid-budget", Package: pkg.ID, Config: json.RawMessage(`{}`)})
			if err == nil {
				err = instance.Wait(t.Context())
			}
			if err == nil || starts.Load() != 0 {
				t.Fatalf("invalid budget started runtime: %v, calls=%d", err, starts.Load())
			}
			defer disableExpectedBudgetFailure(t, h, "invalid-budget", err)
			if wrapped && selected.Load() != 1 {
				t.Fatalf("factory selected %d times", selected.Load())
			}
			runs, err := h.store.Runs()
			if err != nil || len(runs) != 0 {
				t.Fatalf("invalid budget wrote run records: %+v, %v", runs, err)
			}
		})
	}
}

func TestHostDoesNotPublishMismatchedBudget(t *testing.T) {
	h, pkg, _ := lifecycleHost(t)
	h.options.FrameBytes = contract.MinFrameBytes
	instance, err := h.Enable(t.Context(), contract.Entry{ID: "mismatched-budget", Package: pkg.ID, Config: json.RawMessage(`{}`)})
	if err == nil {
		err = instance.Wait(t.Context())
	}
	if err == nil {
		t.Fatal("unconfirmed generic budget was published")
	}
	disableExpectedBudgetFailure(t, h, "mismatched-budget", err)
}

type unexpectedCustomSession struct{ *fixtureSession }

func (*unexpectedCustomSession) Initialize(context.Context, contract.Initialize) (contract.Registration, error) {
	return contract.Registration{}, nil
}
func (*unexpectedCustomSession) Publish(context.Context) error { return nil }
func (*unexpectedCustomSession) Retire(context.Context) error  { return nil }
func (*unexpectedCustomSession) Invoke(context.Context, contract.Invocation) (json.RawMessage, error) {
	return nil, nil
}
func (*unexpectedCustomSession) Deliver(context.Context, contract.Event) ([]contract.EventResult, error) {
	return nil, nil
}
func (*unexpectedCustomSession) Shutdown(context.Context) error { return nil }

type unexpectedCustomRuntime struct{ session *unexpectedCustomSession }

func (r unexpectedCustomRuntime) Start(context.Context, contract.Launch) (contract.Session, error) {
	return r.session, nil
}

func TestHostRejectsUndeclaredCustomRuntime(t *testing.T) {
	h, pkg, _ := lifecycleHost(t)
	session := &unexpectedCustomSession{fixtureSession: &fixtureSession{done: make(chan struct{})}}
	h.options.Runtimes["wasm"] = unexpectedCustomRuntime{session: session}
	instance, err := h.Enable(t.Context(), contract.Entry{ID: "custom-mismatch", Package: pkg.ID, Config: json.RawMessage(`{}`)})
	if err == nil {
		err = instance.Wait(t.Context())
	}
	if err == nil {
		t.Fatal("generic declaration accepted custom Runtime mapping")
	}
	disableExpectedBudgetFailure(t, h, "custom-mismatch", err)
	select {
	case <-session.done:
	default:
		t.Fatal("rejected custom mapping not closed")
	}
}

func disableExpectedBudgetFailure(t *testing.T, host *Host, id string, cause error) {
	t.Helper()
	if err := host.Disable(t.Context(), id); err != nil && !errors.Is(err, cause) {
		t.Fatalf("disable failed instance: %v (expected cause %v)", err, cause)
	}
}
