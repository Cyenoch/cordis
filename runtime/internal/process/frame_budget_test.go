package process

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestInvalidGenericBudgetFailsBeforeExecutableLookup(t *testing.T) {
	for _, factory := range []*contract.SessionFactory{nil, {Protocol: contract.GenericSession}} {
		_, err := Start(context.Background(), "not-an-executable", nil, contract.Launch{
			Identity: contract.Identity{Activation: "a"}, SessionFactory: factory,
			Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, nil },
		}, Options{})
		if err == nil || !strings.Contains(err.Error(), "frameBytes") && !strings.Contains(err.Error(), "opener") {
			t.Fatalf("invalid budget reached executable lookup: %v", err)
		}
	}
}
