package cordis

import (
	"context"
	"errors"
)

// InstanceChange reports one state transition of an instance.
type InstanceChange struct {
	Instance Info
	From, To State
	Err      error
}

// ServiceChange reports that a service became available or unavailable.
type ServiceChange struct {
	Service   string
	Provider  Info
	Available bool
}

// InstanceChanged is emitted for every state transition, in order per
// instance. Listener errors are logged and do not affect the instance.
var InstanceChanged = NewEvent[InstanceChange]("cordis/instance")

// ServiceChanged is emitted when a service becomes available or unavailable.
// Only listeners that resolve the service in the same space receive it.
var ServiceChanged = NewEvent[ServiceChange]("cordis/service")

type notice struct {
	instance *InstanceChange
	service  *ServiceChange
	realm    *Realm
}

// noticeLocked queues a lifecycle event. A separate goroutine delivers the
// queue in order, so listeners may call back into the kernel and wait.
func (a *App) noticeLocked(n notice) {
	a.notices = append(a.notices, n)
	if !a.dispatches {
		a.dispatches = true
		go a.dispatch()
	}
}

func (a *App) dispatch() {
	for {
		a.mu.Lock()
		if len(a.notices) == 0 {
			a.dispatches = false
			if a.root.state == Stopped {
				a.closeDoneLocked()
			}
			a.mu.Unlock()
			return
		}
		next := a.notices[0]
		a.notices = a.notices[1:]
		a.mu.Unlock()

		var err error
		switch {
		case next.instance != nil:
			change := *next.instance
			if logger := a.options.Logger; logger != nil {
				attrs := []any{"instance", change.Instance.Name, "id", change.Instance.ID, "from", change.From.String(), "to", change.To.String()}
				if change.To == Failed && change.Err != nil {
					logger.Error("instance failed", append(attrs, "error", change.Err)...)
				} else {
					logger.Info("instance state", attrs...)
				}
			}
			err = deliver(a, InstanceChanged, change, nil)
		case next.service != nil:
			change := *next.service
			err = deliver(a, ServiceChanged, change, func(listener *Scope) bool {
				return realmOf(listener, change.Service) == next.realm
			})
		}
		if err != nil && a.options.Logger != nil {
			a.options.Logger.Warn("lifecycle listener failed", "error", err)
		}
	}
}

// deliver calls every listener of a lifecycle event in registration order.
// One failing listener does not hide the event from the others. match, when
// set, applies to all listeners, Global ones included.
func deliver[T any](a *App, event Event[T], payload T, match func(*Scope) bool) error {
	key := handlerKey{kindEvent, event.name}
	var errs []error
	for _, h := range a.reachable(a.scope, key) {
		if match != nil && !match(h.scope) {
			continue
		}
		if err := event.call(context.Background(), a, key, h, payload); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// finishWhenIdleLocked completes Close once queued lifecycle events are out.
func (a *App) finishWhenIdleLocked() {
	if !a.dispatches {
		a.closeDoneLocked()
	}
}

func (a *App) closeDoneLocked() {
	select {
	case <-a.done:
	default:
		close(a.done)
	}
}
