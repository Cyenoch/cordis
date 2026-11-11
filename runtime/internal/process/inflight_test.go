package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestInflightLimitIsValidatedBeforeProcessCreation(t *testing.T) {
	for _, limit := range []int{-1, 8, contract.MaxInflight + 1} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			_, err := Start(t.Context(), filepath.Join(t.TempDir(), "missing"), nil, contract.Launch{
				Identity: contract.Identity{Activation: "budget"}, FrameBytes: contract.MaxFrameBytes, Inflight: limit,
				Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, nil },
			}, Options{})
			if !errors.Is(err, contract.Fail(contract.Invalid, "")) {
				t.Fatalf("invalid in-flight limit reached executable lookup: %v", err)
			}
		})
	}
}
