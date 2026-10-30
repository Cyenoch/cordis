package cordis

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

// RecoveryPolicy allows finite retries of one persistent native entry.
// Initialization must tolerate repetition; business requests are never replayed.
type RecoveryPolicy struct {
	MaxRetries   int
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

type RecoveryState string

const (
	RecoveryIdle          RecoveryState = "idle"
	RecoveryScheduled     RecoveryState = "scheduled"
	RecoveryRetrying      RecoveryState = "retrying"
	RecoveryExhausted     RecoveryState = "exhausted"
	RecoveryCleanupFailed RecoveryState = "cleanup_failed"
	RecoveryCanceled      RecoveryState = "canceled"
	RecoveryMissing       RecoveryState = "missing"
)

type RecoveryInfo struct {
	Entry       string            `json:"entry"`
	Identity    contract.Identity `json:"identity"`
	Revision    uint64            `json:"revision"`
	Used        int               `json:"used"`
	MaxRetries  int               `json:"maxRetries"`
	State       RecoveryState     `json:"state"`
	NextRetryAt *time.Time        `json:"nextRetryAt,omitempty"`
}

type recoveryEntry struct {
	policy   RecoveryPolicy
	instance *hostInstance
	info     RecoveryInfo
}

func (h *Host) initRecovery(policies map[string]RecoveryPolicy) error {
	if len(policies) == 0 {
		return nil
	}
	entries, err := h.store.Entries()
	if err != nil {
		return err
	}
	saved := make(map[string]contract.Entry, len(entries))
	for _, entry := range entries {
		saved[entry.ID] = entry
	}
	h.recovery = make(map[string]*recoveryEntry, len(policies))
	h.options.Recovery = maps.Clone(policies)
	for id, policy := range policies {
		if policy.MaxRetries <= 0 || policy.InitialDelay <= 0 || policy.MaxDelay < policy.InitialDelay {
			return contract.Fail(contract.Invalid, fmt.Sprintf("recovery %q: positive retries/delay and maxDelay >= initialDelay required", id))
		}
		entry, exists := saved[id]
		if !exists {
			return contract.Fail(contract.Invalid, fmt.Sprintf("recovery %q: persistent entry does not exist", id))
		}
		pkg, err := h.store.Selected(entry.Package)
		if err != nil {
			return fmt.Errorf("recovery %q package: %w", id, err)
		}
		if pkg.Manifest.Runtime.Kind != "native" {
			return contract.Fail(contract.Invalid, fmt.Sprintf("recovery %q requires a native package", id))
		}
		h.recovery[id] = &recoveryEntry{policy: policy, info: RecoveryInfo{Entry: id, MaxRetries: policy.MaxRetries, State: RecoveryIdle}}
	}
	return nil
}

func (h *Host) startRecovery() {
	if len(h.recovery) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.recoveryCancel, h.recoveryDone = cancel, make(chan struct{})
	go func() {
		defer close(h.recoveryDone)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := h.recoverEntries(ctx); err != nil {
					if ctx.Err() == nil && h.options.Logger != nil {
						h.options.Logger.ErrorContext(ctx, "plugin recovery stopped", "error", err)
					}
					return
				}
			}
		}
	}()
}

// RecoveryStatus returns session-local policy state; unconfigured entries are absent.
func (h *Host) RecoveryStatus() []RecoveryInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := make([]RecoveryInfo, 0, len(h.recovery))
	for _, entry := range h.recovery {
		info := entry.info
		if info.NextRetryAt != nil {
			value := *info.NextRetryAt
			info.NextRetryAt = &value
		}
		result = append(result, info)
	}
	slices.SortFunc(result, func(a, b RecoveryInfo) int {
		if a.Entry < b.Entry {
			return -1
		}
		if a.Entry > b.Entry {
			return 1
		}
		return 0
	})
	return result
}

