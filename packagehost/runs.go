package packagehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

const (
	maxTerminalRuns               = 256
	maxRunningRuns                = 10000
	maxRunsBytes                  = 16 << 20
	commitRunsSynced  commitStage = "runs-next-synced"
	commitRunsSwapped commitStage = "runs-swapped"
)

// RunRecord records a host-reported activation, not a process-liveness check.
// State is running, stopped, completed, failed, canceled, or interrupted.
type RunRecord struct {
	Owner     contract.Identity `json:"owner"`
	State     string            `json:"state"`
	StartedAt time.Time         `json:"startedAt"`
	EndedAt   time.Time         `json:"endedAt"`
	Outcome   contract.Outcome  `json:"outcome"`
}

func validRunOwner(owner contract.Identity) bool {
	return validName(owner.Package) && validName(owner.Entry) && validName(owner.Activation) &&
		(validDigest(owner.Digest) || owner.Digest == "builtin")
}

func terminalRunState(state string) bool {
	switch state {
	case "stopped", "completed", "failed", "canceled", "interrupted":
		return true
	default:
		return false
	}
}

// BeginRun must succeed before the host starts this activation. Existing running
// owners are idempotent; a retained terminal activation cannot start again.
// At 10,000 running records new starts fail instead of evicting live owners.
func (s *Store) BeginRun(ctx context.Context, owner contract.Identity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if !validRunOwner(owner) {
		return fmt.Errorf("invalid run owner")
	}
	running := 0
	for _, run := range s.runs {
		if run.Owner.Activation == owner.Activation {
			if run.Owner == owner && run.State == "running" {
				return ctx.Err()
			}
			return fmt.Errorf("activation %s already recorded", owner.Activation)
		}
		if run.State == "running" {
			running++
		}
	}
	if running == maxRunningRuns {
		return fmt.Errorf("running record quota exceeded")
	}
	runs := append(slices.Clone(s.runs), RunRecord{Owner: owner, State: "running", StartedAt: time.Now().UTC()})
	return s.commitRuns(ctx, runs)
}

// EndRun finishes exactly the matching activation. Repeating its terminal state
// is idempotent; stale identities and conflicting terminal transitions fail.
func (s *Store) EndRun(ctx context.Context, owner contract.Identity, state string, outcome contract.Outcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if !terminalRunState(state) {
		return fmt.Errorf("invalid terminal run state %q", state)
	}
	if err := validateOutcome(outcome); err != nil {
		return err
	}
	for i, run := range s.runs {
		if run.Owner != owner {
			continue
		}
		if run.State == state {
			return ctx.Err()
		}
		if run.State != "running" {
			return fmt.Errorf("activation %s is already %s", owner.Activation, run.State)
		}
		run.State, run.EndedAt = state, time.Now().UTC()
		run.Outcome = outcome.Clone()
		runs := slices.Delete(slices.Clone(s.runs), i, i+1)
		runs = append(runs, run)
		return s.commitRuns(ctx, trimRuns(runs))
	}
	return fmt.Errorf("run owner is not recorded: %s", owner.Activation)
}

// Runs returns independent records in update order, oldest first. Open persists
// leftover running records as interrupted before returning the Store.
func (s *Store) Runs() ([]RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return nil, err
	}
	runs := slices.Clone(s.runs)
	for n := range runs {
		runs[n].Outcome = runs[n].Outcome.Clone()
	}
	return runs, nil
}

func trimRuns(runs []RunRecord) []RunRecord {
	terminal := 0
	for _, run := range runs {
		if run.State != "running" {
			terminal++
		}
	}
	return slices.DeleteFunc(runs, func(run RunRecord) bool {
		if run.State != "running" && terminal > maxTerminalRuns {
			terminal--
			return true
		}
		return false
	})
}

func (s *Store) commitRuns(ctx context.Context, runs []RunRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	next := filepath.Join(s.root, "runs.next")
	raw, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runs: %w", err)
	}
	if len(raw) > maxRunsBytes {
		return fmt.Errorf("run history exceeds byte quota")
	}
	if err := durableBytes(next, raw); err != nil {
		return fmt.Errorf("write runs candidate: %w", err)
	}
	s.observeCommit(commitRunsSynced)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := replaceFile(next, filepath.Join(s.root, "runs.json")); err != nil {
		return fmt.Errorf("replace runs: %w", err)
	}
	s.runs = runs
	s.observeCommit(commitRunsSwapped)
	if err := syncDir(s.root); err != nil {
		return &CommitError{Err: fmt.Errorf("sync runs: %w", err)}
	}
	return nil
}

func (s *Store) recoverRuns() error {
	file, err := openRegular(nil, filepath.Join(s.root, "runs.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		raw, readErr := io.ReadAll(io.LimitReader(file, maxRunsBytes+1))
		if err = errors.Join(readErr, file.Close()); err != nil {
			return err
		}
		if len(raw) > maxRunsBytes {
			return fmt.Errorf("run history exceeds byte quota")
		}
		if err = contract.DecodeWithLimits(raw, &s.runs, 4, maxRunningRuns+maxTerminalRuns); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	running, terminal := 0, 0
	for _, run := range s.runs {
		if err := validateOutcome(run.Outcome); err != nil {
			return err
		}
		if !validRunOwner(run.Owner) || seen[run.Owner.Activation] || run.StartedAt.IsZero() {
			return fmt.Errorf("invalid or duplicate durable run owner")
		}
		seen[run.Owner.Activation] = true
		if run.State == "running" && run.EndedAt.IsZero() {
			running++
		} else if terminalRunState(run.State) && !run.EndedAt.IsZero() {
			terminal++
		} else {
			return fmt.Errorf("invalid durable run state")
		}
	}
	if running > maxRunningRuns || terminal > maxTerminalRuns {
		return fmt.Errorf("durable run count quota exceeded")
	}
	if running > 0 {
		runs := make([]RunRecord, 0, len(s.runs))
		for _, run := range s.runs {
			if run.State != "running" {
				runs = append(runs, run)
			}
		}
		now := time.Now().UTC()
		for _, run := range s.runs {
			if run.State == "running" {
				run.State, run.EndedAt = "interrupted", now
				run.Outcome.Reason = contract.ExitInterrupted
				runs = append(runs, run)
			}
		}
		if err = s.commitRuns(context.Background(), trimRuns(runs)); err != nil {
			return err
		}
	}
	if err = os.Remove(filepath.Join(s.root, "runs.next")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(s.root)
}

func validateOutcome(outcome contract.Outcome) error {
	switch outcome.Reason {
	case "", contract.ExitStopped, contract.ExitUnexpected, contract.ExitProtocol, contract.ExitInterrupted:
	default:
		return fmt.Errorf("invalid run exit reason")
	}
	for _, code := range []contract.Code{outcome.FailureCode, outcome.CleanupCode} {
		switch code {
		case "", contract.Invalid, contract.Incompatible, contract.NotReady, contract.Denied,
			contract.Timeout, contract.Canceled, contract.Reentrant, contract.PluginFailed,
			contract.Internal, contract.Conflict, contract.Superseded, contract.Panicked:
		default:
			return fmt.Errorf("invalid run error classification")
		}
	}
	if len(outcome.Signal) > 64 || outcome.StartupDuration < 0 {
		return fmt.Errorf("invalid run outcome")
	}
	return nil
}
