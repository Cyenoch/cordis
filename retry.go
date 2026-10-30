package cordis

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
	wait, err := h.engine.Retry(instance.Instance, instance.Status(), false)
	if err == nil {
		h.resetRecovery(entryID)
	}
	release()
	h.unlockOperation()
	if err != nil {
		return err
	}
	_, err = wait(ctx)
	return err
}
