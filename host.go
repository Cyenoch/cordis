package cordis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/Cyenoch/cordis/access"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
	"github.com/Cyenoch/cordis/packagehost"
	"github.com/Cyenoch/cordis/schema"
)

type externalCleanupKey struct{}

type Options struct {
	Directory        string
	Trust            packagehost.TrustPolicy
	DecodePackage    packagehost.MetadataDecoder
	Runtimes         map[string]contract.Runtime
	Authorize        func(context.Context, contract.Identity, contract.Capability) (json.RawMessage, error)
	SessionFactory   func(contract.Manifest) *contract.SessionFactory
	FrameBytes       int
	HandleGuest      contract.GuestAdapter
	Sink             *access.Sink
	DiagnosticStderr bool
	Grace            time.Duration
}

type Host struct {
	mu               sync.Mutex
	operations       chan struct{}
	childMu          sync.Mutex
	engine           *core.Engine
	store            *packagehost.Store
	policy           *access.Policy
	broker           *access.Broker
	contributions    *access.ContributionStore
	tasks            *access.TaskManager
	options          Options
	entries          map[string]contract.Instance
	entrySpecs       map[string]contract.Entry
	packages         map[string]contract.PackageRef
	builtins         map[string]contract.Definition
	batches          map[string]*access.Batch
	staged           map[string][]contract.Contribution
	sessions         map[string]contract.Session
	dynamic          map[string]ownedChild
	definitions      map[string]contract.Definition
	admissions       map[string]bool
	controls         map[string]bool
	packageOps       map[string]bool
	closed           bool
	diagnosticErrors []error
}

func Open(options Options) (*Host, error) {
	if options.Directory == "" {
		return nil, contract.Fail(contract.Invalid, "package directory required")
	}
	store, err := packagehost.OpenWithOptions(options.Directory, packagehost.Options{Trust: options.Trust, Decode: options.DecodePackage})
	if err != nil {
		return nil, err
	}
	policy := access.NewPolicy()
	h := &Host{store: store, policy: policy, broker: access.NewBroker(policy), contributions: access.NewContributionStore(policy),
		tasks: access.NewTaskManager(policy), operations: make(chan struct{}, 1), options: options, entries: map[string]contract.Instance{},
		entrySpecs: map[string]contract.Entry{}, packages: map[string]contract.PackageRef{}, builtins: map[string]contract.Definition{}, batches: map[string]*access.Batch{}, sessions: map[string]contract.Session{}, dynamic: map[string]ownedChild{}, definitions: map[string]contract.Definition{}}
	if h.options.Grace == 0 {
		h.options.Grace = time.Second
	}
	h.engine = core.NewWithHooks(core.Hooks{BeforePublish: h.beforePublish, OnRetire: h.onRetire})
	if err := h.contributions.SetActiveGate(h.engine.IsActive); err != nil {
		return nil, errors.Join(err, store.Close())
	}
	return h, nil
}

func (h *Host) Store() *packagehost.Store                { return h.store }
func (h *Host) Access() *access.Policy                   { return h.policy }
func (h *Host) Broker() *access.Broker                   { return h.broker }
func (h *Host) Contributions() *access.ContributionStore { return h.contributions }
func (h *Host) Tasks() *access.TaskManager               { return h.tasks }
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
func (h *Host) ConfigureRealm(name, parent string, isolated []string) error {
	return h.engine.ConfigureRealm(name, parent, isolated)
}
func (h *Host) Call(ctx context.Context, realm string, required contract.Requirement, input json.RawMessage) (json.RawMessage, error) {
	return h.engine.Call(ctx, realm, required, input)
}
func (h *Host) Emit(ctx context.Context, realm, name string, input json.RawMessage, mode contract.Dispatch) ([]contract.EventResult, error) {
	return h.engine.Emit(ctx, realm, name, input, mode)
}

func (h *Host) beforePublish(owner contract.Identity) error {
	h.mu.Lock()
	batch := h.batches[owner.Activation]
	h.mu.Unlock()
	if batch != nil {
		return batch.Commit()
	}
	h.mu.Lock()
	items := h.staged[owner.Activation]
	delete(h.staged, owner.Activation)
	h.mu.Unlock()
	if len(items) == 0 {
		return nil
	}
	staged, err := h.contributions.Stage(context.Background(), owner, items)
	if err != nil {
		return err
	}
	return staged.Commit()
}

