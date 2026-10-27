package contract

import (
	"context"
	"encoding/json"
)

// Child names a same-package definition owned by the current activation.
type Child struct {
	ID         string          `json:"id"`
	Definition string          `json:"definition"`
	Config     json.RawMessage `json:"config"`
}

type ChildScope interface {
	CreateChild(context.Context, Child) (ChildInstance, error)
}

// ChildInstance exposes an explicit refresh because remote status is a snapshot.
type ChildInstance interface {
	Instance
	Refresh(context.Context) (Status, error)
}

// CreateChild registers without waiting for activation: a parent must publish
// before any of its children can start. Wait only after initialization returns.
func CreateChild(ctx context.Context, scope Scope, child Child) (ChildInstance, error) {
	creator, ok := scope.(ChildScope)
	if !ok {
		return nil, Fail(Incompatible, "scope does not support child instances")
	}
	return creator.CreateChild(ctx, child)
}

type ChildRequest struct {
	Child  Child  `json:"child"`
	ID     string `json:"id"`
	Action string `json:"action"`
}
