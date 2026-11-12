// Package hosting runs installed native plugin packages as entries of one
// kernel, next to the in-process plugins the application loads itself.
package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
	"github.com/Cyenoch/cordis/runtime/native"
)

// Options configures a Host. Directory, the package store, is required; the
// rest have working defaults.
type Options struct {
	Directory  string
	Trust      packagehost.TrustPolicy
	Runtime    contract.Runtime
	FrameBytes int
	// Inflight is how many requests a session carries at once in each
	// direction, streams included; eight are kept for the session's own
	// control. Zero is contract.DefaultInflight. A caller beyond the limit
	// waits for a place; a hop of a chain does not wait and gets not_ready.
	Inflight int
	Stderr   func(contract.StderrRecord) error
	// Grace is how long a plugin has to answer a cancellation, to shut down
	// when asked, and to exit after that; a plugin that takes longer is
	// treated as stuck and its process ended. Zero is DefaultGrace.
	Grace time.Duration
	// OnCall, when set, is told about every call and stream routed to a
	// native entry once it has ended, for metrics and tracing. It runs on
	// the caller's goroutine, outside state locks, and must return promptly.
	OnCall func(CallRecord)
	// Logger is optional and called synchronously outside state locks. Handlers
	// must return promptly and must not reenter Host management methods.
	Logger *slog.Logger
	// CapturePanic includes the stacks of panics in in-process plugins in the
	// configured logger, never in Status.
	CapturePanic bool
	// Recovery is opt-in, keyed by an existing persistent native entry ID.
	Recovery map[string]RecoveryPolicy
}

// DefaultGrace leaves room for cleanups and for a busy machine, and still
// ends a plugin that is really stuck within seconds.
const DefaultGrace = 5 * time.Second

// CallRecord describes one finished call or stream to a native entry. It
// carries no request or response data.
type CallRecord struct {
	Entry      string
	Activation string
	Service    contract.Service
	Method     string
	// Stream marks a streaming method; Items is how many items its consumer
	// received.
	Stream   bool
	Items    int
	Duration time.Duration
	// Err is what the caller got; nil for a call that succeeded and for a
	// stream that ended or that its consumer left.
	Err error
}

// Host runs the entries of one package store: it keeps their saved intent,
// starts them in dependency order and coordinates package changes with what
// runs. Open one with Open and Close it when done.
type Host struct {
	mu             sync.Mutex
	operations     chan struct{}
	childMu        sync.Mutex
	app            *cordis.App
	store          *packagehost.Store
	options        Options
	entries        map[string]*hostInstance
	entrySpecs     map[string]contract.Entry
	packages       map[string]contract.PackageRef
	dynamic        map[string]ownedChild
	admissions     map[string]bool
	controls       map[string]bool
	packageOps     map[string]bool
	closed         bool
	done           chan struct{}
	doneOnce       sync.Once
	recovery       map[string]*recoveryEntry
	recoveryEvents []RecoveryInfo
	recoveryCancel context.CancelFunc
	recoveryDone   chan struct{}
	changed        chan struct{} // closed and replaced whenever an instance or a service changes
}

// Open opens the store in options.Directory, locking it for this process.
// It starts nothing; Restore starts the saved entries.
func Open(options Options) (*Host, error) {
	if options.Directory == "" {
		return nil, contract.Fail(contract.Invalid, "package directory required")
	}
	store, err := packagehost.OpenWithOptions(options.Directory, packagehost.Options{Trust: options.Trust, Logger: options.Logger})
	if err != nil {
		return nil, err
	}
	if options.Runtime == nil {
		options.Runtime = native.New(native.Options{})
	}
	if options.Inflight == 0 {
		options.Inflight = contract.DefaultInflight
	}
	if options.FrameBytes == 0 {
		options.FrameBytes = contract.MaxFrameBytes
	}
	h := &Host{store: store, operations: make(chan struct{}, 1), options: options,
		entries: map[string]*hostInstance{}, entrySpecs: map[string]contract.Entry{},
		packages: map[string]contract.PackageRef{},
		dynamic:  map[string]ownedChild{}, admissions: map[string]bool{},
		controls: map[string]bool{}, packageOps: map[string]bool{}, done: make(chan struct{})}
	if h.options.Grace == 0 {
		h.options.Grace = DefaultGrace
	}
	if err := h.initRecovery(options.Recovery); err != nil {
		return nil, errors.Join(err, store.Close())
	}
	h.app = cordis.New(cordis.Options{Logger: options.Logger, CapturePanic: options.CapturePanic})
	h.changed = make(chan struct{})
	notify := func() {
		h.mu.Lock()
		close(h.changed)
		h.changed = make(chan struct{})
		h.mu.Unlock()
	}
	cordis.InstanceChanged.On(h.app.Scope(), func(context.Context, cordis.InstanceChange) error {
		notify()
		return nil
	})
	cordis.ServiceChanged.On(h.app.Scope(), func(context.Context, cordis.ServiceChange) error {
		notify()
		return nil
	}, cordis.Global)
	h.startRecovery()
	return h, nil
}

