package access_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Cyenoch/cordis/access"
	"github.com/Cyenoch/cordis/contract"
)

func TestTaskTransferSurvivesActivationAndCallerCancellation(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("first")
	activate(t, policy, owner)
	manager := access.NewTaskManager(policy)
	caller, cancelCaller := context.WithCancel(t.Context())
	defer cancelCaller()
	task, err := manager.Create(caller, owner, "host-transaction", "test-generation")
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Start(); err != nil {
		t.Fatal(err)
	}
	if err := task.Transfer(t.Context()); err != nil {
		t.Fatal(err)
	}
	cancelCaller()
	if err := policy.Revoke(owner); err != nil {
		t.Fatal(err)
	}
	if err := task.Context().Err(); err != nil {
		t.Fatalf("transferred task canceled: %v", err)
	}
	if !task.Status().HostOwned {
		t.Fatal("host ownership missing")
	}
	if err := manager.Finish(owner, "host-transaction", "test-generation", access.TaskCompleted, json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("retired guest completed host task")
	}
	if err := task.Finish(access.TaskCompleted, json.RawMessage(`{"committed":true}`), nil); err != nil {
		t.Fatal(err)
	}
	if task.Status().State != access.TaskCompleted {
		t.Fatal("host task did not complete")
	}
	if err := task.Cancel(); err == nil {
		t.Fatal("terminal state overwritten")
	}
}

func TestTaskTerminalTransitionIsUniqueAndRevokeInterrupts(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("first")
	activate(t, policy, owner)
	manager := access.NewTaskManager(policy)
	task, err := manager.Create(t.Context(), owner, "race", "test-generation")
	if err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			if err := task.Finish(access.TaskCompleted, json.RawMessage(`null`), nil); err == nil {
				successes.Add(1)
			}
		})
	}
	group.Wait()
	if successes.Load() != 1 {
		t.Fatalf("terminal commits = %d", successes.Load())
	}
	unfinished, err := manager.Create(t.Context(), owner, "unfinished", "test-generation")
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.Revoke(owner); err != nil {
		t.Fatal(err)
	}
	if unfinished.Context().Err() == nil || unfinished.Status().State != access.TaskInterrupted {
		t.Fatalf("unowned task still running: %#v", unfinished.Status())
	}
	if _, err := manager.Create(t.Context(), owner, "late", "test-generation"); err == nil {
		t.Fatal("stopped owner created task")
	}
}

// Gate only the registered cancellation callback, leaving Err observable immediately.
// This models a callback waiting for the policy lock without relying on scheduler timing.
type delayedTaskParent struct {
	context.Context
	release <-chan struct{}
}

func (c delayedTaskParent) Value(any) any { return nil }

func (c delayedTaskParent) AfterFunc(callback func()) func() bool {
	return context.AfterFunc(c.Context, func() {
		<-c.release
		callback()
	})
}

func TestTaskCancellationPrecedesFinishAndTransfer(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"handle-finish", "guest-finish", "transfer", "host-finish"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			policy := access.NewPolicy()
			owner := identity("first")
			activate(t, policy, owner)
			manager := access.NewTaskManager(policy)
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			release := make(chan struct{})
			defer close(release)
			gated := delayedTaskParent{Context: parent, release: release}
			initialParent := context.Context(gated)
			if name == "host-finish" {
				initialParent = t.Context()
			}
			task, err := manager.Create(initialParent, owner, "canceled", "test-generation")
			if err != nil {
				t.Fatal(err)
			}
			if err := task.Start(); err != nil {
				t.Fatal(err)
			}
			if name == "host-finish" {
				if err := task.Transfer(gated); err != nil {
					t.Fatal(err)
				}
			}
			cancel()
			switch name {
			case "guest-finish":
				err = manager.Finish(owner, "canceled", "test-generation", access.TaskCompleted, json.RawMessage(`true`), nil)
			case "transfer":
				err = task.Transfer(t.Context())
			default:
				err = task.Finish(access.TaskCompleted, json.RawMessage(`true`), nil)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled parent accepted transition: %v", err)
			}
			status := task.Status()
			if status.State != access.TaskCanceled || len(status.Result) != 0 {
				t.Fatalf("late success retained: %#v", status)
			}
			if !errors.Is(task.Err(), context.Canceled) || !errors.Is(task.Context().Err(), context.Canceled) {
				t.Fatal("canceled task lost host cancellation state")
			}
			select {
			case <-task.Done():
			default:
				t.Fatal("canceled task not terminal")
			}
			if name == "transfer" && status.HostOwned {
				t.Fatal("canceled work escaped to host ownership")
			}
		})
	}
}