func (h *Host) onRetire(owner contract.Identity) {
	h.mu.Lock()
	delete(h.batches, owner.Activation)
	delete(h.staged, owner.Activation)
	h.mu.Unlock()
	// Revoke is idempotent at this host seam: failed startup may never activate policy.
	if err := h.policy.Check(owner); err == nil {
		if err := h.policy.Revoke(owner); err != nil {
			h.engine.FailActivation(owner, err)
		}
	}
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
		state := instance.(*hostInstance).Instance.Status().State
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
	closed := h.closed
	_, exists := h.entries[entry.ID]
	h.mu.Unlock()
	if closed {
		return nil, contract.Fail(contract.NotReady, "host is closed")
	}
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
	adapter := h.options.Runtimes[pkg.Manifest.Runtime.Kind]
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
		return h.diagnose(startCtx, scope.Identity(), "", "initialize", h.startExternal(startCtx, scope, pkg, entry, adapter))
	}
	if err := h.engine.Preflight(entry, definition); err != nil {
		return nil, err
	}
	var commitErr error
	if owner == nil {
		if err := h.registerBuiltinParent(ctx, entry.Parent); err != nil {
			return nil, err
		}
		commitErr = h.store.SaveEntry(ctx, entry)
	}
	if commitErr != nil && !catalogCommitted(commitErr) {
		return nil, commitErr
	}
	identity := contract.Identity{Package: pkg.Ref.ID, Entry: entry.ID, Digest: pkg.Ref.Digest}
	var instance contract.Instance
	if owner == nil {
		instance, err = h.engine.Add(entry, definition, identity)
	} else {
		instance, err = h.engine.AddOwned(entry, definition, identity, *owner)
	}
	if err != nil {
		return nil, err
	}
	instance = &hostInstance{Instance: instance, host: h, id: entry.ID, diagnostics: diagnostics}
	h.mu.Lock()
	if owner != nil {
		instance = instance.(*hostInstance).Instance
		h.dynamic[entry.ID] = ownedChild{owner: *owner, instance: instance}
		h.mu.Unlock()
		return instance, nil
	}
	h.entries[entry.ID] = instance
	h.entrySpecs[entry.ID] = entry
	h.packages[entry.ID] = pkg.Ref
	h.mu.Unlock()
	return instance, commitErr
}

