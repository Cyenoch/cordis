package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/Cyenoch/cordis/contract"
)

type TaskState string

const (
	TaskQueued      TaskState = "queued"
	TaskRunning     TaskState = "running"
	TaskCompleted   TaskState = "completed"
	TaskFailed      TaskState = "failed"
	TaskCanceled    TaskState = "canceled"
	TaskInterrupted TaskState = "interrupted"
)

// Limits include queued, running and terminal tasks retained for one activation.
const (
	MaxTasksPerActivation           = 256
	MaxTaskResultBytes              = 1 << 20
	MaxTaskResultBytesPerActivation = 8 << 20
)

var ErrTaskQuota = errors.New("task quota exceeded")

func terminal(state TaskState) bool {
	switch state {
	case TaskCompleted, TaskFailed, TaskCanceled, TaskInterrupted:
		return true
	default:
		return false
	}
}

type TaskStatus struct {
	ID        string            `json:"id"`
	Owner     contract.Identity `json:"owner"`
	HostOwned bool              `json:"hostOwned"`
	State     TaskState         `json:"state"`
	Result    json.RawMessage   `json:"result,omitempty"`
	// Failed tasks expose only a stable code. Raw failures remain available to the host via Err.
	Failure contract.Code `json:"failure,omitempty"`
}

type taskKey struct {
	owner contract.Identity
	id    string
}

type taskUsage struct {
	count       int
	resultBytes int
}

type TaskManager struct {
	policy *Policy
	tasks  map[taskKey]*Task
	usage  map[contract.Identity]*taskUsage
}

func NewTaskManager(policy *Policy) *TaskManager {
	manager := &TaskManager{
		policy: policy, tasks: make(map[taskKey]*Task), usage: make(map[contract.Identity]*taskUsage),
	}
	if policy != nil {
		policy.mu.Lock()
		if policy.tasks == nil {
			policy.tasks = make(map[*TaskManager]struct{})
		}
		policy.tasks[manager] = struct{}{}
		policy.mu.Unlock()
	}
	return manager
}

// Task is a trusted host handle; never hand it to a guest. A transferred task can
// only be finished through this handle, not through activation-owned messages.
type Task struct {
	manager    *TaskManager
	status     TaskStatus
	ctx        context.Context
	parent     context.Context
	cancel     context.CancelFunc
	stopParent func() bool
	done       chan struct{}
	err        error
}

func (m *TaskManager) Create(ctx context.Context, owner contract.Identity, id string) (*Task, error) {
	if m.policy == nil || ctx == nil || id == "" {
		return nil, contract.Fail(contract.Invalid, "invalid task request")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}
	p := m.policy
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.activeLocked(owner); err != nil {
		return nil, err
	}
	key := taskKey{owner: owner, id: id}
	if _, exists := m.tasks[key]; exists {
		return nil, contract.Fail(contract.Conflict, "task already exists")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}
	usage := m.usage[owner]
	if usage == nil {
		usage = &taskUsage{}
		m.usage[owner] = usage
	}
	if usage.count >= MaxTasksPerActivation {
		return nil, ErrTaskQuota
	}
	// Parent cancellation is forwarded through a replaceable callback, allowing
	// explicit host transfer without retaining the plugin's cancellation lineage.
	taskCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	task := &Task{
		manager: m, status: TaskStatus{ID: id, Owner: owner, State: TaskQueued},
		ctx: taskCtx, parent: ctx, cancel: cancel, done: make(chan struct{}),
	}
	m.tasks[key] = task
	usage.count++
	task.stopParent = context.AfterFunc(ctx, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if !task.status.HostOwned && !terminal(task.status.State) {
			task.finishLocked(TaskCanceled, nil, ctx.Err())
		}
	})
	return task, nil
}

func (t *Task) Context() context.Context { return t.ctx }
func (t *Task) Done() <-chan struct{}    { return t.done }

func (t *Task) Status() TaskStatus {
	t.manager.policy.mu.Lock()
	defer t.manager.policy.mu.Unlock()
	status := t.status
	status.Result = append(json.RawMessage(nil), status.Result...)
	return status
}

func (t *Task) Err() error {
	t.manager.policy.mu.Lock()
	defer t.manager.policy.mu.Unlock()
	return t.err
}

func (t *Task) Start() error {
	t.manager.policy.mu.Lock()
	defer t.manager.policy.mu.Unlock()
	if t.status.State != TaskQueued {
		return contract.Fail(contract.Conflict, "task cannot start")
	}
	t.status.State = TaskRunning
	return nil
}

func validateFinish(state TaskState, result json.RawMessage, failure error) error {
	if !terminal(state) {
		return contract.Fail(contract.Invalid, "task needs a terminal state")
	}
	if state == TaskFailed && failure == nil {
		return contract.Fail(contract.Invalid, "failed task needs an error")
	}
	if state == TaskCompleted && failure != nil {
		return contract.Fail(contract.Invalid, "completed task cannot have an error")
	}
	if len(result) > MaxTaskResultBytes {
		return ErrTaskQuota
	}
	if len(result) > 0 {
		if err := contract.CheckJSON(result, 32, 10000); err != nil {
			return contract.Fail(contract.Invalid, "invalid task result")
		}
	}
	return nil
}

