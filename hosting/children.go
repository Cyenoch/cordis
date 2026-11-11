package hosting

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/Cyenoch/cordis/contract"
)

type ownedChild struct {
	owner    contract.Identity
	instance *hostInstance
	entry    contract.Entry
	ref      contract.PackageRef
}

// A parent may own optional features, not an unbounded process spawn loop.
const maxChildrenPerActivation = 256

// Reserve across preflight, durable intent and registration without holding a
// mutex during storage I/O or blocking activation retirement.
func (h *Host) reserveEntry(id string, dynamic bool) (func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, contract.Fail(contract.NotReady, "host is closed")
	}
	if _, owned := h.dynamic[id]; owned {
		return nil, contract.Fail(contract.Conflict, "entry id is still owned by a dynamic child")
	}
	if h.admissions[id] || dynamic && h.controls[id] {
		return nil, contract.Fail(contract.Conflict, "entry registration is already in progress")
	}
	h.admissions[id] = true
	return func() {
		h.mu.Lock()
		delete(h.admissions, id)
		h.mu.Unlock()
	}, nil
}

// createChild runs another definition of the parent's package as a kernel
// child of the parent's activation. It is not persisted and ends with that
// activation.
func (h *Host) createChild(ctx context.Context, parent *run, child contract.Child) (*hostInstance, error) {
	owner := parent.identity
	if child.ID == "" || child.Definition == "" || strings.Contains(child.ID, "..") || len(owner.Entry)+1+len(child.ID) > 200 {
		return nil, contract.Fail(contract.Invalid, "invalid child id or definition")
	}
	for _, r := range child.ID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_') {
			return nil, contract.Fail(contract.Invalid, "invalid child id")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h.childMu.Lock()
	defer h.childMu.Unlock()
	h.mu.Lock()
	count := 0
	for _, child := range h.dynamic {
		if child.owner == owner {
			count++
		}
	}
	h.mu.Unlock()
	if count >= maxChildrenPerActivation {
		return nil, contract.Fail(contract.Denied, "child instance quota exceeded")
	}
	entry := contract.Entry{ID: owner.Entry + "." + child.ID, Parent: owner.Entry, Package: owner.Package,
		Definition: child.Definition, Config: child.Config, Enabled: true, Isolate: slices.Clone(child.Isolate)}
	instance, err := h.enableEntry(ctx, entry, parent, nil)
	if err != nil {
		return nil, err
	}
	return instance.(*hostInstance), nil
}

// forgetChildren drops the records of the children of an activation. The
// kernel has stopped them by the time the activation cleans up.
func (h *Host) forgetChildren(owner contract.Identity) error {
	h.childMu.Lock()
	defer h.childMu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, child := range h.dynamic {
		if child.owner == owner {
			delete(h.dynamic, id)
		}
	}
	return nil
}

func (h *Host) handleChild(ctx context.Context, parent *run, input json.RawMessage) (json.RawMessage, error) {
	var request contract.ChildRequest
	if err := contract.Decode(input, &request); err != nil {
		return nil, err
	}
	if request.Action == "create" {
		instance, err := h.createChild(ctx, parent, request.Child)
		if err != nil {
			return nil, err
		}
		return json.Marshal(instance.Status())
	}
	h.mu.Lock()
	child, exists := h.dynamic[parent.identity.Entry+"."+request.ID]
	h.mu.Unlock()
	if !exists || child.owner != parent.identity {
		return nil, contract.Fail(contract.NotReady, "child is not owned by this activation")
	}
	var err error
	switch request.Action {
	case "status":
	case "wait":
		err = child.instance.Wait(ctx)
	case "stop":
		err = child.instance.Stop(ctx)
	case "update":
		result, updateErr := child.instance.Update(ctx, request.Child.Config)
		if updateErr != nil {
			return nil, updateErr
		}
		return json.Marshal(result)
	default:
		return nil, contract.Fail(contract.Invalid, "unknown child action")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(child.instance.Status())
}
