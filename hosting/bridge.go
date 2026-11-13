package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

// hostFeatures are the session features this host implements.
var hostFeatures = []string{"cancel", "parent", "events", "children", "subscriptions", "streams", "chains"}

// run is one activation of a native entry: its process, its session and
// what the guest registered through it.
type run struct {
	host     *Host
	instance *hostInstance
	identity contract.Identity
	parent   string    // the parent's activation, when the parent is native
	began    time.Time // when the activation started starting
	startup  time.Duration
	scope    *cordis.Scope
	session  contract.Session
	slots    chan struct{} // bounds the requests in flight to the guest

	mu          sync.Mutex
	stopping    bool  // the activation cleans up; nothing new is registered
	initialized bool  // the guest answered initialize
	failure     error // what the guest reported before it ended the session
	last        uint64
	listened    map[string]*listener
	chains      chainCalls
}

// start is the Start of the plugin that represents a native entry. Each
// activation owns one process.
func (i *hostInstance) start(ctx context.Context, s *cordis.Scope, config json.RawMessage) error {
	h := i.host
	if err := contract.ValidateFrameBytes(h.options.FrameBytes); err != nil {
		return err
	}
	if err := contract.ValidateInflight(h.options.Inflight); err != nil {
		return err
	}
	owner := contract.Identity{Package: i.pkg.Ref.ID, Entry: i.id, Digest: i.pkg.Ref.Digest, Activation: contract.NewID()}
	r := &run{host: h, instance: i, identity: owner, began: time.Now(), scope: s, listened: map[string]*listener{},
		chains: chainCalls{stops: map[string]func(){}, nexts: map[string]*chainCall{}},
		// Subscription control shares the session's limit with business requests.
		slots: make(chan struct{}, h.options.Inflight-8)}
	i.begin(r)
	// Cleanups run in reverse: the session ends, the package snapshot is
	// released, and the run is recorded last.
	beginErr := h.store.BeginRun(ctx, owner)
	if beginErr == nil || catalogCommitted(beginErr) {
		s.Defer(func(context.Context) error { return i.endRun(r) })
	}
	if beginErr != nil {
		if !catalogCommitted(beginErr) {
			// No run was recorded, so endRun will not report this one.
			h.logActivation(r, slog.LevelError, "activation failed", "stage", "starting", "error", beginErr)
		}
		return beginErr
	}
	s.Defer(func(context.Context) error { return h.forgetChildren(owner) })
	if i.parent != "" {
		parent, err := parentKey(i.parent).Get(s)
		if err != nil {
			return err
		}
		r.parent = parent.Activation
	}
	// A package built for another protocol never gets a process.
	minor, err := i.pkg.Manifest.Protocol.Negotiate(contract.Protocol{Major: contract.ProtocolMajor})
	if err != nil {
		return err
	}
	for _, required := range i.pkg.Manifest.Protocol.RequiredFeatures {
		if !slices.Contains(hostFeatures, required) {
			return contract.Fail(contract.Incompatible, "host lacks required session feature: "+required)
		}
	}
	snapshot, err := h.store.Snapshot(ctx, i.pkg.Ref)
	if err != nil {
		// Denied here is the store's own check of trust and integrity, never
		// something the plugin said.
		if contract.ErrorCode(err) == contract.Denied {
			i.refuse(r)
		}
		return err
	}
	s.Defer(func(context.Context) error { return snapshot.Close() })
	artifact, err := packagehost.Artifact(i.pkg.Manifest, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	bound := make([]contract.Service, 0, len(i.spec.Requires))
	for _, consumer := range i.spec.Requires {
		required := consumer.Requirement
		provider, err := contract.EndpointKey(required.Name, required.Major).Get(s)
		if err != nil {
			return err
		}
		bound = append(bound, provider.Service())
	}
	session, err := h.options.Runtime.Start(ctx, contract.Launch{Identity: owner, Directory: snapshot.Directory, Artifact: artifact,
		Grace: h.options.Grace, FrameBytes: h.options.FrameBytes, Inflight: h.options.Inflight, Stderr: h.options.Stderr, Handle: r.handle, Stream: r.stream})
	if err != nil {
		return err
	}
	r.session = session
	s.Defer(r.shutdown)
	// Until initialize has succeeded there is nothing to shut down in the
	// guest; the cleanup above then only closes the session.
	registration, err := r.initialize(ctx, contract.Initialize{Identity: owner, Definition: i.spec.ID, Config: config,
		FrameBytes: h.options.FrameBytes, Inflight: h.options.Inflight, Protocol: contract.Protocol{Major: contract.ProtocolMajor, MaxMinor: minor},
		Services: bound, Features: hostFeatures, Provides: append([]contract.Service{}, i.spec.Provides...)})
	if err != nil {
		return r.reported(err)
	}
	r.mu.Lock()
	r.initialized = true
	r.mu.Unlock()
	if registration.FrameBytes != h.options.FrameBytes {
		return contract.Fail(contract.Invalid, "registration frameBytes mismatch")
	}
	if registration.Minor != minor {
		return contract.Fail(contract.Incompatible, "plugin selected incompatible protocol minor")
	}
	for _, required := range i.pkg.Manifest.Protocol.RequiredFeatures {
		if !slices.Contains(registration.Features, required) {
			return contract.Fail(contract.Incompatible, "plugin omitted required feature: "+required)
		}
	}
	declared := map[string]contract.Service{}
	for _, service := range i.spec.Provides {
		declared[service.Key()] = service
	}
	for _, service := range registration.Services {
		if expected, ok := declared[service.Key()]; !ok || expected != service {
			return contract.Fail(contract.Invalid, "plugin registered undeclared service")
		}
		delete(declared, service.Key())
	}
	if len(declared) > 0 {
		return contract.Fail(contract.Invalid, "plugin omitted declared service")
	}
	for _, service := range registration.Services {
		if err := contract.EndpointKey(service.Name, service.Major).Provide(s, contract.Endpoint(&guestEndpoint{run: r, service: service})); err != nil {
			return err
		}
	}
	// The session ending on its own is the failure of this activation.
	s.Go(func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return nil
		case <-session.Done():
			return r.reported(sessionError(session))
		}
	})
	// Persistent entries that name this one as their parent may start now.
	if err := parentKey(i.id).Provide(s, owner); err != nil {
		return err
	}
	r.startup = time.Since(r.began)
	h.logActivation(r, slog.LevelInfo, "activation started", "startup", r.startup)
	return nil
}

