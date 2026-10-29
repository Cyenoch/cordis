package cordis

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
	"github.com/Cyenoch/cordis/packagehost"
	"github.com/Cyenoch/cordis/runtime/native"
	"github.com/Cyenoch/cordis/schema"
)

type externalCleanupKey struct{}

type Options struct {
	Directory  string
	Trust      packagehost.TrustPolicy
	Runtime    contract.Runtime
	FrameBytes int
	Stderr     func([]byte) error
	Grace      time.Duration
}

type Host struct {
	mu         sync.Mutex
	operations chan struct{}
	childMu    sync.Mutex
	engine     *core.Engine
	store      *packagehost.Store
	options    Options
	entries    map[string]*hostInstance
	entrySpecs map[string]contract.Entry
	packages   map[string]contract.PackageRef
	builtins   map[string]contract.Definition
	dynamic    map[string]ownedChild
	admissions map[string]bool
	controls   map[string]bool
	packageOps map[string]bool
	closed     bool
}

func Open(options Options) (*Host, error) {
	if options.Directory == "" {
		return nil, contract.Fail(contract.Invalid, "package directory required")
	}
	store, err := packagehost.Open(options.Directory, options.Trust)
	if err != nil {
		return nil, err
	}
	if options.Runtime == nil {
		options.Runtime = native.New()
	}
	if options.FrameBytes == 0 {
		options.FrameBytes = contract.MaxFrameBytes
	}
	h := &Host{store: store, operations: make(chan struct{}, 1), options: options,
		entries: map[string]*hostInstance{}, entrySpecs: map[string]contract.Entry{},
		packages: map[string]contract.PackageRef{}, builtins: map[string]contract.Definition{},
		dynamic: map[string]ownedChild{}, admissions: map[string]bool{},
		controls: map[string]bool{}, packageOps: map[string]bool{}}
	if h.options.Grace == 0 {
		h.options.Grace = time.Second
	}
	h.engine = core.NewWithHooks(core.Hooks{OnRetire: h.onRetire})
	return h, nil
}

