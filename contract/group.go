package contract

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Group owns cleanups released once, serially in reverse registration order.
// Its zero value is ready for standalone use. A Group must not be copied after use.
// Use NewGroup to make a Scope or another Group own its lifetime.
type Group struct {
	mu       sync.Mutex
	cleanups []Cleanup
	done     chan struct{}
	err      error // Written before done closes; read only after receiving from done.
}

// NewGroup registers the group's Close with owner before returning it. An owner
// can be a Scope or another Group. If registration fails, no group is returned.
func NewGroup(owner interface{ Defer(Cleanup) error }) (*Group, error) {
	if owner == nil {
		return nil, Fail(Invalid, "resource group owner is required")
	}
	g := &Group{}
	if err := owner.Defer(g.Close); err != nil {
		return nil, fmt.Errorf("register resource group: %w", err)
	}
	return g, nil
}

// Defer takes ownership of cleanup if registration succeeds. Nil cleanups and
// registrations after the first Close begins are rejected without taking ownership.
func (g *Group) Defer(cleanup Cleanup) error {
	if cleanup == nil {
		return Fail(Invalid, "nil cleanup")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done != nil {
		return Fail(NotReady, "resource group is closing or closed")
	}
	g.cleanups = append(g.cleanups, cleanup)
	return nil
}

// Close starts one shared release and waits for it. All completed waits return
// the same aggregate error, including recovered cleanup panics. Canceling ctx
// stops only this caller's wait, not cleanup. Cleanup receives the first caller's
// context values but no cancellation or deadline, preserving Scope cleanup bindings.
// A blocked cleanup blocks earlier cleanups; Close never claims it was released.
func (g *Group) Close(ctx context.Context) error {
	g.mu.Lock()
	if g.done == nil {
		g.done = make(chan struct{})
		cleanups := g.cleanups
		g.cleanups = nil
		if len(cleanups) == 0 {
			close(g.done)
		} else {
			go g.release(context.WithoutCancel(ctx), cleanups)
		}
	}
	done := g.done
	g.mu.Unlock()

	// Prefer the cached result when release already completed, even if ctx is canceled.
	select {
	case <-done:
		return g.err
	default:
	}
	select {
	case <-done:
		return g.err
	case <-ctx.Done():
		select {
		case <-done:
			return g.err
		default:
			return ctx.Err()
		}
	}
}

func (g *Group) release(ctx context.Context, cleanups []Cleanup) {
	var errs []error
	for n := len(cleanups) - 1; n >= 0; n-- {
		cleanup := cleanups[n]
		cleanups[n] = nil
		if err := invokeGroupCleanup(ctx, cleanup); err != nil {
			errs = append(errs, fmt.Errorf("resource group cleanup [%d]: %w", n, err))
		}
	}
	g.err = errors.Join(errs...)
	close(g.done)
}

func invokeGroupCleanup(ctx context.Context, cleanup Cleanup) (err error) {
	defer func() {
		if value := recover(); value != nil {
			if cause, ok := value.(error); ok {
				err = fmt.Errorf("cleanup panic: %w", cause)
			} else {
				err = fmt.Errorf("cleanup panic: %v", value)
			}
		}
	}()
	return cleanup(ctx)
}