// logActivation writes a record that names the activation it is about, so
// it can be matched with Status and the store's run history.
func (h *Host) logActivation(r *run, level slog.Level, message string, attrs ...any) {
	if h.options.Logger == nil {
		return
	}
	attrs = append([]any{"package", r.identity.Package, "entry", r.identity.Entry,
		"activation", r.identity.Activation, "digest", r.identity.Digest}, attrs...)
	if r.instance.parent != "" {
		attrs = append(attrs, "parent", r.instance.parent)
	}
	if r.parent != "" {
		attrs = append(attrs, "parentActivation", r.parent)
	}
	h.options.Logger.Log(context.Background(), level, message, attrs...)
}

func sessionError(session contract.Session) error {
	if err := session.Err(); err != nil {
		return err
	}
	return contract.Fail(contract.PluginFailed, "plugin session ended")
}

// reported prefers what the guest said about its failure over the
// transport error that followed it.
func (r *run) reported(err error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failure != nil {
		return r.failure
	}
	return err
}

// gone turns the error of a request the session ended under into what its
// caller sees: not_ready, with the reason the entry went away. One code for
// every way of going away lets a caller tell "try again when it is back"
// from a failure the provider answered with. Other errors pass unchanged.
func (r *run) gone(err error) error {
	closed, ok := errors.AsType[*contract.ClosedError](err)
	if !ok {
		return err
	}
	return &contract.Error{Code: contract.NotReady, Message: fmt.Sprintf("entry %q stopped during the call: %v", r.identity.Entry, r.reason(closed.Err))}
}