func (h *Host) startExternal(ctx context.Context, scope contract.Scope, pkg packagehost.Package, entry contract.Entry, adapter contract.Runtime) error {
	scope = &hostScope{Scope: scope, host: h, realm: entry.Realm}
	owner := scope.Identity()
	var factory *contract.SessionFactory
	if h.options.SessionFactory != nil {
		factory = h.options.SessionFactory(pkg.Manifest)
	}
	if err := factory.Validate(h.options.FrameBytes); err != nil {
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
	if err := h.policy.Activate(owner, pkg.Manifest.Capabilities); err != nil {
		return err
	}
	if err := scope.Defer(func(context.Context) error { h.onRetire(owner); return nil }); err != nil {
		return err
	}
	for _, capability := range pkg.Manifest.Capabilities {
		if h.options.Authorize == nil {
			continue
		}
		grant, err := h.options.Authorize(ctx, owner, capability)
		if err != nil {
			return err
		}
		if grant != nil {
			if _, err := h.policy.Grant(owner, capability.ID, grant); err != nil {
				return err
			}
		}
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
	launch := contract.Launch{Identity: owner, Manifest: pkg.Manifest, Directory: snapshot.Directory, Artifact: artifact, Grace: h.options.Grace, FrameBytes: h.options.FrameBytes, SessionFactory: factory}
	if h.options.DiagnosticStderr && h.options.Sink != nil {
		launch.Stderr = func(chunk []byte) error {
			err := h.options.Sink.Emit(context.Background(), access.DiagnosticInput{Owner: owner, Stage: "stderr", Code: contract.PluginFailed, Summary: string(chunk)})
			if err != nil {
				h.keepDiagnosticError(err)
			}
			return err
		}
	}
	launch.Handle = func(callCtx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
		value, err := h.handleGuest(callCtx, scope, method, input)
		if err != nil {
			return nil, h.options.Sink.PublicError(h.diagnose(callCtx, owner, method, "guest-call", err))
		}
		return value, nil
	}
	session, err := adapter.Start(ctx, launch)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.sessions[owner.Activation] = session
	h.mu.Unlock()
	if err := scope.Defer(func(context.Context) error {
		h.mu.Lock()
		delete(h.sessions, owner.Activation)
		h.mu.Unlock()
		return nil
	}); err != nil {
		return err
	}
	var plugin contract.PluginSession = genericPlugin{session: session}
	selected, _ := session.(contract.PluginSession)
	if adapted, ok := session.(interface{ PluginSession() contract.PluginSession }); ok {
		if mapped := adapted.PluginSession(); mapped != nil {
			selected = mapped
		}
	}
	if factory.IsCustom() != (selected != nil) {
		return errors.Join(contract.Fail(contract.Invalid, "session factory protocol does not match PluginSession"), session.Close())
	}
	if selected != nil {
		plugin = selected
	}
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
	if !factory.IsCustom() && registration.FrameBytes != h.options.FrameBytes {
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
	items := append(append([]contract.Contribution{}, pkg.Manifest.Contributions...), registration.Contributions...)
	batch, err := h.contributions.Stage(ctx, owner, items)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.batches[owner.Activation] = batch
	h.mu.Unlock()
	go func() {
		<-session.Done()
		if err := session.Err(); err != nil {
			h.engine.FailActivation(owner, h.diagnose(context.Background(), owner, "", "session", err))
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
		if err := h.policy.Check(scope.Identity()); err != nil {
			return nil, err
		}
	}
	if h.options.HandleGuest != nil {
		return h.options.HandleGuest(ctx, scope, method, input)
	}
	return h.DispatchGuest(ctx, scope, method, input)
}

// DispatchGuest is the standard dispatcher for explicit delegation by a custom
// HandleGuest. A handler error never triggers implicit fallback.
func (h *Host) DispatchGuest(ctx context.Context, scope contract.Scope, method string, input json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "child":
		return h.handleChild(ctx, scope, input)
	case "task.begin":
		var request contract.TaskRequest
		if err := contract.Decode(input, &request); err != nil {
			return nil, err
		}
		task, err := h.tasks.Create(scope.Context(), scope.Identity(), request.ID, request.Token)
		if err != nil {
			return nil, err
		}
		if err := task.Start(); err != nil {
			return nil, err
		}
		go func() {
			<-task.Done()
			state := task.Status().State
			if state != access.TaskCanceled && state != access.TaskInterrupted {
				return
			}
			if scope.Context().Err() != nil {
				return
			}
			h.mu.Lock()
			session := h.sessions[scope.Identity().Activation]
			h.mu.Unlock()
			if session == nil {
				return
			}
			controlCtx, cancel := context.WithTimeout(context.Background(), h.options.Grace)
			defer cancel()
			data, err := json.Marshal(contract.TaskRequest{ID: request.ID, Token: request.Token})
			if err == nil {
				_, err = session.Call(controlCtx, "task.cancel", data)
			}
			if err != nil {
				h.engine.FailActivation(scope.Identity(), err)
			}
		}()
		return json.RawMessage(`{}`), nil
	case "task.release":
		var request contract.TaskRequest
		if err := contract.Decode(input, &request); err != nil {
			return nil, err
		}
		if err := h.tasks.Release(scope.Identity(), request.ID, request.Token); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	case "task.finish":
		var request contract.TaskRequest
		if err := contract.Decode(input, &request); err != nil {
			return nil, err
		}
		var failure error
		if request.Failure != "" {
			failure = contract.Fail(request.Failure, "plugin task failed")
		}
		if err := h.tasks.Finish(scope.Identity(), request.ID, request.Token, access.TaskState(request.State), request.Result, failure); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
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
	case "host.invoke":
		var request contract.HostCall
		if err := contract.Decode(input, &request); err != nil {
			return nil, err
		}
		return h.broker.Call(ctx, scope.Identity(), request.Capability, request.Input)
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
	all, err := h.store.Entries()
	if err != nil {
		return err
	}
	h.mu.Lock()
	for id, entry := range h.entrySpecs {
		if _, builtin := h.builtins[id]; builtin {
			all = append(all, entry)
		}
	}
	h.mu.Unlock()
	owned := ownedEntries(all, map[string]bool{id: true})
	if len(owned) == 0 {
		return contract.Fail(contract.NotReady, "entry does not exist")
	}
	var result error
	for n := len(owned) - 1; n >= 0; n-- {
		h.mu.Lock()
		_, live := h.entries[owned[n].ID]
		h.mu.Unlock()
		var err error
		if live {
			err = h.disableOne(ctx, owned[n].ID)
		} else {
			entry := owned[n]
			if !entry.Enabled {
				continue
			}
			entry.Enabled = false
			err = h.store.SaveEntry(ctx, entry)
		}
		result = errors.Join(result, err)
		if err != nil && (!h.engine.Missing(owned[n].ID) || !live && !catalogCommitted(err)) {
			return result
		}
	}
	return result
}

func (h *Host) disableOne(ctx context.Context, id string) error {
	h.mu.Lock()
	instance := h.entries[id]
	entry := h.entrySpecs[id]
	_, builtin := h.builtins[id]
	h.mu.Unlock()
	if instance == nil {
		return contract.Fail(contract.NotReady, "entry does not exist")
	}
	var persistErr error
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
	wait, err := h.engine.Stop(ctx, id)
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

func (h *Host) Uninstall(ctx context.Context, ref contract.PackageRef, deleteData bool) (packagehost.Removal, error) {
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
	options.DeleteData = deleteData
	return h.store.Uninstall(ctx, ref, options)
}

func (h *Host) Replace(ctx context.Context, ref contract.PackageRef, options packagehost.ChangeOptions) error {
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
	return h.replace(ctx, ref, options)
}

func (h *Host) replace(ctx context.Context, ref contract.PackageRef, options packagehost.ChangeOptions) error {
	if options.MigrationCommitted {
		if err := h.store.ConfirmDataVersion(ctx, ref.ID, options.DataVersion); err != nil {
			return err
		}
	}
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
	actual.DataVersion = options.DataVersion
	actual.MigrationCommitted = options.MigrationCommitted
	if err := h.store.CheckSelect(ref, actual); err != nil {
		return err
	}
	if err := h.checkRuntime(ref); err != nil {
		return err
	}
	var retirementErrors error
	for n := len(entries) - 1; n >= 0; n-- {
		if err := h.disableOne(ctx, entries[n].ID); err != nil {
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
	actual.DataVersion = options.DataVersion
	actual.MigrationCommitted = options.MigrationCommitted
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
		return h.startBuiltin(ctx, scope, entry.Realm, original)
	}
	instance, err := h.engine.Add(entry, definition, contract.Identity{Package: entry.Package, Entry: entry.ID, Digest: "builtin"})
	if err != nil {
		return nil, err
	}
	instance = &hostInstance{Instance: instance, host: h, id: entry.ID}
	h.mu.Lock()
	h.entries[entry.ID] = instance
	h.entrySpecs[entry.ID] = entry
	h.builtins[entry.ID] = original
	h.mu.Unlock()
	return instance, nil
}

func (h *Host) Status(id string) (contract.Status, error) {
	h.mu.Lock()
	instance := h.entries[id]
	entry := h.entrySpecs[id]
	h.mu.Unlock()
	if instance == nil {
		return contract.Status{}, fmt.Errorf("entry %q not found", id)
	}
	status := instance.Status()
	status.Enabled = entry.Enabled
	status.TemporaryStopped = entry.Enabled && status.State == contract.Stopped
	return status, nil
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

// Supervise explicitly restarts failed activations within a finite lifetime
// budget. Canceling supervision never replays an in-flight service operation.
func (h *Host) Supervise(ctx context.Context, id string, budget uint32, delay time.Duration) error {
	return h.engine.Supervise(ctx, id, budget, delay)
}
