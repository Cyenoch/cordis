package sdk

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func processContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}
