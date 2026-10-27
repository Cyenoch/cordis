package core_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func TestExplicitSupervisorSpendsFiniteBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer func() { _ = e.Close(t.Context()) }()
		var starts atomic.Uint32
		failure := errors.New("activation deliberately failed")
		i, err := e.Add(contract.Entry{ID: "crash", Enabled: true, Config: []byte(`{}`)}, contract.Definition{ID: "crash", Start: func(context.Context, contract.Scope) error { starts.Add(1); return failure }}, contract.Identity{})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Supervise(t.Context(), "crash", 2, time.Second); !errors.Is(err, failure) {
			t.Fatalf("exhaustion lost cause: %v", err)
		}
		if starts.Load() != 3 || i.Status().State != contract.Failed {
			t.Fatalf("budget not finite: starts=%d status=%+v", starts.Load(), i.Status())
		}
		synctest.Wait()
		if starts.Load() != 3 {
			t.Fatal("implicit retry after exhaustion")
		}
	})
}