func TestTaskFloodQuotaAndRetirementPreserveHostHandles(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("first")
	activate(t, policy, owner)
	manager := access.NewTaskManager(policy)
	failure := errors.New("host-only failure detail")
	failed, err := manager.Create(t.Context(), owner, "failed", "test-generation")
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.Finish(access.TaskFailed, json.RawMessage(`{"failure":true}`), failure); err != nil {
		t.Fatal(err)
	}
	transferred, err := manager.Create(t.Context(), owner, "transferred", "test-generation")
	if err != nil {
		t.Fatal(err)
	}
	if err := transferred.Transfer(t.Context()); err != nil {
		t.Fatal(err)
	}
	unfinished, err := manager.Create(t.Context(), owner, "unfinished", "test-generation")
	if err != nil {
		t.Fatal(err)
	}
	for i := 3; i < access.MaxTasksPerActivation; i++ {
		id := fmt.Sprintf("flood-%d", i)
		if _, err := manager.Create(t.Context(), owner, id, "test-generation"); err != nil {
			t.Fatalf("task %d below quota: %v", i, err)
		}
		if err := manager.Finish(owner, id, "test-generation", access.TaskCompleted, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := manager.Create(t.Context(), owner, "over-quota", "test-generation"); !errors.Is(err, access.ErrTaskQuota) {
		t.Fatalf("completed-task flood bypassed quota: %v", err)
	}
	if got := len(manager.Snapshot()); got != access.MaxTasksPerActivation {
		t.Fatalf("quota snapshot size = %d", got)
	}
	if err := policy.Revoke(owner); err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot()
	if len(snapshot) != 1 || snapshot[0].ID != "transferred" || !snapshot[0].HostOwned {
		t.Fatalf("retired owner ledger retained: %#v", snapshot)
	}
	if failed.Status().State != access.TaskFailed || !errors.Is(failed.Err(), failure) || string(failed.Status().Result) != `{"failure":true}` {
		t.Fatal("retirement destroyed host-held terminal handle")
	}
	if unfinished.Status().State != access.TaskInterrupted || !errors.Is(unfinished.Err(), context.Canceled) {
		t.Fatal("retirement lost interruption state")
	}
	if err := transferred.Finish(access.TaskCompleted, json.RawMessage(`true`), nil); err != nil {
		t.Fatalf("retained host task unusable: %v", err)
	}
	replacement := identity("replacement")
	activate(t, policy, replacement)
	if _, err := manager.Create(t.Context(), replacement, "new-budget", "test-generation"); err != nil {
		t.Fatalf("retired owner consumed new activation quota: %v", err)
	}
}

func TestTaskResultQuotaBoundaries(t *testing.T) {
	t.Parallel()
	for _, guest := range []bool{false, true} {
		t.Run(fmt.Sprintf("guest-%t", guest), func(t *testing.T) {
			t.Parallel()
			policy := access.NewPolicy()
			owner := identity("first")
			activate(t, policy, owner)
			manager := access.NewTaskManager(policy)
			finish := func(task *access.Task, result json.RawMessage) error {
				if guest {
					return manager.Finish(owner, task.Status().ID, task.Status().Token, access.TaskCompleted, result, nil)
				}
				return task.Finish(access.TaskCompleted, result, nil)
			}
			result := json.RawMessage(`"` + strings.Repeat("x", access.MaxTaskResultBytes-2) + `"`)
			var retained *access.Task
			for i := range access.MaxTaskResultBytesPerActivation / access.MaxTaskResultBytes {
				task, err := manager.Create(t.Context(), owner, fmt.Sprintf("result-%d", i), "test-generation")
				if err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					retained = task
					tooLarge := json.RawMessage(`"` + strings.Repeat("x", access.MaxTaskResultBytes-1) + `"`)
					if err := finish(task, tooLarge); !errors.Is(err, access.ErrTaskQuota) {
						t.Fatalf("oversized result accepted: %v", err)
					}
					if task.Status().State != access.TaskQueued {
						t.Fatal("rejected result changed task state")
					}
				}
				if err := finish(task, result); err != nil {
					t.Fatalf("result at byte boundary rejected: %v", err)
				}
			}
			over, err := manager.Create(t.Context(), owner, "aggregate-overflow", "test-generation")
			if err != nil {
				t.Fatal(err)
			}
			if err := finish(over, json.RawMessage(`0`)); !errors.Is(err, access.ErrTaskQuota) {
				t.Fatalf("aggregate result bytes unbounded: %v", err)
			}
			if err := finish(over, nil); err != nil {
				t.Fatalf("quota rejection prevented result-free finish: %v", err)
			}
			if err := manager.Release(owner, retained.Status().ID, retained.Status().Token); err != nil {
				t.Fatal(err)
			}
			replacement, err := manager.Create(t.Context(), owner, "replacement-result", "new-generation")
			if err != nil {
				t.Fatal(err)
			}
			if err := finish(replacement, result); err != nil {
				t.Fatalf("release did not reclaim result budget: %v", err)
			}
			if string(retained.Status().Result) != string(result) {
				t.Fatal("release destroyed host-held result")
			}
			if err := policy.Revoke(owner); err != nil {
				t.Fatal(err)
			}
			if len(manager.Snapshot()) != 0 {
				t.Fatal("retired results remain in manager snapshot")
			}
		})
	}
}

func TestTaskFinishRejectsMalformedResultsWithoutCommitting(t *testing.T) {
	t.Parallel()
	for _, result := range []string{`{"a":1,"a":2}`, `true false`, strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33)} {
		t.Run(result, func(t *testing.T) {
			t.Parallel()
			policy := access.NewPolicy()
			owner := identity("first")
			activate(t, policy, owner)
			manager := access.NewTaskManager(policy)
			task, err := manager.Create(t.Context(), owner, "invalid-result", "test-generation")
			if err != nil {
				t.Fatal(err)
			}
			for _, err := range []error{
				task.Finish(access.TaskCompleted, json.RawMessage(result), nil),
				manager.Finish(owner, "invalid-result", "test-generation", access.TaskCompleted, json.RawMessage(result), nil),
			} {
				var coded *contract.Error
				if !errors.As(err, &coded) || coded.Code != contract.Invalid {
					t.Fatalf("invalid result error = %v", err)
				}
			}
			if task.Status().State != access.TaskQueued {
				t.Fatal("invalid result committed task")
			}
		})
	}
}

