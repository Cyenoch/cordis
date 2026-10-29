package cordis

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

type remoteSubscription struct {
	handle   contract.Subscription
	mu       sync.Mutex
	queued   <-chan struct{}
	canceled bool
}

// remoteSubscriptions maps wire identities to core handles and send receipts.
// Ordering, active quotas and admission remain owned by core.
type remoteSubscriptions struct {
	mu      sync.Mutex
	last    uint64
	owner   contract.Identity
	scope   *core.Scope
	plugin  genericPlugin
	grace   time.Duration
	fail    func(error)
	entries map[string]*remoteSubscription
}

func (r *remoteSubscriptions) subscribe(ctx context.Context, spec contract.SubscriptionSpec) error {
	number, err := contract.SubscriptionNumber(r.owner.Activation, spec.ID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if number <= r.last {
		return contract.Fail(contract.Invalid, "subscription sequence already used")
	}
	r.last = number
	sub := &remoteSubscription{}
	handle, err := r.scope.OnPrepared(ctx, spec.Name, spec.Options, func() contract.EventHandler {
		queued := make(chan struct{})
		sub.mu.Lock()
		// The activation gate serializes callbacks. Earlier sends are settled
		// before another callback can reserve this subscription's next send.
		sub.queued = queued
		sub.mu.Unlock()
		return func(ctx context.Context, input json.RawMessage) (contract.EventResult, error) {
			sent := false
			result, err := r.plugin.Deliver(ctx, contract.EventDelivery{ID: spec.ID, Input: input}, func(queuedFrame bool) {
				sent = queuedFrame
				close(queued)
				sub.mu.Lock()
				canceled := sub.canceled
				sub.mu.Unlock()
				if spec.Options.Once || canceled {
					r.forget(spec.ID, sub)
				}
			})
			if spec.Options.Once && !sent {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.grace)
				defer cancel()
				if cleanupErr := r.plugin.ForgetSubscription(cleanup, spec.ID); cleanupErr != nil {
					r.fail(fmt.Errorf("release undelivered once subscription: %w", cleanupErr))
				}
			}
			return result, err
		}
	})
	if err != nil {
		return err
	}
	sub.handle = handle
	r.entries[spec.ID] = sub
	return nil
}

func (r *remoteSubscriptions) forget(id string, sub *remoteSubscription) {
	r.mu.Lock()
	if r.entries[id] == sub {
		delete(r.entries, id)
	}
	r.mu.Unlock()
}

func (r *remoteSubscriptions) unsubscribe(ctx context.Context, id string) error {
	if _, err := contract.SubscriptionNumber(r.owner.Activation, id); err != nil {
		return err
	}
	r.mu.Lock()
	sub := r.entries[id]
	r.mu.Unlock()
	if sub == nil {
		return nil
	}
	if err := sub.handle.Cancel(ctx); err != nil {
		return err
	}
	sub.mu.Lock()
	sub.canceled = true
	queued := sub.queued
	sub.mu.Unlock()
	if queued != nil {
		select {
		case <-queued:
		default:
			select {
			case <-queued:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	r.forget(id, sub)
	return nil
}