// reason is why the session ended. What the guest reported comes first.
// The transport only knows its stream closed; a native session learns how
// the process exited once it has reaped it, so an unrequested end waits up
// to a grace for that.
func (r *run) reason(transport error) error {
	r.mu.Lock()
	failure, stopping := r.failure, r.stopping
	r.mu.Unlock()
	if failure != nil {
		return failure
	}
	if stopping {
		// The host ended the session, or it ended and the host is cleaning
		// up after it; then its reason may already be known.
		select {
		case <-r.session.Done():
			if err := r.session.Err(); err != nil {
				return err
			}
		default:
		}
		return transport
	}
	timer := time.NewTimer(r.host.options.Grace)
	defer timer.Stop()
	select {
	case <-r.session.Done():
		if err := r.session.Err(); err != nil {
			return err
		}
	case <-timer.C:
	}
	return transport
}

func (r *run) call(ctx context.Context, method string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	data, err = r.session.Call(ctx, method, data)
	if err != nil || output == nil {
		return err
	}
	return contract.Decode(data, output)
}

func (r *run) initialize(ctx context.Context, input contract.Initialize) (contract.Registration, error) {
	var output contract.Registration
	err := r.call(ctx, "initialize", input, &output)
	return output, err
}

// shutdown ends the session when the activation cleans up. By then the
// consumers of its services have finished their own cleanup.
func (r *run) shutdown(ctx context.Context) error {
	r.mu.Lock()
	r.stopping = true
	initialized := r.initialized
	r.mu.Unlock()
	var shutdownErr error
	select {
	case <-r.session.Done():
	default:
		if initialized {
			shutdownCtx, cancel := context.WithTimeout(ctx, r.host.options.Grace)
			shutdownErr = r.call(shutdownCtx, "shutdown", struct{}{}, nil)
			cancel()
			if errors.Is(shutdownErr, context.DeadlineExceeded) && ctx.Err() == nil {
				// Not the caller's deadline: say whose it was.
				shutdownErr = contract.Fail(contract.Timeout, fmt.Sprintf("entry %q did not shut down within %v", r.identity.Entry, r.host.options.Grace))
			}
		}
	}
	closeErr := r.session.Close()
	if !initialized {
		// The start was given up or refused; how the session ends after
		// that is not a failure to clean up.
		closeErr = nil
	}
	process, native := r.session.(contract.ProcessSession)
	if !native {
		return errors.Join(shutdownErr, closeErr)
	}
	outcome := process.Outcome()
	r.instance.setOutcome(r, outcome)
	// A transport error from a process that already failed is that failure,
	// not a second failure to clean up.
	if outcome.FailureCode != "" {
		if shutdownErr != nil && errors.Is(r.session.Err(), shutdownErr) {
			shutdownErr = nil
		}
		if outcome.CleanupCode == "" {
			closeErr = nil
		}
	}
	return errors.Join(shutdownErr, closeErr)
}

