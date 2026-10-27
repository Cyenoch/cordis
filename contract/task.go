package contract

import (
	"context"
	"encoding/json"
)

type Task interface {
	Context() context.Context
	Finish(context.Context, string, json.RawMessage, Code) error
	Release(context.Context) error
}

type TaskStarter interface {
	BeginTask(context.Context, string) (Task, error)
}

func BeginTask(ctx context.Context, owner any, id string) (Task, error) {
	starter, ok := owner.(TaskStarter)
	if !ok {
		return nil, Fail(Incompatible, "scope does not support tasks")
	}
	return starter.BeginTask(ctx, id)
}

type TaskRequest struct {
	ID      string          `json:"id"`
	Token   string          `json:"token"`
	State   string          `json:"state,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Failure Code            `json:"failure,omitempty"`
}