func (t *Task) Finish(state TaskState, result json.RawMessage, failure error) error {
	if err := validateFinish(state, result, failure); err != nil {
		return err
	}
	t.manager.policy.mu.Lock()
	defer t.manager.policy.mu.Unlock()
	if terminal(t.status.State) {
		return contract.Fail(contract.Conflict, "task already terminal")
	}
	if !t.status.HostOwned {
		if _, err := t.manager.policy.activeLocked(t.status.Owner); err != nil {
			return err
		}
	}
	if err := t.checkParentLocked(); err != nil {
		return err
	}
	if err := t.reserveResultLocked(result); err != nil {
		return err
	}
	t.finishLocked(state, result, failure)
	return nil
}

// Cancellation is checked at the commit point, independently of AfterFunc scheduling.
func (t *Task) checkParentLocked() error {
	if err := t.parent.Err(); err != nil {
		t.finishLocked(TaskCanceled, nil, err)
		return fmt.Errorf("task parent canceled: %w", err)
	}
	return nil
}

func (t *Task) reserveResultLocked(result json.RawMessage) error {
	usage := t.manager.usage[t.status.Owner]
	if len(result) > MaxTaskResultBytesPerActivation-usage.resultBytes {
		return ErrTaskQuota
	}
	usage.resultBytes += len(result)
	return nil
}

func (t *Task) finishLocked(state TaskState, result json.RawMessage, failure error) {
	t.status.State = state
	t.status.Result = append(json.RawMessage(nil), result...)
	if failure != nil {
		t.status.Failure = contract.PluginFailed
	}
	t.err = failure
	if t.stopParent != nil {
		t.stopParent()
		t.stopParent = nil
	}
	t.parent = nil
	t.cancel()
	close(t.done)
}

func (t *Task) Cancel() error { return t.Finish(TaskCanceled, nil, context.Canceled) }

// Transfer explicitly moves cancellation authority to the host. It neither
// commits a business transaction nor stores a durable checkpoint.
func (t *Task) Transfer(hostCtx context.Context) error {
	if hostCtx == nil {
		return contract.Fail(contract.Invalid, "missing host task context")
	}
	if err := hostCtx.Err(); err != nil {
		return fmt.Errorf("transfer task: %w", err)
	}
	p := t.manager.policy
	p.mu.Lock()
	defer p.mu.Unlock()
	if terminal(t.status.State) || t.status.HostOwned {
		return contract.Fail(contract.Conflict, "task cannot transfer")
	}
	if _, err := p.activeLocked(t.status.Owner); err != nil {
		return err
	}
	if err := t.checkParentLocked(); err != nil {
		return err
	}
	if err := hostCtx.Err(); err != nil {
		return fmt.Errorf("transfer task: %w", err)
	}
	if t.stopParent != nil {
		t.stopParent()
	}
	t.status.HostOwned = true
	t.parent = hostCtx
	t.stopParent = context.AfterFunc(hostCtx, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if !terminal(t.status.State) {
			t.finishLocked(TaskCanceled, nil, hostCtx.Err())
		}
	})
	return nil
}

// Finish is the activation-bound guest completion path. It rejects stale owners
// and does not let a guest control tasks already transferred to the host.
func (m *TaskManager) Finish(
	owner contract.Identity, id string, state TaskState, result json.RawMessage, failure error,
) error {
	if m.policy == nil {
		return contract.Fail(contract.Invalid, "task manager not configured")
	}
	if err := validateFinish(state, result, failure); err != nil {
		return err
	}
	m.policy.mu.Lock()
	defer m.policy.mu.Unlock()
	if _, err := m.policy.activeLocked(owner); err != nil {
		return err
	}
	task := m.tasks[taskKey{owner: owner, id: id}]
	if task == nil || task.status.HostOwned {
		return contract.Fail(contract.Denied, "task not activation-owned")
	}
	if terminal(task.status.State) {
		return contract.Fail(contract.Conflict, "task already terminal")
	}
	if err := task.checkParentLocked(); err != nil {
		return err
	}
	if err := task.reserveResultLocked(result); err != nil {
		return err
	}
	task.finishLocked(state, result, failure)
	return nil
}

func (m *TaskManager) revokeLocked(owner contract.Identity) {
	usage := m.usage[owner]
	for key, task := range m.tasks {
		if task.status.Owner != owner || task.status.HostOwned {
			continue
		}
		if !terminal(task.status.State) {
			task.finishLocked(TaskInterrupted, nil, context.Canceled)
		}
		usage.count--
		usage.resultBytes -= len(task.status.Result)
		delete(m.tasks, key)
	}
	if usage != nil && usage.count == 0 {
		delete(m.usage, owner)
	}
}

func (m *TaskManager) Snapshot() []TaskStatus {
	if m.policy == nil {
		return []TaskStatus{}
	}
	m.policy.mu.Lock()
	defer m.policy.mu.Unlock()
	result := make([]TaskStatus, 0, len(m.tasks))
	for _, task := range m.tasks {
		status := task.status
		status.Result = append(json.RawMessage(nil), status.Result...)
		result = append(result, status)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Owner.Activation != result[j].Owner.Activation {
			return result[i].Owner.Activation < result[j].Owner.Activation
		}
		return result[i].ID < result[j].ID
	})
	return result
}
