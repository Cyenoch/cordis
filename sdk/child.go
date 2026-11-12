package sdk

import (
	"context"
	"encoding/json"
	"slices"
	"sync"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

// CreateChild asks the host to run another definition of the same package
// as a child of the activation that s runs in. The child has its own
// process and ends with the activation. CreateChild does not wait for the
// child to start: a child can only start after its parent has.
func CreateChild(ctx context.Context, s *cordis.Scope, child contract.Child) (contract.ChildInstance, error) {
	g, ok := guestOf(s)
	if !ok {
		return nil, contract.Fail(contract.Incompatible, "child instances need a native plugin process")
	}
	instance := &childInstance{guest: g, id: child.ID}
	if err := instance.invoke(ctx, contract.ChildRequest{Action: "create", Child: child}, &instance.status); err != nil {
		return nil, err
	}
	return instance, nil
}

type childInstance struct {
	guest  *guest
	id     string
	mu     sync.Mutex
	status contract.Status
}

func (i *childInstance) invoke(ctx context.Context, request contract.ChildRequest, target any) error {
	request.ID = i.id
	return i.guest.request(ctx, "child", request, target)
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

// Refresh asks the host for the current status. On failure it returns the last
// acknowledged status with the error.
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

// Wait asks the host to wait for the child as its Wait does, and records the
// status the host answers with.
func (i *childInstance) Wait(ctx context.Context) error { return i.control(ctx, "wait") }

// Stop asks the host to stop the child for good, and records the status the
// host answers with.
func (i *childInstance) Stop(ctx context.Context) error { return i.control(ctx, "stop") }

// Update asks the host to validate and apply config. It leaves the recorded
// status as it is.
func (i *childInstance) Update(ctx context.Context, config json.RawMessage) (contract.UpdateResult, error) {
	var result contract.UpdateResult
	err := i.invoke(ctx, contract.ChildRequest{Action: "update", Child: contract.Child{Config: config}}, &result)
	return result, err
}
