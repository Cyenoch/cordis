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
	host  *Host
	realm string
}

// RegisterDefinition makes a builtin definition available to same-package children.
func (h *Host) RegisterDefinition(ctx context.Context, packageID string, definition contract.Definition) error {
	if packageID == "" || definition.ID == "" || definition.Start == nil {
		return contract.Fail(contract.Invalid, "package, definition id and start are required")
	}
	if err := h.lockOperation(ctx); err != nil {
		return err
	}
	defer h.unlockOperation()
	h.mu.Lock()
	defer h.mu.Unlock()
	key := packageID + "/" + definition.ID
	if h.closed || h.definitions[key].Start != nil {
		return contract.Fail(contract.Conflict, "definition already registered or host closed")
	}
	h.definitions[key] = definition
	return nil
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
	if h.admissions == nil {
		h.admissions = map[string]bool{}
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
		Definition: child.Definition, Realm: s.realm, Config: child.Config, Enabled: true}
	if len(entry.Config) == 0 {
		entry.Config = json.RawMessage(`{}`)
	}
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
	h.mu.Lock()
	definition := h.definitions[owner.Package+"/"+child.Definition]
	h.mu.Unlock()
	if definition.Start == nil {
		return nil, contract.Fail(contract.Invalid, "child definition is not registered")
	}
	original := definition.Start
	definition.Start = func(ctx context.Context, scope contract.Scope) error {
		return original(ctx, &hostScope{Scope: scope, host: h, realm: entry.Realm})
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
	case "retry":
		err = child.instance.Retry(ctx)
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
