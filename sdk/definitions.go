package sdk

import (
	"context"
	"io"

	"github.com/Cyenoch/cordis/contract"
)

// RunTransport serves a definition over an explicitly owned protocol stream.
func RunTransport(ctx context.Context, definition contract.Definition, transport io.ReadWriteCloser) error {
	return serveDefinitions(ctx, transport, []contract.Definition{definition})
}

func serveDefinitions(ctx context.Context, transport io.ReadWriteCloser, definitions []contract.Definition) error {
	byID := make(map[string]contract.Definition, len(definitions))
	for _, definition := range definitions {
		if definition.ID == "" || definition.Start == nil || byID[definition.ID].Start != nil {
			return contract.Fail(contract.Invalid, "invalid or duplicate SDK definition")
		}
		byID[definition.ID] = definition
	}
	if len(byID) == 0 {
		return contract.Fail(contract.Invalid, "at least one SDK definition required")
	}
	return serve(ctx, byID, transport)
}
