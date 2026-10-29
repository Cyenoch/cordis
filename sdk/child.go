package sdk

import (
	"context"
	"encoding/json"
	"slices"
	"sync"

	"github.com/Cyenoch/cordis/contract"
)

type childInstance struct {
	client *Client
	id     string
	mu     sync.Mutex
	status contract.Status
}

func (c *Client) CreateChild(ctx context.Context, child contract.Child) (contract.ChildInstance, error) {
	instance := &childInstance{client: c, id: child.ID}
	if err := instance.invoke(ctx, contract.ChildRequest{Action: "create", Child: child}, &instance.status); err != nil {
		return nil, err
	}
	return instance, nil
}

func (i *childInstance) invoke(ctx context.Context, request contract.ChildRequest, target any) error {
	request.ID = i.id
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	response, err := i.client.endpoint.Call(ctx, "child", data)
	if err != nil {
		return err
	}
	return contract.Decode(response, target)
}

// Status is the last acknowledged status. Refresh obtains a current host snapshot.
func (i *childInstance) Status() contract.Status {
	i.mu.Lock()
	defer i.mu.Unlock()
	status := i.status
	status.Waiting = slices.Clone(status.Waiting)
	for n := range status.Waiting {
		status.Waiting[n].Visible = slices.Clone(status.Waiting[n].Visible)
		if requirement := status.Waiting[n].Requirement; requirement != nil {
			copy := *requirement
			status.Waiting[n].Requirement = &copy
		}
	}
	status.Diagnostics = slices.Clone(status.Diagnostics)
	return status
}

func (i *childInstance) Refresh(ctx context.Context) (contract.Status, error) {
	err := i.control(ctx, "status")
	return i.Status(), err
}

func (i *childInstance) control(ctx context.Context, action string) error {
	var status contract.Status
	if err := i.invoke(ctx, contract.ChildRequest{Action: action}, &status); err != nil {
		return err
	}
	i.mu.Lock()
	i.status = status
	i.mu.Unlock()
	return nil
}

func (i *childInstance) Wait(ctx context.Context) error { return i.control(ctx, "wait") }
func (i *childInstance) Stop(ctx context.Context) error { return i.control(ctx, "stop") }
func (i *childInstance) Update(ctx context.Context, config json.RawMessage) (contract.UpdateResult, error) {
	var result contract.UpdateResult
	err := i.invoke(ctx, contract.ChildRequest{Action: "update", Child: contract.Child{Config: config}}, &result)
	return result, err
}
