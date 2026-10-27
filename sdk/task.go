package sdk

import (
	"context"
	"encoding/json"

	"github.com/Cyenoch/cordis/contract"
)

type Task struct {
	client *Client
	id     string
	token  string
	ctx    context.Context
	cancel context.CancelFunc
}

func (c *Client) BeginTask(ctx context.Context, id string) (contract.Task, error) {
	if id == "" {
		return nil, contract.Fail(contract.Invalid, "task id required")
	}
	c.mu.Lock()
	if c.tasks == nil {
		c.tasks = map[string]*Task{}
	}
	if c.tasks[id] != nil || c.retiring || c.closed {
		c.mu.Unlock()
		return nil, contract.Fail(contract.Conflict, "task already exists or client closed")
	}
	taskCtx, cancel := context.WithCancel(c.ctx)
	task := &Task{client: c, id: id, token: contract.NewID(), ctx: taskCtx, cancel: cancel}
	c.tasks[id] = task
	c.mu.Unlock()
	data, err := json.Marshal(contract.TaskRequest{ID: id, Token: task.token})
	if err == nil {
		_, err = c.endpoint.Call(ctx, "task.begin", data)
	}
	if err != nil {
		cancel()
		c.mu.Lock()
		delete(c.tasks, id)
		c.mu.Unlock()
		return nil, err
	}
	return task, nil
}

func (t *Task) Context() context.Context { return t.ctx }
func (t *Task) Release(ctx context.Context) error {
	data, err := json.Marshal(contract.TaskRequest{ID: t.id, Token: t.token})
	if err != nil {
		return err
	}
	_, err = t.client.endpoint.Call(ctx, "task.release", data)
	return err
}
func (t *Task) Finish(ctx context.Context, state string, result json.RawMessage, failure contract.Code) error {
	data, err := json.Marshal(contract.TaskRequest{ID: t.id, Token: t.token, State: state, Result: result, Failure: failure})
	if err != nil {
		return err
	}
	if _, err := t.client.endpoint.Call(ctx, "task.finish", data); err != nil {
		return err
	}
	t.cancel()
	t.client.mu.Lock()
	delete(t.client.tasks, t.id)
	t.client.mu.Unlock()
	return nil
}

func (c *Client) cancelTask(input json.RawMessage) (json.RawMessage, error) {
	var request contract.TaskRequest
	if err := contract.Decode(input, &request); err != nil {
		return nil, err
	}
	c.mu.Lock()
	task := c.tasks[request.ID]
	c.mu.Unlock()
	if task == nil || task.token != request.Token {
		return nil, contract.Fail(contract.NotReady, "task not found")
	}
	task.cancel()
	return json.RawMessage(`{}`), nil
}
