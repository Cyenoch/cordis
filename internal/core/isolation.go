package core

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/Cyenoch/cordis/contract"
)

type serviceKey struct {
	boundary *instance
	key      string
}

func serviceAddress(i *instance, key string) serviceKey {
	for parent := i; parent != nil; parent = parent.parent {
		if slices.Contains(parent.entry.Isolate, key) {
			return serviceKey{boundary: parent, key: key}
		}
	}
	return serviceKey{key: key}
}

type caller struct {
	engine *Engine
	target *instance
}

// Caller captures a registration's service view, not its activation authority.
func (e *Engine) Caller(id string) (contract.Caller, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	i := e.instances[id]
	if e.closed || i == nil || i.removing {
		return nil, contract.Fail(contract.NotReady, "entry is unavailable")
	}
	return &caller{engine: e, target: i}, nil
}

func (c *caller) Call(ctx context.Context, required contract.Requirement, input json.RawMessage) (json.RawMessage, error) {
	e := c.engine
	e.mu.Lock()
	if e.closed || c.target.removing || e.instances[c.target.entry.ID] != c.target {
		e.mu.Unlock()
		return nil, contract.Fail(contract.NotReady, "caller entry has been removed")
	}
	p := e.resolveLocked(c.target, required)
	if p == nil {
		e.mu.Unlock()
		return nil, contract.Fail(contract.NotReady, "service is unavailable in entry scope")
	}
	return e.callLocked(ctx, p, input, false)
}
