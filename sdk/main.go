package sdk

import (
	"context"
	"fmt"
	"os"

	"github.com/Cyenoch/cordis/contract"
)

// Main runs definitions as a plugin process. Protocol traffic alone uses stdout;
// failures go to stderr and terminate with status 1. Use Run to retain the error.
func Main(definitions ...contract.Definition) {
	if err := Run(context.Background(), definitions...); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Run serves definitions on standard input/output and handles native termination
// signals. Cancellation drains owned work and resources; it does not restart them.
func Run(ctx context.Context, definitions ...contract.Definition) error {
	ctx, stop := processContext(ctx)
	defer stop()
	return ServeDefinitions(ctx, definitions...)
}