func (h *Host) Store() *packagehost.Store { return h.store }
func (h *Host) lockOperation(ctx context.Context) error {
	select {
	case h.operations <- struct{}{}:
		if err := ctx.Err(); err != nil {
			h.unlockOperation()
			return err
		}
		h.mu.Lock()
		closed := h.closed
		h.mu.Unlock()
		if closed {
			h.unlockOperation()
			return contract.Fail(contract.NotReady, "host is closed")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (h *Host) unlockOperation() { <-h.operations }
func (h *Host) Call(ctx context.Context, required contract.Requirement, input json.RawMessage) (json.RawMessage, error) {
	return h.engine.Call(ctx, required, input)
}
func (h *Host) Emit(ctx context.Context, name string, input json.RawMessage, mode contract.Dispatch) ([]contract.EventResult, error) {
	return h.engine.Emit(ctx, name, input, mode)
}

func (h *Host) onRetire(owner contract.Identity) {
	if err := h.removeChildren(owner); err != nil {
		h.engine.FailActivation(owner, err)
	}
}

func (h *Host) Install(ctx context.Context, archive string) (packagehost.Package, error) {
	return h.store.Install(ctx, archive)
}

func (h *Host) Select(ctx context.Context, ref contract.PackageRef) error {
	if err := h.lockOperation(ctx); err != nil {
		return err
	}
	defer h.unlockOperation()
	if h.packageBusy(ref.ID) {
		return contract.Fail(contract.Conflict, "package operation is in progress")
	}
	options, err := h.changeOptions()
	if err != nil {
		return err
	}
	return h.store.Select(ctx, ref, options)
}

func (h *Host) changeOptions() (packagehost.ChangeOptions, error) {
	entries, err := h.store.Entries()
	if err != nil {
		return packagehost.ChangeOptions{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	options := packagehost.ChangeOptions{InUse: []contract.PackageRef{}, Enabled: []string{}}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Enabled && !seen[entry.Package] {
			options.Enabled = append(options.Enabled, entry.Package)
			seen[entry.Package] = true
		}
	}
	for id, instance := range h.entries {
		if _, builtin := h.builtins[id]; builtin {
			continue
		}
		state := instance.Instance.Status().State
		if state == contract.Stopped || state == contract.Failed {
			continue
		}
		ref := h.packages[id]
		options.InUse = append(options.InUse, ref)
	}
	return options, nil
}

func (h *Host) Enable(ctx context.Context, entry contract.Entry) (contract.Instance, error) {
	if err := h.lockOperation(ctx); err != nil {
		return nil, err
	}
	defer h.unlockOperation()
	if h.controlBusy(entry.ID, entry.Parent) {
		return nil, contract.Fail(contract.Conflict, "entry ownership tree has an operation in progress")
	}
	if h.packageBusy(entry.Package) {
		return nil, contract.Fail(contract.Conflict, "package operation is in progress")
	}
	return h.enable(ctx, entry)
}

func (h *Host) enable(ctx context.Context, entry contract.Entry) (contract.Instance, error) {
	return h.enableEntry(ctx, entry, nil)
}

func (h *Host) enableEntry(ctx context.Context, entry contract.Entry, owner *contract.Identity) (contract.Instance, error) {
	release, err := h.reserveEntry(entry.ID)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h.mu.Lock()
	_, exists := h.entries[entry.ID]
	h.mu.Unlock()
	if exists {
		return nil, contract.Fail(contract.Conflict, "entry already exists; disable before enabling")
	}
	pkg, err := h.store.Selected(entry.Package)
	if err != nil {
		return nil, err
	}
	if owner != nil && pkg.Ref.Digest != owner.Digest {
		return nil, contract.Fail(contract.Conflict, "child package selection differs from parent activation")
	}
	diagnostics, err := h.store.Preflight(pkg.Ref)
	if err != nil {
		return nil, err
	}
	if entry.Definition == "" {
		entry.Definition = pkg.Manifest.DefaultDefinition
	}
	var spec contract.DefinitionSpec
	found := false
	for _, definition := range pkg.Manifest.Definitions {
		if definition.ID == entry.Definition {
			spec = definition
			found = true
			break
		}
	}
	if !found {
		return nil, contract.Fail(contract.Invalid, "unknown package definition")
	}
	adapter := h.options.Runtime
	if adapter == nil {
		return nil, contract.Fail(contract.Denied, "runtime not enabled by host")
	}
	entry.Enabled = true
	if len(entry.Config) == 0 {
		entry.Config = json.RawMessage(`{}`)
	}
	definition := contract.Definition{ID: spec.ID, Requires: spec.Requirements(), Provides: spec.Provides,
		Validate: func(config json.RawMessage) error {
			return schema.Validate(spec.ConfigSchema, config)
		}}
	definition.Start = func(startCtx context.Context, scope contract.Scope) error {
		return h.startExternal(startCtx, scope, pkg, entry, adapter)
	}
	var commitErr error
	identity := contract.Identity{Package: pkg.Ref.ID, Entry: entry.ID, Digest: pkg.Ref.Digest}
	var instance contract.Instance
	if owner == nil {
		instance, err = h.engine.AddWithCommit(entry, definition, identity, func() error {
			if err := h.registerBuiltinParent(ctx, entry.Parent); err != nil {
				return err
			}
			commitErr = h.store.SaveEntry(ctx, entry)
			if commitErr != nil && !catalogCommitted(commitErr) {
				return commitErr
			}
			return nil
		})
	} else {
		instance, err = h.engine.AddOwned(entry, definition, identity, *owner)
	}
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	if owner != nil {
		h.dynamic[entry.ID] = ownedChild{owner: *owner, instance: instance}
		h.mu.Unlock()
		return instance, nil
	}
	wrapped := &hostInstance{Instance: instance, host: h, id: entry.ID, diagnostics: diagnostics}
	h.entries[entry.ID] = wrapped
	h.entrySpecs[entry.ID] = entry
	h.packages[entry.ID] = pkg.Ref
	h.mu.Unlock()
	return wrapped, commitErr
}

func (h *Host) startExternal(ctx context.Context, scope contract.Scope, pkg packagehost.Package, entry contract.Entry, adapter contract.Runtime) error {
	scope = &hostScope{Scope: scope, host: h}
	owner := scope.Identity()
	if err := contract.ValidateFrameBytes(h.options.FrameBytes); err != nil {
		return err
	}
	if err := h.store.BeginRun(ctx, owner); err != nil {
		if catalogCommitted(err) {
			return errors.Join(err, h.store.EndRun(context.Background(), owner, "failed"))
		}
		return err
	}
	if err := scope.Defer(func(cleanupCtx context.Context) error {
		state := "stopped"
		if core.FailedCleanup(cleanupCtx) {
			state = "failed"
		}
		return h.store.EndRun(cleanupCtx, owner, state)
	}); err != nil {
		return errors.Join(err, h.store.EndRun(context.Background(), owner, "failed"))
	}
	minor, err := pkg.Manifest.Protocol.Negotiate(contract.Protocol{Major: 1})
	if err != nil {
		return err
	}
	snapshot, err := h.store.Snapshot(ctx, pkg.Ref)
	if err != nil {
		return err
	}
	if err := scope.Defer(func(context.Context) error { return snapshot.Close() }); err != nil {
		return err
	}
	artifact, err := packagehost.Artifact(pkg.Manifest, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	launch := contract.Launch{Identity: owner, Directory: snapshot.Directory, Artifact: artifact, Grace: h.options.Grace, FrameBytes: h.options.FrameBytes, Stderr: h.options.Stderr}
	launch.Handle = func(callCtx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
		return h.handleGuest(callCtx, scope, method, input)
	}
	session, err := adapter.Start(ctx, launch)
	if err != nil {
		return err
	}
	plugin := genericPlugin{session: session}
	lifecycle := scope.(contract.ActivationScope)
	if err := lifecycle.OnPublish(func(publishCtx context.Context) error {
		publishCtx, cancel := context.WithTimeout(publishCtx, h.options.Grace)
		defer cancel()
		return plugin.Publish(publishCtx)
	}); err != nil {
		return errors.Join(err, session.Close())
	}
	if err := lifecycle.OnRetire(func(retireCtx context.Context) error {
		retireCtx, cancel := context.WithTimeout(retireCtx, h.options.Grace)
		defer cancel()
		return plugin.Retire(retireCtx)
	}); err != nil {
		return errors.Join(err, session.Close())
	}
	if err := scope.Defer(func(cleanupCtx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(cleanupCtx, h.options.Grace)
		defer cancel()
		shutdownCtx = context.WithValue(shutdownCtx, externalCleanupKey{}, owner)
		shutdownErr := plugin.Shutdown(shutdownCtx)
		return errors.Join(shutdownErr, session.Close())
	}); err != nil {
		return err
	}
	registration, err := plugin.Initialize(ctx, contract.Initialize{Identity: owner, Protocol: pkg.Manifest.Protocol, Definition: entry.Definition, Config: scope.Config(), FrameBytes: h.options.FrameBytes})
	if err != nil {
		return err
	}
	if registration.FrameBytes != h.options.FrameBytes {
		return contract.Fail(contract.Invalid, "registration frameBytes mismatch")
	}
	if registration.Minor != minor {
		return contract.Fail(contract.Incompatible, "plugin selected incompatible protocol minor")
	}
	for _, required := range pkg.Manifest.Protocol.RequiredFeatures {
		found := false
		for _, feature := range registration.Features {
			if required == feature {
				found = true
				break
			}
		}
		if !found {
			return contract.Fail(contract.Incompatible, "plugin omitted required feature: "+required)
		}
	}
	declared := map[string]contract.Service{}
	for _, service := range pkg.Manifest.Definitions {
		if service.ID == entry.Definition {
			for _, provided := range service.Provides {
				declared[provided.Key()] = provided
			}
		}
	}
	for _, service := range registration.Services {
		expected, ok := declared[service.Key()]
		if !ok || expected != service {
			return contract.Fail(contract.Invalid, "plugin registered undeclared service")
		}
		service := service
		if err := scope.Provide(service, func(callCtx context.Context, input json.RawMessage) (json.RawMessage, error) {
			if core.IsCleanup(callCtx, owner) {
				callCtx = context.WithValue(callCtx, externalCleanupKey{}, owner)
			}
			return plugin.Invoke(callCtx, contract.Invocation{Service: service, Input: input, Cleanup: core.IsCleanup(callCtx, owner)})
		}); err != nil {
			return err
		}
		delete(declared, service.Key())
	}
	if len(declared) > 0 {
		return contract.Fail(contract.Invalid, "plugin omitted declared service")
	}
	for _, name := range registration.Subscriptions {
		name := name
		if err := scope.On(name, func(callCtx context.Context, input json.RawMessage) (contract.EventResult, error) {
			results, err := plugin.Deliver(callCtx, contract.Event{Name: name, Input: input, Mode: core.DispatchMode(callCtx)})
			if err != nil {
				return contract.EventResult{}, err
			}
			for _, result := range results {
				if result.Handled {
					return result, nil
				}
			}
			return contract.EventResult{}, nil
		}); err != nil {
			return err
		}
	}
	go func() {
		<-session.Done()
		if err := session.Err(); err != nil {
			h.engine.FailActivation(owner, err)
		}
	}()
	return nil
}

func (h *Host) handleGuest(ctx context.Context, scope contract.Scope, method string, input json.RawMessage) (json.RawMessage, error) {
	cleanupOwner, _ := ctx.Value(externalCleanupKey{}).(contract.Identity)
	cleanupCall := method == "call" && cleanupOwner == scope.Identity()
	if !cleanupCall {
		if err := scope.Context().Err(); err != nil {
			return nil, err
		}
		callCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(scope.Context(), cancel)
		defer func() { stop(); cancel() }()
		ctx = callCtx
	}
	switch method {
	case "child":
		return h.handleChild(ctx, scope, input)
	case "call":
		var request contract.ServiceCall
		if err := contract.Decode(input, &request); err != nil {
			return nil, err
		}
		return scope.Call(ctx, request.Required, request.Input)
	case "event":
		var request contract.Event
		if err := contract.Decode(input, &request); err != nil {
			return nil, err
		}
		value, err := scope.Emit(ctx, request.Name, request.Input, request.Mode)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	default:
		return nil, contract.Fail(contract.Invalid, "unknown guest method")
	}
}

func (h *Host) Disable(ctx context.Context, id string) error {
	if err := h.lockOperation(ctx); err != nil {
		return err
	}
	defer h.unlockOperation()
	release, err := h.reserveTree(id)
	if err != nil {
		return err
	}
	defer release()
	return h.disable(ctx, id)
}

func (h *Host) disable(ctx context.Context, id string) error {
	all, err := h.entrySnapshot()
	if err != nil {
		return err
	}
	owned := ownedEntries(all, map[string]bool{id: true})
	if len(owned) == 0 {
		return contract.Fail(contract.NotReady, "entry does not exist")
	}
	return h.disableEntries(ctx, owned)
}

func (h *Host) disableEntries(ctx context.Context, entries []contract.Entry) error {
	var result error
	for n := len(entries) - 1; n >= 0; n-- {
		entry := entries[n]
		h.mu.Lock()
		_, live := h.entries[entry.ID]
		h.mu.Unlock()
		var err error
		if live {
			err = h.retireOne(ctx, entry.ID, true)
		} else {
			if !entry.Enabled {
				continue
			}
			entry.Enabled = false
			err = h.store.SaveEntry(ctx, entry)
		}
		result = errors.Join(result, err)
		if err != nil && (!h.engine.Missing(entry.ID) || !live && !catalogCommitted(err)) {
			return result
		}
	}
	return result
}

func (h *Host) retireOne(ctx context.Context, id string, disable bool) error {
	h.mu.Lock()
	instance := h.entries[id]
	entry := h.entrySpecs[id]
	_, builtin := h.builtins[id]
	h.mu.Unlock()
	if instance == nil {
		return contract.Fail(contract.NotReady, "entry does not exist")
	}
	var persistErr error
	if disable {
		entry.Enabled = false
		if !builtin {
			persistErr = h.store.SaveEntry(ctx, entry)
			if persistErr != nil && !catalogCommitted(persistErr) {
				return persistErr
			}
		}
		h.mu.Lock()
		h.entrySpecs[id] = entry
		h.mu.Unlock()
	}
	var wait func(context.Context) error
	var err error
	if disable {
		wait, err = h.engine.Disable(ctx, id)
	} else {
		wait, err = h.engine.Stop(ctx, id)
	}
	if err != nil {
		return errors.Join(persistErr, err)
	}
	stopErr := h.waitOperation(func() error { return wait(ctx) })
	removeErr := h.waitOperation(func() error { return h.engine.Remove(ctx, id) })
	if !h.engine.Missing(id) {
		return errors.Join(stopErr, removeErr, persistErr)
	}
	h.mu.Lock()
	delete(h.entries, id)
	delete(h.packages, id)
	delete(h.entrySpecs, id)
	delete(h.builtins, id)
	h.mu.Unlock()
	return errors.Join(stopErr, removeErr, persistErr)
}

func (h *Host) Uninstall(ctx context.Context, ref contract.PackageRef) (packagehost.Removal, error) {
	if err := h.lockOperation(ctx); err != nil {
		return packagehost.Removal{}, err
	}
	defer h.unlockOperation()
	if h.packageBusy(ref.ID) {
		return packagehost.Removal{}, contract.Fail(contract.Conflict, "package operation is in progress")
	}
	options, err := h.changeOptions()
	if err != nil {
		return packagehost.Removal{}, err
	}
	return h.store.Uninstall(ctx, ref, options)
}

func (h *Host) Replace(ctx context.Context, ref contract.PackageRef) error {
	if err := h.lockOperation(ctx); err != nil {
		return err
	}
	defer h.unlockOperation()
	releasePackage, err := h.reservePackage(ref.ID)
	if err != nil {
		return err
	}
	defer releasePackage()
	h.mu.Lock()
	var roots []string
	for id, pkg := range h.packages {
		if pkg.ID == ref.ID {
			roots = append(roots, id)
		}
	}
	h.mu.Unlock()
	release, err := h.reserveTrees(roots)
	if err != nil {
		return err
	}
	defer release()
	return h.replace(ctx, ref)
}

func (h *Host) replace(ctx context.Context, ref contract.PackageRef) error {
	h.mu.Lock()
	all := make([]contract.Entry, 0, len(h.entrySpecs))
	roots := map[string]bool{}
	builtins := make(map[string]contract.Definition, len(h.builtins))
	for id, definition := range h.builtins {
		builtins[id] = definition
	}
	for id, entry := range h.entrySpecs {
		all = append(all, entry)
		if h.packages[id].ID == ref.ID {
			roots[id] = true
		}
	}
	h.mu.Unlock()
	entries := ownedEntries(all, roots)
	// Preflight with actual reverse dependencies while leaving live old instances untouched.
	actual, err := h.changeOptions()
	if err != nil {
		return err
	}
	if err := h.store.CheckSelect(ctx, ref, actual); err != nil {
		return err
	}
	var retirementErrors error
	for n := len(entries) - 1; n >= 0; n-- {
		if err := h.retireOne(ctx, entries[n].ID, false); err != nil {
			if !h.engine.Missing(entries[n].ID) {
				return errors.Join(retirementErrors, err)
			}
			retirementErrors = errors.Join(retirementErrors, err)
		}
	}
	actual, err = h.changeOptions()
	if err != nil {
		return errors.Join(retirementErrors, err)
	}
	selectErr := h.store.Select(ctx, ref, actual)
	if selectErr != nil && !catalogCommitted(selectErr) {
		return errors.Join(retirementErrors, selectErr)
	}
	result := errors.Join(retirementErrors, selectErr)
	instances := []contract.Instance{}
	for _, entry := range entries {
		var instance contract.Instance
		var err error
		if definition, ok := builtins[entry.ID]; ok {
			instance, err = h.addBuiltin(entry, definition)
		} else {
			instance, err = h.enable(ctx, entry)
		}
		if err != nil {
			result = errors.Join(result, err)
		}
		if instance != nil {
			instances = append(instances, instance)
		}
	}
	for _, instance := range instances {
		result = errors.Join(result, h.waitOperation(func() error { return instance.Wait(ctx) }))
	}
	return result
}

// Close drains activations before releasing the store. Cancellation ends only
// this wait: Go cannot forcibly stop a non-cooperative builtin callback or its
// cleanup. Its ownership/dependency tree remains pending until it returns;
// unrelated activation cleanups do not share a global concurrency limit.
func (h *Host) Close(ctx context.Context) error {
	select {
	case h.operations <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer h.unlockOperation()
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	err := h.waitOperation(func() error { return h.engine.Close(ctx) })
	if !h.engine.Drained() {
		return err
	}
	return errors.Join(err, h.store.Close())
}

func (h *Host) AddBuiltin(ctx context.Context, entry contract.Entry, definition contract.Definition) (contract.Instance, error) {
	if err := h.lockOperation(ctx); err != nil {
		return nil, err
	}
	defer h.unlockOperation()
	if h.controlBusy(entry.ID, entry.Parent) {
		return nil, contract.Fail(contract.Conflict, "entry ownership tree has an operation in progress")
	}
	return h.addBuiltin(entry, definition)
}

func (h *Host) addBuiltin(entry contract.Entry, definition contract.Definition) (contract.Instance, error) {
	release, err := h.reserveEntry(entry.ID)
	if err != nil {
		return nil, err
	}
	defer release()
	if definition.Start == nil {
		return nil, contract.Fail(contract.Invalid, "builtin start callback is required")
	}
	if entry.Package == "" {
		entry.Package = "builtin"
	}
	if len(entry.Config) == 0 {
		entry.Config = json.RawMessage(`{}`)
	}
	entry.Enabled = true
	entry.Config = append(json.RawMessage(nil), entry.Config...)
	original := definition
	definition.Start = func(ctx context.Context, scope contract.Scope) error {
		return original.Start(ctx, &hostScope{Scope: scope, host: h, definitions: original.Children})
	}
	instance, err := h.engine.Add(entry, definition, contract.Identity{Package: entry.Package, Entry: entry.ID, Digest: "builtin"})
	if err != nil {
		return nil, err
	}
	wrapped := &hostInstance{Instance: instance, host: h, id: entry.ID}
	h.mu.Lock()
	h.entries[entry.ID] = wrapped
	h.entrySpecs[entry.ID] = entry
	h.builtins[entry.ID] = original
	h.mu.Unlock()
	return wrapped, nil
}

func (h *Host) Status(id string) (contract.Status, error) {
	h.mu.Lock()
	instance := h.entries[id]
	h.mu.Unlock()
	if instance == nil {
		return contract.Status{}, contract.Fail(contract.NotReady, "entry does not exist")
	}
	return instance.Status(), nil
}

// Resume re-enables an existing, fully stopped entry. Disable removes the entry;
// use Enable/AddBuiltin to create it again after Disable instead.
func (h *Host) Resume(ctx context.Context, id string) error {
	if err := h.lockOperation(ctx); err != nil {
		return err
	}
	release, err := h.reserveTree(id)
	if err != nil {
		h.unlockOperation()
		return err
	}
	h.mu.Lock()
	enabled := h.entrySpecs[id].Enabled
	h.mu.Unlock()
	if !enabled {
		release()
		h.unlockOperation()
		return contract.Fail(contract.NotReady, "entry is persistently disabled")
	}
	instance, err := h.engine.Resume(id)
	release()
	h.unlockOperation()
	if err != nil {
		return err
	}
	return instance.Wait(ctx)
}
