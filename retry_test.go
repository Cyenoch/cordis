package cordis_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

func TestHostRetryUsesCurrentFailedInstance(t *testing.T) {
	h, err := cordis.Open(cordis.Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close(t.Context()) }()
	var starts atomic.Int32
	i, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "example"}, contract.Definition{
		Start: func(context.Context, contract.Scope) error {
			if starts.Add(1) == 1 {
				return errors.New("initial failure")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = i.Wait(t.Context())
	if err := h.Retry(t.Context(), "example"); err != nil {
		t.Fatal(err)
	}
	if i.Status().State != contract.Active || starts.Load() != 2 {
		t.Fatalf("retry did not activate: %+v", i.Status())
	}
	if err := h.Retry(t.Context(), "example"); err == nil {
		t.Fatal("retry accepted an Active instance")
	}
	if err := h.Retry(t.Context(), "absent"); err == nil {
		t.Fatal("retry created a missing entry")
	}
}