func TestTaskReleasePreservesEveryTerminalState(t *testing.T) {
	t.Parallel()
	for _, state := range []access.TaskState{
		access.TaskCompleted, access.TaskFailed, access.TaskCanceled, access.TaskInterrupted,
	} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()
			policy := access.NewPolicy()
			owner := identity("release")
			activate(t, policy, owner)
			manager := access.NewTaskManager(policy)
			task, err := manager.Create(t.Context(), owner, "job", "generation")
			if err != nil {
				t.Fatal(err)
			}
			requireAccessCode(t, task.Release(), contract.Conflict)
			var failure error
			if state != access.TaskCompleted {
				failure = errors.New("host-only task failure")
			}
			if err := task.Finish(state, json.RawMessage(`{"retained":true}`), failure); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := task.Release(); err != nil {
					t.Fatal(err)
				}
			}
			status := task.Status()
			if status.State != state || string(status.Result) != `{"retained":true}` || task.Err() != failure {
				t.Fatalf("release changed terminal handle: %#v, %v", status, task.Err())
			}
			select {
			case <-task.Done():
			default:
				t.Fatal("released task lost terminal signal")
			}
			if len(manager.Snapshot()) != 0 {
				t.Fatal("released task retained in ledger")
			}
		})
	}
}

func TestTaskReleaseAllowsLongLivedActivation(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("long-lived")
	activate(t, policy, owner)
	manager := access.NewTaskManager(policy)
	for cycle := range 3 {
		handles := make([]*access.Task, 0, access.MaxTasksPerActivation)
		for i := range access.MaxTasksPerActivation {
			id := fmt.Sprintf("job-%d", i)
			token := contract.NewID()
			task, err := manager.Create(t.Context(), owner, id, token)
			if err != nil {
				t.Fatalf("cycle %d task %d: %v", cycle, i, err)
			}
			if err := manager.Finish(owner, id, token, access.TaskCompleted, json.RawMessage(`true`), nil); err != nil {
				t.Fatal(err)
			}
			handles = append(handles, task)
		}
		if _, err := manager.Create(t.Context(), owner, "overflow", contract.NewID()); !errors.Is(err, access.ErrTaskQuota) {
			t.Fatalf("retained terminal tasks bypassed quota: %v", err)
		}
		if len(manager.Snapshot()) != access.MaxTasksPerActivation {
			t.Fatal("manager implicitly evicted retained results")
		}
		for _, task := range handles {
			status := task.Status()
			if err := manager.Release(owner, status.ID, status.Token); err != nil {
				t.Fatal(err)
			}
			if task.Status().State != access.TaskCompleted || string(task.Status().Result) != "true" {
				t.Fatal("release changed existing handle")
			}
		}
		if len(manager.Snapshot()) != 0 {
			t.Fatal("release retained ledger entries")
		}
	}
}

