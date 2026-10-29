package cordis

import (
	"context"
	"encoding/json"
	"errors"
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
	h, pkg, _ := lifecycleHost(t)
	h.options.FrameBytes = 0
	var starts atomic.Int32
	h.options.Runtime = countedBudgetRuntime{calls: &starts}
	instance, err := h.Enable(t.Context(), contract.Entry{ID: "invalid-budget", Package: pkg.ID, Config: json.RawMessage(`{}`)})
	if err == nil {
		err = instance.Wait(t.Context())
	}
	if err == nil || starts.Load() != 0 {
		t.Fatalf("invalid budget started runtime: %v, calls=%d", err, starts.Load())
	}
	defer disableExpectedBudgetFailure(t, h, "invalid-budget", err)
	runs, err := h.store.Runs()
	if err != nil || len(runs) != 0 {
		t.Fatalf("invalid budget wrote run records: %+v, %v", runs, err)
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

func disableExpectedBudgetFailure(t *testing.T, host *Host, id string, cause error) {
	t.Helper()
	if err := host.Disable(t.Context(), id); err != nil && !errors.Is(err, cause) {
		t.Fatalf("disable failed instance: %v (expected cause %v)", err, cause)
	}
}
