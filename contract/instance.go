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

// Event is a guest's request to run the host's listeners of an event, or
// its handlers of a query.
type Event struct {
	Kind  Kind            `json:"kind"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	Mode  Dispatch        `json:"mode"`
}