// Catalog reads the package store a Host owns. Changes go through the Host,
// which coordinates them with running instances.
type Catalog interface {
	Installed() ([]packagehost.Package, error)
	Get(ref contract.PackageRef) (packagehost.Package, error)
	Selected(id string) (packagehost.Package, error)
	Entries() ([]contract.Entry, error)
	Runs() ([]packagehost.RunRecord, error)
	Preflight(ref contract.PackageRef) ([]string, error)
	PendingRemovals() ([]contract.PackageRef, error)
}

// Store returns the read side of the Host's package store.
func (h *Host) Store() Catalog { return h.store }

// UpdateTrust replaces the trust policy. Later installs, selections and
// activation snapshots are checked against it; running activations go on.
func (h *Host) UpdateTrust(trust packagehost.TrustPolicy) error { return h.store.UpdateTrust(trust) }

// RetryRemovals deletes the files of earlier uninstalls that are still
// pending.
func (h *Host) RetryRemovals() error { return h.store.RetryRemovals() }

// changes returns a channel that is closed at the next change of an
// instance or a service.
func (h *Host) changes() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.changed
}

// Scope is the root scope of the host's kernel. Plugins loaded on it run in
// the host process and share the service space of the native entries: each
// side can require the endpoints the other provides. Closing the host stops
// them too.
func (h *Host) Scope() *cordis.Scope { return h.app.Scope() }

// ProvideParent makes the instance that owns s the parent called id:
// persistent entries that name id as their parent start once it is Active
// and clean up before it stops. Only one instance can be a given parent at
// a time. The host stores the identifier with those entries, never the
// plugin or its configuration.
func (h *Host) ProvideParent(s *cordis.Scope, id string) error {
	if id == "" {
		return contract.Fail(contract.Invalid, "parent id is required")
	}
	h.mu.Lock()
	_, entry := h.entrySpecs[id]
	h.mu.Unlock()
	if entry {
		return contract.Fail(contract.Conflict, "parent id belongs to a persistent entry")
	}
	if err := parentKey(id).Provide(s, contract.Identity{Entry: id}); err != nil {
		if errors.Is(err, cordis.ErrConflict) {
			return errors.Join(contract.Fail(contract.Conflict, "parent id is already provided"), err)
		}
		return err
	}
	return nil
}

// Done closes after the kernel has drained and the Store is released. Close
// may still return historical or cleanup errors after this signal.
func (h *Host) Done() <-chan struct{} { return h.done }
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
func (h *Host) unlockOperation() {
	<-h.operations
	h.flushRecoveryDiagnostics()
}

// Caller resolves services as one persistent entry sees them: through the
// isolation boundaries of the entry and of the entries above it. It keeps
// the host's authority; it is not a plugin identity and bypasses no
// cleanup. It belongs to the registration it was taken from: once the entry
// has been disabled, removed or replaced it fails with not_ready, while a
// recovery restart keeps it.
type Caller struct {
	host     *Host
	id       string
	instance *hostInstance
}

// Endpoint returns the provider that satisfies required in the view, for a
// generated NewXClient, contract.Invoke or contract.InvokeStream. Like
// Service.Get on Scope, it is the provider of the moment and does not
// follow a restart: once a native provider stops, calls on it fail with
// not_ready and a new endpoint is taken.
func (c Caller) Endpoint(required contract.Requirement) (contract.Endpoint, error) {
	c.host.mu.Lock()
	current := c.host.entries[c.id] == c.instance
	scope := c.instance.scope
	c.host.mu.Unlock()
	if !current {
		return nil, contract.Fail(contract.NotReady, "view of entry "+strconv.Quote(c.id)+" is out of date: the entry was disabled, removed or replaced")
	}
	return resolve(scope, required)
}

