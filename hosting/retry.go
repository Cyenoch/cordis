package hosting

import (
	"context"

	"github.com/Cyenoch/cordis/contract"
)

// Retry starts a new revision of the current static Failed instance without
// changing its saved configuration. Cancellation stops waiting, not the revision.
func (h *Host) Retry(ctx context.Context, entryID string) error {
	if err := h.lockOperation(ctx); err != nil {
		return err
	}
	if err := h.requireStatic(entryID); err != nil {
		h.unlockOperation()
		return err
	}
	h.mu.Lock()
	instance := h.entries[entryID]
	h.mu.Unlock()
	if instance == nil {
		h.unlockOperation()
		return contract.Fail(contract.NotReady, "entry is not registered")
	}
	release, err := h.reserveTree(entryID)
	if err != nil {
		h.unlockOperation()
		return err
	}
	if instance.Status().State != contract.Failed {
		release()
		h.unlockOperation()
		return contract.Fail(contract.NotReady, "entry is not failed")
	}
	instance.retry()
	h.resetRecovery(entryID)
	release()
	h.unlockOperation()
	return instance.Wait(ctx)
}

// retry restarts a failed entry as a new revision and returns it. It does
// not wait for the restart.
func (i *hostInstance) retry() uint64 {
	i.mu.Lock()
	i.desired++
	revision := i.desired
	i.mu.Unlock()
	i.restart()
	return revision
}

// restart submits a restart of the kernel instance without waiting for it.
func (i *hostInstance) restart() {
	// Restart takes effect before it waits, so a context that is already
	// done submits the restart without waiting for it.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	i.instance.Restart(ctx)
}
