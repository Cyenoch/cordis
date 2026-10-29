package process

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestInvalidGenericBudgetFailsBeforeExecutableLookup(t *testing.T) {
	session, err := Start(t.Context(), filepath.Join(t.TempDir(), "missing"), nil, contract.Launch{
		Identity: contract.Identity{Activation: "a"},
		Handle:   func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, nil },
	}, Options{})
	var invalid *contract.Error
	if session != nil || !errors.As(err, &invalid) || invalid.Code != contract.Invalid {
		t.Fatalf("invalid budget was not rejected before executable lookup: session=%v err=%v", session, err)
	}
}
