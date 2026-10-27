package sdk

import (
	"context"
	"io"

	"github.com/Cyenoch/cordis/contract"
)

// ServeDefinitions lets one package artifact implement several independently
// instantiated definitions. The host chooses one definition during initialization.
func ServeDefinitions(ctx context.Context, definitions ...contract.Definition) error {
	transport, err := stdioTransport()
	if err != nil {
		return err
	}
	return serveDefinitions(ctx, transport, definitions)
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
