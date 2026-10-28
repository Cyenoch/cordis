package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/wire"
)

func TestGenericInflightBudgetIsValidatedBeforeProcessCreation(t *testing.T) {
	for _, limit := range []int{-1, 8, 65} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			_, err := Start(t.Context(), filepath.Join(t.TempDir(), "missing"), nil, contract.Launch{
				Identity: contract.Identity{Activation: "budget"}, FrameBytes: contract.MaxFrameBytes,
				Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, nil },
			}, Options{Limits: wire.Limits{Inflight: limit}})
			if !errors.Is(err, contract.Fail(contract.Invalid, "")) {
				t.Fatalf("invalid inflight budget reached executable lookup: %v", err)
			}
		})
	}
}

func TestCustomSessionRetainsItsOwnInflightPolicy(t *testing.T) {
	factory := &contract.SessionFactory{
		Protocol: contract.CustomSession,
		Open: func(io.ReadWriteCloser, contract.Identity, contract.SessionHandler, int) (contract.Session, error) {
			t.Fatal("missing executable unexpectedly started")
			return nil, nil
		},
	}
	_, err := Start(t.Context(), filepath.Join(t.TempDir(), "missing"), nil, contract.Launch{
		Identity: contract.Identity{Activation: "custom-budget"}, SessionFactory: factory,
		Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, nil },
	}, Options{Limits: wire.Limits{Inflight: 8}})
	var pathError *os.PathError
	if !errors.As(err, &pathError) {
		t.Fatalf("generic inflight policy applied to custom protocol: %v", err)
	}
}