// Call invokes one method of the provider that satisfies required.
func (c Caller) Call(ctx context.Context, required contract.Requirement, method string, input json.RawMessage) (json.RawMessage, error) {
	provider, err := c.Endpoint(required)
	if err != nil {
		return nil, err
	}
	return provider.Call(ctx, method, input)
}

// Call invokes a method of a service as the root scope sees it. Typed code
// uses a generated client on an endpoint from Scope instead.
func (h *Host) Call(ctx context.Context, required contract.Requirement, method string, input json.RawMessage) (json.RawMessage, error) {
	provider, err := resolve(h.app.Scope(), required)
	if err != nil {
		return nil, err
	}
	return provider.Call(ctx, method, input)
}

// resolve returns the provider s sees for required, if its minor is in range.
func resolve(s *cordis.Scope, required contract.Requirement) (contract.Endpoint, error) {
	provider, err := contract.EndpointKey(required.Name, required.Major).Get(s)
	if err != nil {
		return nil, errors.Join(contract.Fail(contract.NotReady, "service "+required.Key()+" is not available"), err)
	}
	if !required.Accepts(provider.Service()) {
		return nil, &contract.VersionError{Required: required, Visible: provider.Service()}
	}
	return provider, nil
}

// Caller returns the view of the persistent entry entryID for as long as
// its current registration lasts.
func (h *Host) Caller(entryID string) (Caller, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	instance := h.entries[entryID]
	if instance == nil {
		return Caller{}, contract.Fail(contract.NotReady, "entry does not exist")
	}
	return Caller{host: h, id: entryID, instance: instance}, nil
}

// Emit runs the listeners of a cross-process event, or the handlers of a
// query, in every entry and in-process plugin.
func (h *Host) Emit(ctx context.Context, event contract.Event) (contract.EventResult, error) {
	return dispatch(ctx, h.app.Scope(), event)
}

// Install verifies archive against the trust policy and adds it to the
// store. It selects nothing.
func (h *Host) Install(ctx context.Context, archive string) (packagehost.Package, error) {
	return h.store.Install(ctx, archive)
}

// Select makes ref the selected version of its package after checking its
// trust, dependencies and the saved entries that use it. It refuses while
// another version of the package runs; Replace switches running entries.
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
		state := instance.Status().State
		if state == contract.Stopped || state == contract.Failed {
			continue
		}
		ref := h.packages[id]
		options.InUse = append(options.InUse, ref)
	}
	// What is not saved is in use all the same.
	for _, child := range h.dynamic {
		if state := child.instance.Status().State; state != contract.Stopped && state != contract.Failed {
			options.InUse = append(options.InUse, child.ref)
		}
	}
	return options, nil
}

// Enable saves entry as enabled and starts it. The returned Instance
// reports when it becomes active or why it waits.
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
	return h.enableEntry(ctx, entry, nil, nil)
}

