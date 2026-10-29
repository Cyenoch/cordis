package cordis

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Cyenoch/cordis/contract"
)

type ownedChild struct {
	owner    contract.Identity
	instance contract.Instance
}

type hostScope struct {
	contract.Scope
	host        *Host
	definitions []contract.Definition
}

func (s *hostScope) Go(work func(context.Context) error) error {
	return contract.Go(s.Scope, work)
}

func (s *hostScope) OnPublish(hook func(context.Context) error) error {
	return s.Scope.(contract.ActivationScope).OnPublish(hook)
}

func (s *hostScope) OnRetire(hook func(context.Context) error) error {
	return s.Scope.(contract.ActivationScope).OnRetire(hook)
}

type localChild struct{ contract.Instance }

func (i localChild) Refresh(ctx context.Context) (contract.Status, error) {
	if err := ctx.Err(); err != nil {
		return contract.Status{}, err
	}
	return i.Status(), nil
}

// A parent may own optional features, not an unbounded process spawn loop.
const maxChildrenPerActivation = 256

// Reserve across preflight, durable intent and registration without holding a
// mutex during storage I/O or blocking activation retirement.
func (h *Host) reserveEntry(id string) (func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, contract.Fail(contract.NotReady, "host is closed")
	}
	if h.admissions[id] {
		return nil, contract.Fail(contract.Conflict, "entry registration is already in progress")
	}
	h.admissions[id] = true
	return func() {
		h.mu.Lock()
		delete(h.admissions, id)
		h.mu.Unlock()
	}, nil
}

func (s *hostScope) CreateChild(ctx context.Context, child contract.Child) (contract.ChildInstance, error) {
	if child.ID == "" || child.Definition == "" || strings.Contains(child.ID, "..") || len(s.Identity().Entry)+1+len(child.ID) > 200 {
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
	h := s.host
	h.childMu.Lock()
	defer h.childMu.Unlock()
	owner := s.Identity()
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
		Definition: child.Definition, Config: child.Config, Enabled: true}
	if owner.Digest != "builtin" {
		instance, err := h.enableEntry(ctx, entry, &owner)
		if err != nil {
			return nil, err
		}
		return localChild{instance}, nil
	}
	release, err := h.reserveEntry(entry.ID)
	if err != nil {
		return nil, err
	}
	defer release()
	var definition contract.Definition
	found := false
	for _, candidate := range s.definitions {
		if candidate.ID == child.Definition {
			if found {
				return nil, contract.Fail(contract.Conflict, "duplicate child definition: "+child.Definition)
			}
			definition = candidate
			found = true
		}
	}
	if definition.Start == nil {
		return nil, contract.Fail(contract.Invalid, "child definition is not registered")
	}
	original := definition
	definition.Start = func(ctx context.Context, scope contract.Scope) error {
		return original.Start(ctx, &hostScope{Scope: scope, host: h, definitions: original.Children})
	}
	instance, err := h.engine.AddOwned(entry, definition, contract.Identity{Package: owner.Package, Digest: "builtin"}, owner)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.dynamic[entry.ID] = ownedChild{owner: owner, instance: instance}
	h.mu.Unlock()
	return localChild{instance}, nil
}

func (h *Host) removeChildren(owner contract.Identity) error {
	// Do not retain childMu while waiting: a child's own retirement also removes
	// its descendants. AddOwned atomically rejects creation after parent retirement.
	h.childMu.Lock()
	h.mu.Lock()
	children := map[string]ownedChild{}
	for id, child := range h.dynamic {
		if child.owner == owner {
			children[id] = child
			delete(h.dynamic, id)
		}
	}
	h.mu.Unlock()
	h.childMu.Unlock()
	var result error
	for id, child := range children {
		result = errors.Join(result, child.instance.Stop(context.Background()))
		result = errors.Join(result, h.engine.Remove(context.Background(), id))
	}
	return result
}

func (h *Host) registerBuiltinParent(ctx context.Context, id string) error {
	h.mu.Lock()
	_, builtin := h.builtins[id]
	h.mu.Unlock()
	if builtin {
		return h.store.RegisterParent(ctx, id)
	}
	return nil
}

func (h *Host) handleChild(ctx context.Context, scope contract.Scope, input json.RawMessage) (json.RawMessage, error) {
	var request contract.ChildRequest
	if err := contract.Decode(input, &request); err != nil {
		return nil, err
	}
	if request.Action == "create" {
		instance, err := contract.CreateChild(ctx, scope, request.Child)
		if err != nil {
			return nil, err
		}
		return json.Marshal(instance.Status())
	}
	h.mu.Lock()
	child, exists := h.dynamic[scope.Identity().Entry+"."+request.ID]
	h.mu.Unlock()
	if !exists || child.owner != scope.Identity() {
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
