package sdk

import (
	"context"

	"github.com/Cyenoch/cordis/contract"
)

// Run serves definitions on standard input/output and handles native termination
// signals. Cancellation drains owned work and resources; it does not restart them.
func Run(ctx context.Context, definitions ...contract.Definition) error {
	ctx, stop := processContext(ctx)
	defer stop()
	transport, err := stdioTransport()
	if err != nil {
		return err
	}
	return serveDefinitions(ctx, transport, definitions)
}