// acquire takes one of the session's slots for a request to the guest.
func (r *run) acquire(ctx context.Context) (release func(), err error) {
	select {
	case r.slots <- struct{}{}:
		return func() { <-r.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.session.Done():
		return nil, contract.Fail(contract.NotReady, "plugin session is closed")
	}
}

// tryAcquire takes a slot only if one is free. The hops of a chain
// execution hold their requests open while later hops of the same
// execution need more; waiting for a slot there could wait for itself.
func (r *run) tryAcquire() (release func(), err error) {
	select {
	case r.slots <- struct{}{}:
		return func() { <-r.slots }, nil
	default:
		return nil, contract.Fail(contract.NotReady, "session is at its limit of requests in flight")
	}
}

// guestEndpoint forwards the calls of one registered service to the guest.
type guestEndpoint struct {
	run     *run
	service contract.Service
}

// Service is the registered service the endpoint forwards to.
func (e *guestEndpoint) Service() contract.Service { return e.service }

// observe reports a finished call to the host's observer.
func (e *guestEndpoint) observe(method string, stream bool, started time.Time, items int, err error) {
	if report := e.run.host.options.OnCall; report != nil {
		report(CallRecord{Entry: e.run.identity.Entry, Activation: e.run.identity.Activation, Service: e.service,
			Method: method, Stream: stream, Items: items, Duration: time.Since(started), Err: err})
	}
}

// Call forwards a call to the guest once a session slot is free and reports it
// to OnCall. A call the session ended under fails with not_ready.
func (e *guestEndpoint) Call(ctx context.Context, method string, input json.RawMessage) (output json.RawMessage, err error) {
	defer func(started time.Time) { e.observe(method, false, started, 0, err) }(time.Now())
	release, err := e.run.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if err = e.run.call(ctx, "call", contract.Invocation{Service: e.service, Method: method, Input: input}, &output); err != nil {
		err = e.run.gone(err)
	}
	return output, err
}

// Stream forwards a streaming call to the guest.
func (e *guestEndpoint) Stream(ctx context.Context, method string, input json.RawMessage) iter.Seq2[json.RawMessage, error] {
	return func(yield func(json.RawMessage, error) bool) {
		var failure error
		items := 0
		defer func(started time.Time) { e.observe(method, true, started, items, failure) }(time.Now())
		data, err := json.Marshal(contract.Invocation{Service: e.service, Method: method, Input: input})
		if err != nil {
			failure = err
			yield(nil, err)
			return
		}
		// A stream holds one of the session's slots for as long as it runs.
		release, err := e.run.acquire(ctx)
		if err != nil {
			failure = err
			yield(nil, err)
			return
		}
		defer release()
		for item, err := range e.run.session.Stream(ctx, "call", data) {
			if err != nil {
				err = e.run.gone(err)
				failure = err
				yield(nil, err)
				return
			}
			items++
			if !yield(item, nil) {
				return
			}
		}
	}
}

// requesterKey marks the ctx of a request the host serves for a native
// activation.
type requesterKey struct{}

// Requester reports the native activation that sent the request ctx belongs
// to. The host sets it for everything it serves a plugin: the calls and
// streams of the services it requires, the events and queries it emits,
// its chains and its child operations. The ctx carries it on to whatever
// the handler calls with it; when that reaches another plugin, which calls
// the host in turn, that plugin is the requester of its own request. Calls
// that the application or an in-process plugin starts report false. It is
// information: Cordis authorizes nothing with it.
func Requester(ctx context.Context) (contract.Identity, bool) {
	identity, ok := ctx.Value(requesterKey{}).(contract.Identity)
	return identity, ok
}

// stream serves the guest's streaming calls of the services it requires.
func (r *run) stream(ctx context.Context, method string, input json.RawMessage) iter.Seq2[json.RawMessage, error] {
	ctx = context.WithValue(ctx, requesterKey{}, r.identity)
	var request contract.ServiceCall
	if err := contract.Decode(input, &request); err != nil {
		return contract.FailedStream(err)
	}
	if method != "call" {
		return contract.FailedStream(contract.Fail(contract.Invalid, "unknown guest stream method"))
	}
	provider, err := contract.EndpointKey(request.Required.Name, request.Required.Major).Get(r.scope)
	if err != nil {
		return contract.FailedStream(contract.Fail(contract.NotReady, err.Error()))
	}
	if !request.Required.Accepts(provider.Service()) {
		return contract.FailedStream(&contract.VersionError{Required: request.Required, Visible: provider.Service()})
	}
	return provider.Stream(ctx, request.Method, request.Input)
}

// handle serves the requests of the guest on behalf of the entry: they act
// with the entry's bindings and end with its activation.
func (r *run) handle(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	ctx = context.WithValue(ctx, requesterKey{}, r.identity)
	switch method {
	case "call":
		var request contract.ServiceCall
		if err := contract.Decode(input, &request); err != nil {
			return nil, err
		}
		provider, err := contract.EndpointKey(request.Required.Name, request.Required.Major).Get(r.scope)
		if err != nil {
			return nil, contract.Fail(contract.NotReady, err.Error())
		}
		if !request.Required.Accepts(provider.Service()) {
			return nil, &contract.VersionError{Required: request.Required, Visible: provider.Service()}
		}
		return provider.Call(ctx, request.Method, request.Input)
	case "event":
		var request contract.Event
		if err := contract.Decode(input, &request); err != nil {
			return nil, err
		}
		result, err := dispatch(ctx, r.scope, request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "event.subscribe":
		var spec contract.SubscriptionSpec
		if err := contract.Decode(input, &spec); err != nil {
			return nil, err
		}
		if err := r.subscribe(spec); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	case "event.unsubscribe":
		var request contract.SubscriptionCancel
		if err := contract.Decode(input, &request); err != nil {
			return nil, err
		}
		if err := r.unsubscribe(ctx, request.ID); err != nil {
			return nil, err
		}
		return json.Marshal(contract.SubscriptionState{Inactive: true})
	case "chain.use":
		var spec contract.SubscriptionSpec
		if err := contract.Decode(input, &spec); err != nil {
			return nil, err
		}
		if err := r.useChain(spec); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	case "chain.unuse":
		var request contract.SubscriptionCancel
		if err := contract.Decode(input, &request); err != nil {
			return nil, err
		}
		if err := r.unuseChain(request.ID); err != nil {
			return nil, err
		}
		return json.Marshal(contract.SubscriptionState{Inactive: true})
	case "chain.next":
		var step contract.ChainStep
		if err := contract.Decode(input, &step); err != nil {
			return nil, err
		}
		return r.nextChain(ctx, step)
	case "chain.do":
		var request contract.ChainDo
		if err := contract.Decode(input, &request); err != nil {
			return nil, err
		}
		return r.doChain(ctx, request)
	case "child":
		return r.host.handleChild(ctx, r, input)
	case "failed":
		var failure contract.Failure
		if err := contract.Decode(input, &failure); err != nil {
			return nil, err
		}
		r.mu.Lock()
		if r.failure == nil {
			r.failure = &failure.Error
		}
		r.mu.Unlock()
		return json.RawMessage(`{}`), nil
	}
	return nil, contract.Fail(contract.Invalid, "unknown guest method")
}

// dispatch runs the listeners of an event, or the handlers of a query, that
// are reachable from s.
func dispatch(ctx context.Context, s *cordis.Scope, event contract.Event) (contract.EventResult, error) {
	if event.Name == "" || len(event.Input) == 0 {
		return contract.EventResult{}, contract.Fail(contract.Invalid, "event name and input are required")
	}
	switch {
	case event.Kind == contract.KindEvent && event.Mode == contract.Sequential:
		return contract.EventResult{}, contract.RawEvent(event.Name).Emit(ctx, s, event.Input)
	case event.Kind == contract.KindEvent && event.Mode == contract.Parallel:
		return contract.EventResult{}, contract.RawEvent(event.Name).EmitParallel(ctx, s, event.Input)
	case event.Kind == contract.KindQuery && event.Mode == contract.First:
		value, handled, err := contract.RawQuery(event.Name).Ask(ctx, s, event.Input)
		return contract.EventResult{Handled: handled, Value: value}, err
	}
	return contract.EventResult{}, contract.Fail(contract.Invalid, "event kind and mode do not match")
}

// listener is one subscription of the guest, registered in the host kernel.
type listener struct {
	stop     func()
	mu       sync.Mutex
	canceled bool
	sending  map[chan struct{}]struct{} // admitted deliveries not yet queued on the session
}

// admit decides whether a call the kernel passed on is delivered. It is the
// one place where a delivery and a cancellation are ordered.
func (l *listener) admit() (queued chan struct{}, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.canceled {
		return nil, false
	}
	queued = make(chan struct{})
	l.sending[queued] = struct{}{}
	return queued, true
}

func (r *run) subscribe(spec contract.SubscriptionSpec) error {
	number, err := contract.SubscriptionNumber(r.identity.Activation, spec.ID)
	if err != nil {
		return err
	}
	if spec.Name == "" {
		return contract.Fail(contract.Invalid, "event name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return contract.Fail(contract.NotReady, "activation is stopping")
	}
	if number <= r.last {
		return contract.Fail(contract.Invalid, "subscription sequence already used")
	}
	if len(r.listened)+len(r.chains.stops) >= contract.MaxSubscriptions {
		return contract.Fail(contract.Denied, "registration quota exceeded")
	}
	r.last = number
	l := &listener{sending: map[chan struct{}]struct{}{}}
	deliver := func(ctx context.Context, input json.RawMessage) (contract.EventResult, error) {
		queued, admitted := l.admit()
		if !admitted {
			return contract.EventResult{}, nil
		}
		return r.deliver(ctx, spec, l, queued, input)
	}
	var options []cordis.ListenOption
	if spec.Options.Once {
		options = append(options, cordis.Once)
	}
	if spec.Options.Prepend {
		options = append(options, cordis.Prepend)
	}
	switch spec.Kind {
	case contract.KindEvent:
		l.stop = contract.RawEvent(spec.Name).On(r.scope, func(ctx context.Context, input json.RawMessage) error {
			_, err := deliver(ctx, input)
			return err
		}, options...)
	case contract.KindQuery:
		l.stop = contract.RawQuery(spec.Name).Handle(r.scope, func(ctx context.Context, input json.RawMessage) (json.RawMessage, bool, error) {
			result, err := deliver(ctx, input)
			return result.Value, result.Handled, err
		}, options...)
	default:
		return contract.Fail(contract.Invalid, "unknown subscription kind")
	}
	r.listened[spec.ID] = l
	return nil
}

// deliver sends one admitted call to the guest.
func (r *run) deliver(ctx context.Context, spec contract.SubscriptionSpec, l *listener, queued chan struct{}, input json.RawMessage) (contract.EventResult, error) {
	sent := false
	settle := func(queuedFrame bool) {
		sent = queuedFrame
		l.mu.Lock()
		delete(l.sending, queued)
		l.mu.Unlock()
		close(queued)
		if spec.Options.Once {
			r.forget(spec.ID, l)
		}
	}
	var result contract.EventResult
	release, err := r.acquire(ctx)
	if err != nil {
		settle(false)
	} else {
		defer release()
		var data []byte
		if data, err = json.Marshal(contract.EventDelivery{ID: spec.ID, Input: input}); err != nil {
			settle(false)
		} else if data, err = r.session.CallWithReceipt(ctx, "event", data, settle); err != nil {
			err = r.gone(err)
		} else {
			var answer struct {
				Handled *bool           `json:"handled"`
				Value   json.RawMessage `json:"value"`
			}
			if err = contract.Decode(data, &answer); err == nil && spec.Kind == contract.KindQuery {
				if answer.Handled == nil {
					err = contract.Fail(contract.Invalid, "query result requires handled")
				} else {
					result = contract.EventResult{Handled: *answer.Handled, Value: answer.Value}
				}
			}
		}
	}
	if spec.Options.Once && !sent {
		// The kernel consumed the registration but the guest never saw the
		// call; let it release the callback.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.host.options.Grace)
		defer cancel()
		var state contract.SubscriptionState
		if cleanupErr := r.call(cleanup, "event.unsubscribe", contract.SubscriptionCancel{ID: spec.ID}, &state); cleanupErr != nil {
			select {
			case <-r.session.Done():
			default:
				r.fail(fmt.Errorf("release undelivered once subscription: %w", cleanupErr))
			}
		}
	}
	return result, err
}

// fail ends the session because the two sides no longer agree.
func (r *run) fail(err error) {
	r.mu.Lock()
	if r.failure == nil {
		r.failure = err
	}
	r.mu.Unlock()
	r.session.Close()
}

func (r *run) forget(id string, l *listener) {
	r.mu.Lock()
	if r.listened[id] == l {
		delete(r.listened, id)
	}
	r.mu.Unlock()
}

// unsubscribe stops admitting calls and waits until the admitted ones are
// queued on the session, so that the guest sees them before the
// confirmation. It does not wait for the callbacks themselves.
func (r *run) unsubscribe(ctx context.Context, id string) error {
	if _, err := contract.SubscriptionNumber(r.identity.Activation, id); err != nil {
		return err
	}
	r.mu.Lock()
	l := r.listened[id]
	r.mu.Unlock()
	if l == nil {
		return nil
	}
	l.mu.Lock()
	l.canceled = true
	sending := make([]chan struct{}, 0, len(l.sending))
	for queued := range l.sending {
		sending = append(sending, queued)
	}
	l.mu.Unlock()
	l.stop()
	for _, queued := range sending {
		select {
		case <-queued:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.forget(id, l)
	return nil
}

// Keys of the private services that tie entries to the host and to their
// parents. The prefix is reserved: packages cannot declare services under it.
const reservedPrefix = "cordis.host/"

// runKey is provided by the host while an entry is not temporarily stopped.
func runKey(entry string) cordis.Service[struct{}] {
	return cordis.NewService[struct{}](reservedPrefix + "run:" + entry)
}

// parentKey is provided by whatever acts as the parent with this ID: a
// native entry, or an in-process plugin through Host.ProvideParent.
func parentKey(id string) cordis.Service[contract.Identity] {
	return cordis.NewService[contract.Identity](reservedPrefix + "parent:" + id)
}
