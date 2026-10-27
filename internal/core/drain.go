package core

func (e *Engine) Drained() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}
