//go:build wasip1

package sdk

import "context"

func processContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}