// enableEntry registers an entry. With parent it is a dynamic child of that
// activation; with on it is loaded on that scope for whoever owns it, such
// as a configuration loader. Neither of those is saved.
func (h *Host) enableEntry(ctx context.Context, entry contract.Entry, parent *run, on *cordis.Scope) (contract.Instance, error) {
	entry.Isolate = slices.Clone(entry.Isolate)
	release, err := h.reserveEntry(entry.ID, parent != nil || on != nil)
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
	if parent != nil && pkg.Ref.Digest != parent.identity.Digest {
		return nil, contract.Fail(contract.Conflict, "child package selection differs from parent activation")
	}
	diagnostics, err := h.store.Preflight(pkg.Ref)
	if err != nil {
		return nil, err
	}
	if entry.Definition == "" {
		entry.Definition = pkg.Manifest.DefaultDefinition
	}
	index := slices.IndexFunc(pkg.Manifest.Definitions, func(d contract.DefinitionSpec) bool { return d.ID == entry.Definition })
	if index < 0 {
		return nil, contract.Fail(contract.Invalid, "unknown package definition")
	}
	spec := pkg.Manifest.Definitions[index]
	for _, service := range spec.Provides {
		if strings.HasPrefix(service.Name, reservedPrefix) {
			return nil, contract.Fail(contract.Invalid, "service name uses the reserved prefix "+reservedPrefix)
		}
	}
	if err := contract.CheckIsolate(entry.Isolate); err != nil {
		return nil, err
	}
	entry.Enabled = true
	if len(entry.Config) == 0 || string(entry.Config) == "null" {
		entry.Config = json.RawMessage(`{}`) // an omitted configuration is an empty object
	}
	// Nothing is saved or started for a configuration the definition rejects.
	if err := validConfig(spec, entry.Config); err != nil {
		return nil, err
	}
	instance := &hostInstance{host: h, id: entry.ID, diagnostics: diagnostics, pkg: pkg, spec: spec,
		parent: entry.Parent, enabled: true, desired: 1}
	isolate := make([]cordis.ServiceKey, len(entry.Isolate))
	for n, key := range entry.Isolate {
		isolate[n] = cordis.Name(key)
	}
	if parent != nil || on != nil {
		owner := contract.Identity{Entry: entry.Parent}
		instance.scope = on
		if parent != nil {
			owner, instance.scope = parent.identity, parent.scope
		} else {
			instance.parent = "" // the owner is not an entry
		}
		instance.owner = &owner
		if len(isolate) > 0 {
			instance.scope = instance.scope.Isolate(isolate...)
		}
		instance.instance = instance.plugin().Load(instance.scope, slices.Clone(entry.Config))
		if instance.instance.Status().State == cordis.Stopped {
			return nil, contract.Fail(contract.NotReady, "parent activation is no longer current")
		}
		h.mu.Lock()
		h.dynamic[entry.ID] = ownedChild{owner: owner, instance: instance, entry: entry, ref: pkg.Ref}
		h.mu.Unlock()
		return instance, nil
	}
	h.mu.Lock()
	entries := make([]contract.Entry, 0, len(h.entrySpecs)+1)
	for _, known := range h.entrySpecs {
		entries = append(entries, known)
	}
	instance.scope = h.app.Scope()
	if live := h.entries[entry.Parent]; live != nil {
		instance.scope = live.scope
	}
	_, known := h.entrySpecs[entry.Parent]
	h.mu.Unlock()
	if err := contract.CheckEntryGraph(append(entries, entry)); err != nil && contract.ErrorCode(err) == contract.Reentrant {
		return nil, err
	}
	if entry.Parent != "" && !known {
		// The parent is an in-process plugin, or an entry that is not
		// registered now: the entry waits for it.
		if err := h.store.RegisterParent(ctx, entry.Parent); err != nil {
			return nil, err
		}
	}
	commitErr := h.store.SaveEntry(ctx, entry)
	if commitErr != nil && !catalogCommitted(commitErr) {
		return nil, commitErr
	}
	if len(isolate) > 0 {
		instance.scope = instance.scope.Isolate(isolate...)
	}
	instance.open()
	instance.instance = instance.plugin().Load(instance.scope, slices.Clone(entry.Config))
	h.mu.Lock()
	h.entries[entry.ID] = instance
	h.entrySpecs[entry.ID] = entry
	h.packages[entry.ID] = pkg.Ref
	h.resetRecoveryLocked(entry.ID)
	h.mu.Unlock()
	return instance, commitErr
}

// Disable stops the entry and the entries it owns and saves them as
// disabled.
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
	if err := h.requireStatic(id); err != nil {
		return err
	}
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
		if err != nil && (h.registered(entry.ID) || !live && !catalogCommitted(err)) {
			return result
		}
	}
	return result
}