func (h *Host) recoverEntries(ctx context.Context) error {
	if err := h.lockOperation(ctx); err != nil {
		return err
	}
	defer h.unlockOperation()
	entries, err := h.store.Entries()
	if err != nil {
		return err
	}
	saved := make(map[string]contract.Entry, len(entries))
	for _, entry := range entries {
		saved[entry.ID] = entry
	}
	for id, recovery := range h.recovery {
		now := time.Now()
		h.mu.Lock()
		instance := h.entries[id]
		_, builtin := h.builtins[id]
		entry, exists := saved[id]
		if instance == nil || builtin || !exists || !entry.Enabled {
			state := RecoveryIdle
			if !exists {
				state = RecoveryMissing
			} else if !entry.Enabled {
				state = RecoveryCanceled
			}
			recovery.instance = nil
			h.recoveryStateLocked(recovery, state, nil)
			h.mu.Unlock()
			continue
		}
		status := instance.Status()
		if recovery.instance != instance || recovery.info.Revision != status.Desired {
			h.resetRecoveryLocked(id)
		}
		recovery.info.Identity = status.Identity
		recovery.info.Revision = status.Desired
		switch {
		case recovery.info.State == RecoveryCanceled || status.TemporaryStopped || !status.Enabled:
			h.recoveryStateLocked(recovery, RecoveryCanceled, nil)
		case status.State != contract.Failed:
			state := RecoveryIdle
			if recovery.info.State == RecoveryRetrying && status.State != contract.Active {
				state = RecoveryRetrying
			}
			h.recoveryStateLocked(recovery, state, nil)
		case !h.engine.FailedActivation(instance.Instance):
			h.recoveryStateLocked(recovery, RecoveryIdle, nil)
		case status.Outcome.CleanupCode != "":
			h.recoveryStateLocked(recovery, RecoveryCleanupFailed, nil)
		case recovery.info.Used >= recovery.policy.MaxRetries:
			h.recoveryStateLocked(recovery, RecoveryExhausted, nil)
		default:
			if recovery.info.NextRetryAt == nil {
				next := now.Add(recoveryDelay(recovery.policy, recovery.info.Used))
				h.recoveryStateLocked(recovery, RecoveryScheduled, &next)
			}
		}
		due := recovery.info.State == RecoveryScheduled && !now.Before(*recovery.info.NextRetryAt)
		h.mu.Unlock()
		if !due || h.packageBusy(entry.Package) {
			continue
		}
		release, err := h.reserveTree(id)
		if err != nil {
			continue
		}
		_, err = h.engine.Retry(instance.Instance, status, true)
		if err == nil {
			h.mu.Lock()
			recovery.info.Used++
			recovery.info.Revision = status.Desired + 1
			h.recoveryStateLocked(recovery, RecoveryRetrying, nil)
			h.mu.Unlock()
		}
		release()
	}
	return nil
}

func recoveryDelay(policy RecoveryPolicy, used int) time.Duration {
	delay := policy.InitialDelay
	for n := 0; n < used && delay < policy.MaxDelay; n++ {
		if delay > policy.MaxDelay/2 {
			return policy.MaxDelay
		}
		delay *= 2
	}
	return min(delay, policy.MaxDelay)
}

func (h *Host) resetRecovery(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.resetRecoveryLocked(id)
}

func (h *Host) resetRecoveryLocked(id string) {
	recovery := h.recovery[id]
	if recovery == nil {
		return
	}
	recovery.instance = h.entries[id]
	recovery.info.Used = 0
	if recovery.instance != nil {
		status := recovery.instance.Status()
		recovery.info.Identity, recovery.info.Revision = status.Identity, status.Desired
	}
	h.recoveryStateLocked(recovery, RecoveryIdle, nil)
}

func (h *Host) cancelRecoveryTree(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for target, recovery := range h.recovery {
		for current := target; current != ""; current = h.entrySpecs[current].Parent {
			if current == id {
				h.recoveryStateLocked(recovery, RecoveryCanceled, nil)
				break
			}
		}
	}
}

func (h *Host) recoveryStateLocked(entry *recoveryEntry, state RecoveryState, next *time.Time) {
	previous := entry.info.State
	entry.info.State, entry.info.NextRetryAt = state, next
	if previous != state && h.options.Logger != nil {
		h.recoveryEvents = append(h.recoveryEvents, entry.info)
	}
}

func (h *Host) flushRecoveryDiagnostics() {
	h.mu.Lock()
	events := h.recoveryEvents
	h.recoveryEvents = nil
	h.mu.Unlock()
	for _, event := range events {
		stage := string(event.State)
		if event.State == RecoveryRetrying {
			stage = "attempt"
		}
		h.options.Logger.Info("plugin recovery", "operation", "recovery", "stage", stage,
			"entry", event.Entry, "activation", event.Identity.Activation, "package", event.Identity.Package,
			"digest", event.Identity.Digest, "revision", event.Revision, "used", event.Used,
			"maxRetries", event.MaxRetries, "nextRetryAt", event.NextRetryAt)
	}
}