func TestTaskManagerReleaseRequiresMatchingTerminalTask(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	owner := identity("release")
	activate(t, policy, owner)
	manager := access.NewTaskManager(policy)
	requireAccessCode(t, manager.Release(owner, "job", "token"), contract.NotReady)
	task, err := manager.Create(t.Context(), owner, "job", "token")
	if err != nil {
		t.Fatal(err)
	}
	requireAccessCode(t, manager.Release(owner, "job", "wrong-token"), contract.Denied)
	requireAccessCode(t, manager.Release(identity("other"), "job", "token"), contract.NotReady)
	requireAccessCode(t, manager.Release(owner, "job", "token"), contract.Conflict)
	if err := task.Start(); err != nil {
		t.Fatal(err)
	}
	requireAccessCode(t, manager.Release(owner, "job", "token"), contract.Conflict)
	if got := manager.Snapshot(); len(got) != 1 || got[0].State != access.TaskRunning {
		t.Fatalf("release removed active task: %#v", got)
	}
	if err := task.Finish(access.TaskCompleted, nil, nil); err != nil {
		t.Fatal(err)
	}
	requireAccessCode(t, manager.Release(owner, "job", "wrong-token"), contract.Denied)
	if err := manager.Release(owner, "job", "token"); err != nil {
		t.Fatal(err)
	}
	requireAccessCode(t, manager.Release(owner, "job", "token"), contract.NotReady)
	if len(manager.Snapshot()) != 0 {
		t.Fatal("release retained task")
	}
}

func TestTransferredTaskReleasePreservesHandleAndRecoversQuota(t *testing.T) {
	t.Parallel()
	policy := access.NewPolicy()
	manager := access.NewTaskManager(policy)
	owner := identity("release")
	activate(t, policy, owner)
	task, err := manager.Create(t.Context(), owner, "job", "test-generation")
	if err != nil {
		t.Fatal(err)
	}
	if err = task.Transfer(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = task.Release(); err == nil {
		t.Fatal("released active work")
	}
	if err = policy.Revoke(owner); err != nil {
		t.Fatal(err)
	}
	if err = task.Finish(access.TaskCompleted, []byte(`{"ok":true}`), nil); err != nil {
		t.Fatal(err)
	}
	if err = manager.Release(owner, "job", "test-generation"); err != nil {
		t.Fatal(err)
	}
	if err = task.Release(); err != nil {
		t.Fatal(err)
	}
	if len(manager.Snapshot()) != 0 || task.Status().State != access.TaskCompleted || string(task.Status().Result) != `{"ok":true}` {
		t.Fatal("release lost handle or retained ledger")
	}
}

func TestReleasedTaskIDRejectsOldGeneration(t *testing.T) {
	t.Parallel()
	for _, transferred := range []bool{false, true} {
		t.Run(fmt.Sprintf("transferred-%t", transferred), func(t *testing.T) {
			t.Parallel()
			policy := access.NewPolicy()
			owner := identity("first")
			activate(t, policy, owner)
			manager := access.NewTaskManager(policy)
			old, err := manager.Create(t.Context(), owner, "job", "old")
			if err != nil {
				t.Fatal(err)
			}
			if transferred {
				if err := old.Transfer(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if err := old.Finish(access.TaskCompleted, json.RawMessage(`"original"`), nil); err != nil {
				t.Fatal(err)
			}
			if err := old.Release(); err != nil {
				t.Fatal(err)
			}
			replacement, err := manager.Create(t.Context(), owner, "job", "new")
			if err != nil {
				t.Fatal(err)
			}
			_, err = manager.Create(t.Context(), owner, "job", "old")
			requireAccessCode(t, err, contract.Conflict)
			for _, state := range []access.TaskState{access.TaskCompleted, access.TaskCanceled} {
				requireAccessCode(t, manager.Finish(owner, "job", "old", state, json.RawMessage(`"stale"`), nil), contract.Denied)
			}
			requireAccessCode(t, manager.Release(owner, "job", "old"), contract.Denied)
			requireAccessCode(t, old.Start(), contract.Conflict)
			requireAccessCode(t, old.Cancel(), contract.Conflict)
			requireAccessCode(t, old.Finish(access.TaskCompleted, json.RawMessage(`"late"`), nil), contract.Conflict)
			if err := old.Release(); err != nil {
				t.Fatal(err)
			}
			if replacement.Status().State != access.TaskQueued {
				t.Fatal(replacement.Status())
			}
			if err := manager.Finish(owner, "job", "new", access.TaskCompleted, json.RawMessage(`"current"`), nil); err != nil {
				t.Fatal(err)
			}
			requireAccessCode(t, manager.Release(owner, "job", "old"), contract.Denied)
			if err := old.Release(); err != nil {
				t.Fatal(err)
			}
			if got := manager.Snapshot(); len(got) != 1 || got[0].Token != "new" || string(got[0].Result) != `"current"` {
				t.Fatalf("old token or handle changed replacement: %#v", got)
			}
			if old.Status().State != access.TaskCompleted || string(old.Status().Result) != `"original"` {
				t.Fatal("ID reuse changed old terminal handle")
			}
		})
	}
}
