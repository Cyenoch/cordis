package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
