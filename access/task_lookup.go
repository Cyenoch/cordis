package access

import "github.com/Cyenoch/cordis/contract"

// Lookup returns a trusted host handle for cancellation or explicit transfer.
// Guest messages must use the activation-bound Finish method instead.
func (m *TaskManager) Lookup(owner contract.Identity, id string) (*Task, bool) {
	if m.policy == nil {
		return nil, false
	}
	m.policy.mu.Lock()
	defer m.policy.mu.Unlock()
	task, ok := m.tasks[taskKey{owner: owner, id: id}]
	return task, ok
}
