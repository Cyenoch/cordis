package core

func (e *Engine) Missing(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.instances[id] == nil
}
