package contract

import (
	"context"
	"encoding/json"
)

// Instance is an independently configurable, stoppable plugin entry.
type Instance interface {
	Status() Status
	Wait(context.Context) error
	Update(context.Context, json.RawMessage) (UpdateResult, error)
	Stop(context.Context) error
}

// Event carries an explicitly scoped event across a session.
type Event struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	Mode  Dispatch        `json:"mode"`
}
