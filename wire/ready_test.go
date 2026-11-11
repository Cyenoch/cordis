package wire

// past marks an endpoint as past its handshake, for tests of the frame
// layer that do not perform one.
func past(e *Endpoint) *Endpoint {
	e.mu.Lock()
	e.ready = true
	e.mu.Unlock()
	return e
}
