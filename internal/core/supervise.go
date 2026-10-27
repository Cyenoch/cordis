package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

// Supervise watches a registered entry until explicitly stopped, canceled or its
// lifetime restart budget is exhausted. No service invocation is ever replayed.
func (e *Engine) Supervise(ctx context.Context, id string, budget uint32, delay time.Duration) error {
	if delay < 0 {
		return contract.Fail(contract.Invalid, "negative restart delay")
	}
	e.mu.Lock()
	i := e.instances[id]
	if i == nil || i.supervising {
		e.mu.Unlock()
		return contract.Fail(contract.Conflict, "entry missing or already supervised")
	}
	i.supervising = true
	e.mu.Unlock()
	defer func() { e.mu.Lock(); i.supervising = false; e.mu.Unlock() }()
	for {
		e.mu.Lock()
		state, failure, enabled, changed := i.state, i.failure, i.enabled, e.changed
		closed := e.closed || i.removing || e.instances[id] != i
		e.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if closed || !enabled || state == contract.Stopped {
			return nil
		}
		if state != contract.Failed {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
				continue
			}
		}
		if budget == 0 {
			return fmt.Errorf("restart budget exhausted for %s: %w", id, failure)
		}
		budget--
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := i.Retry(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var coded *contract.Error
			if errors.As(err, &coded) && coded.Code == contract.Conflict {
				// An explicit valid update can supersede a scheduled retry.
				continue
			}
			// Retry returns activation failures; reobserve the stable state before
			// spending another attempt or reporting exhaustion.
		}
	}
}
