package process

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestInvalidGenericBudgetFailsBeforeExecutableLookup(t *testing.T) {
	for _, name := range []string{"default", "generic factory"} {
		t.Run(name, func(t *testing.T) {
			var factory *contract.SessionFactory
			if name == "generic factory" {
				factory = &contract.SessionFactory{Protocol: contract.GenericSession,
					Open: func(io.ReadWriteCloser, contract.Identity, contract.SessionHandler, int) (contract.Session, error) {
						t.Fatal("invalid budget reached session factory")
						return nil, nil
					},
				}
			}
			session, err := Start(t.Context(), filepath.Join(t.TempDir(), "missing"), nil, contract.Launch{
				Identity: contract.Identity{Activation: "a"}, SessionFactory: factory,
				Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, nil },
			}, Options{})
			var invalid *contract.Error
			if session != nil || !errors.As(err, &invalid) || invalid.Code != contract.Invalid {
				t.Fatalf("invalid budget was not rejected before executable lookup: session=%v err=%v", session, err)
			}
		})
	}
}