func (h *Host) retireOne(ctx context.Context, id string, disable bool) error {
	h.mu.Lock()
	instance := h.entries[id]
	entry := h.entrySpecs[id]
	h.mu.Unlock()
	if instance == nil {
		return contract.Fail(contract.NotReady, "entry does not exist")
	}
	var persistErr error
	if disable {
		entry.Enabled = false
		persistErr = h.store.SaveEntry(ctx, entry)
		if persistErr != nil && !catalogCommitted(persistErr) {
			return persistErr
		}
		h.mu.Lock()
		h.entrySpecs[id] = entry
		h.mu.Unlock()
		instance.mu.Lock()
		instance.enabled = false
		instance.mu.Unlock()
	}
	h.cancelRecoveryTree(id)
	stopErr := h.waitOperation(func() error { return instance.retire(ctx) })
	instance.mu.Lock()
	closing := instance.closing
	instance.mu.Unlock()
	if closing != nil {
		// A gate taken down by an earlier call may still be going; the entry
		// is not out of the way before it has.
		select {
		case <-closing:
		case <-ctx.Done():
		}
	}
	finished := func(done <-chan struct{}) bool {
		select {
		case <-done:
			return true
		default:
			return done == nil
		}
	}
	if !finished(instance.instance.Done()) || !finished(closing) {
		// The caller gave up waiting, but the stop is submitted and final.
		// The registration goes when the entry and its run gate have
		// cleaned up; until then nothing else can take its place.
		go func() {
			<-instance.instance.Done()
			if closing != nil {
				<-closing
			}
			h.forget(id, instance)
		}()
		return errors.Join(stopErr, persistErr)
	}
	h.forget(id, instance)
	return errors.Join(stopErr, persistErr)
}

// forget drops the registration of a retired entry, unless the id has been
// registered anew since.
func (h *Host) forget(id string, instance *hostInstance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.entries[id] != instance {
		return
	}
	delete(h.entries, id)
	delete(h.packages, id)
	delete(h.entrySpecs, id)
}

// Uninstall removes ref from the store. It refuses a version that runs, and
// a selected version while entries of its package or of packages that need
// it are enabled. Removal.Pending means its files are not deleted yet;
// Reason says why, and RetryRemovals tries again.
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

// Replace selects ref and restarts every entry of its package, with the
// entries they own, on the new version.
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
			if h.registered(entries[n].ID) {
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
		instance, err := h.enable(ctx, entry)
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

// registered reports whether a persistent entry is still in the kernel.
func (h *Host) registered(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.entries[id] != nil
}

// gone reports whether the entry has left the kernel.
func (i *hostInstance) gone() bool {
	return i.instance.Status().State == cordis.Stopped
}

// Close stops every entry and in-process plugin in kernel order before it
// releases the store. Cancellation ends only this wait: Go cannot stop a
// callback or cleanup that does not cooperate, and what depends on it stays
// pending until it returns.
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
	for _, recovery := range h.recovery {
		h.recoveryStateLocked(recovery, RecoveryCanceled, nil)
	}
	h.mu.Unlock()
	if h.recoveryCancel != nil {
		h.recoveryCancel()
	}
	drained := false
	err := h.waitOperation(func() error {
		// Closing is submitted even when the caller stops waiting for it.
		// h.closed and the canceled coordinator already forbid new admissions.
		closeErr := h.app.Close(ctx)
		drained = ctx.Err() == nil || !errors.Is(closeErr, ctx.Err())
		if h.recoveryDone != nil {
			select {
			case <-h.recoveryDone:
			case <-ctx.Done():
				drained = false
				return errors.Join(closeErr, ctx.Err())
			}
		}
		return closeErr
	})
	if !drained {
		return err
	}
	storeErr := h.store.Close()
	h.doneOnce.Do(func() { close(h.done) })
	return errors.Join(err, storeErr)
}

// Status is the status of a registered entry, persistent or dynamic.
func (h *Host) Status(id string) (contract.Status, error) {
	instance, err := h.Instance(id)
	if err != nil {
		return contract.Status{}, err
	}
	return instance.Status(), nil
}

// Resume lets a temporarily stopped entry run again. Disable removes the
// entry; use Enable to create it again after Disable instead.
func (h *Host) Resume(ctx context.Context, id string) error {
	if err := h.lockOperation(ctx); err != nil {
		return err
	}
	release, err := h.reserveTree(id)
	if err != nil {
		h.unlockOperation()
		return err
	}
	if err := h.requireStatic(id); err != nil {
		release()
		h.unlockOperation()
		return err
	}
	h.mu.Lock()
	enabled := h.entrySpecs[id].Enabled
	instance := h.entries[id]
	h.mu.Unlock()
	if !enabled || instance == nil {
		release()
		h.unlockOperation()
		return contract.Fail(contract.NotReady, "entry is persistently disabled")
	}
	instance.open()
	h.resetRecovery(id)
	release()
	h.unlockOperation()
	if instance.instance.Status().State == cordis.Failed {
		// A stopped entry does not keep an old failure.
		return instance.instance.Restart(ctx)
	}
	return instance.Wait(ctx)
}
