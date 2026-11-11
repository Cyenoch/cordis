package sdk

import (
	"context"
	"io"
	"time"
)

// RunWithLimit is Run over a transport, with the limit and the reaction to
// passing it chosen by the test instead of ending the process.
func RunWithLimit(ctx context.Context, transport io.ReadWriteCloser, limit time.Duration, overdue func(), definitions ...Definition) error {
	return run(ctx, transport, limit, overdue, definitions...)
}
