package contract

import (
	"context"
	"encoding/json"
)

// Child names a same-package definition owned by the current activation.
// Isolate starts child-owned boundaries; all other service keys are inherited.
type Child struct {
	ID         string          `json:"id"`
	Definition string          `json:"definition"`
	Config     json.RawMessage `json:"config"`
	Isolate    []string        `json:"isolate,omitempty"`
}

// ChildInstance exposes an explicit refresh because remote status is a snapshot.
type ChildInstance interface {
	Instance
	Refresh(context.Context) (Status, error)
}

type ChildRequest struct {
	Child  Child  `json:"child"`
	ID     string `json:"id"`
	Action string `json:"action"`
}
