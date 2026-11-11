package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/wire"
)

// guest is one plugin process: a session with the host and the kernel the
// plugin runs in. It is also the uplink of the keys in package contract.
type guest struct {
	definitions map[string]Definition
	endpoint    *wire.Endpoint
	ready       chan struct{} // closed once endpoint is set
	slots       chan struct{} // bounds the requests in flight to the host

	// Set by initialize, before any plugin code runs.
	identity contract.Identity
	features []string // what the host supports
	app      *cordis.App
	chains   chains

	starts   sync.RWMutex // held for reading until a request made while starting is queued
	mu       sync.Mutex
	starting context.Context // the initialize request, while the plugin starts
	closing  bool
	active   sync.WaitGroup // host calls and deliveries being handled
	sequence uint64
	events   map[string]*subscription
	failure  error // why the plugin failed; reported by serve

	register sync.Mutex // subscriptions reach the host in the order of their numbers
	stopOnce sync.Once
	stopErr  error
	answered bool // the start failure went out as the answer to initialize
}

type subscription struct {
	id      string
	once    bool
	handler contract.EventHandler
}

func serve(ctx context.Context, definitions map[string]Definition, transport io.ReadWriteCloser) error {
	limits := wire.DefaultLimits()
	g := &guest{definitions: definitions, ready: make(chan struct{}), events: map[string]*subscription{},
		chains: chains{middlewares: map[string]contract.RawMiddleware{}, finals: map[string]contract.RawHandler{}},
		// Subscription control shares the session's limit with business requests.
		slots: make(chan struct{}, limits.Inflight-8)}
	endpoint, err := wire.New(wire.Options{Transport: transport, Guest: true, Handler: g.handle, Prepare: g.prepare, Stream: g.stream, Limits: limits})
	if err != nil {
		return err
	}
	g.endpoint = endpoint
	close(g.ready)
	select {
	case <-ctx.Done():
	case <-endpoint.Done():
	}
	// Whatever ended the session, the plugin's cleanups run before the
	// process goes away.
	cleanupErr := g.stop(context.WithoutCancel(ctx))
	endpoint.Close()
	endpoint.Wait()
	err = endpoint.Err()
	if errors.Is(err, io.EOF) {
		err = nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return errors.Join(err, cleanupErr, g.failure)
}

// stop closes the kernel once: no new work is admitted, what is in flight
// finishes, then every instance cleans up.
func (g *guest) stop(ctx context.Context) error {
	g.stopOnce.Do(func() {
		g.mu.Lock()
		g.closing = true
		clear(g.events)
		g.mu.Unlock()
		g.active.Wait()
		if g.app != nil {
			err := g.app.Close(ctx)
			g.mu.Lock()
			g.stopErr = err
			g.mu.Unlock()
		}
	})
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.answered {
		return nil
	}
	return g.stopErr
}

// admit registers one host request that runs plugin code.
func (g *guest) admit() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing || g.app == nil {
		return contract.Fail(contract.NotReady, "plugin is not active")
	}
	g.active.Add(1)
	return nil
}

func (g *guest) handle(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	select {
	case <-g.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	switch method {
	case "initialize":
		return g.initialize(ctx, input)
	case "shutdown":
		if err := g.stop(ctx); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	case "call":
		var invocation contract.Invocation
		if err := contract.Decode(input, &invocation); err != nil {
			return nil, err
		}
		if err := g.admit(); err != nil {
			return nil, err
		}
		defer g.active.Done()
		key := contract.EndpointKey(invocation.Service.Name, invocation.Service.Major)
		provided, err := key.Get(g.app.Scope())
		if err != nil || provided == nil || provided.Service().Minor < invocation.Service.Minor {
			return nil, contract.Fail(contract.NotReady, "service "+invocation.Service.Key()+" is not provided")
		}
		return provided.Call(ctx, invocation.Method, invocation.Input)
	case "chain.invoke":
		return g.invoke(ctx, input)
	case "chain.final":
		return g.final(ctx, input)
	case "event.unsubscribe":
		// The host lets go of a subscription it could not deliver to.
		var request contract.SubscriptionCancel
		if err := contract.Decode(input, &request); err != nil {
			return nil, err
		}
		if _, err := contract.SubscriptionNumber(g.identity.Activation, request.ID); err != nil {
			return nil, err
		}
		g.mu.Lock()
		delete(g.events, request.ID)
		g.mu.Unlock()
		return json.Marshal(contract.SubscriptionState{Inactive: true})
	}
	return nil, contract.Fail(contract.Invalid, "unknown plugin method")
}

func (g *guest) initialize(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var init contract.Initialize
	if err := contract.Decode(input, &init); err != nil {
		return nil, err
	}
	minor, err := init.Protocol.Negotiate(capabilities.Protocol)
	if err != nil {
		return nil, err
	}
	for _, feature := range capabilities.Protocol.RequiredFeatures {
		if !slices.Contains(init.Features, feature) {
			return nil, contract.Fail(contract.Incompatible, "host lacks required session feature: "+feature)
		}
	}
	definition, exists := g.definitions[init.Definition]
	if !exists {
		return nil, contract.Fail(contract.Invalid, "unknown plugin definition")
	}
	g.mu.Lock()
	if g.app != nil || g.closing {
		g.mu.Unlock()
		return nil, contract.Fail(contract.NotReady, "plugin is already initialized or closing")
	}
	g.identity, g.features = init.Identity, init.Features
	g.app = cordis.New(cordis.Options{})
	g.starting = ctx
	g.mu.Unlock()
	defer g.started()
	root := g.app.Scope()
	if err := contract.UplinkKey.Provide(root, contract.Uplink(g)); err != nil {
		return nil, err
	}
	for _, service := range init.Services {
		key := contract.EndpointKey(service.Name, service.Major)
		if err := key.Provide(root, contract.Endpoint(&dependency{guest: g, service: service})); err != nil {
			return nil, err
		}
	}
	instance, err := definition.load(root, init.Config)
	if err != nil {
		return nil, err
	}
	if err := instance.Wait(ctx); err != nil {
		// The failure is the answer to initialize. Clean up now, so that the
		// shutdown that follows does not report it a second time as a
		// failure to clean up.
		g.mu.Lock()
		g.answered = true
		g.mu.Unlock()
		g.stop(context.WithoutCancel(ctx))
		return nil, reportable(err)
	}
	if status := instance.Status(); status.State != cordis.Active {
		// It failed between starting and being asked, for example in its
		// managed work.
		g.mu.Lock()
		g.answered = true
		g.mu.Unlock()
		g.stop(context.WithoutCancel(ctx))
		if status.Err != nil {
			return nil, reportable(status.Err)
		}
		return nil, contract.Fail(contract.NotReady, fmt.Sprintf("plugin is %s after start", status.State))
	}
	// What is not declared stays in this process.
	for _, declared := range init.Provides {
		provided, err := contract.EndpointKey(declared.Name, declared.Major).Get(root)
		if err != nil || provided == nil {
			return nil, contract.Fail(contract.Invalid, "plugin did not provide declared service "+declared.Key())
		}
		if provided.Service() != declared {
			return nil, contract.Fail(contract.Invalid, fmt.Sprintf("plugin provides %s minor %d, declared minor %d",
				declared.Key(), provided.Service().Minor, declared.Minor))
		}
	}
	// A failure after this point ends the session with its reason.
	id := instance.Info().ID
	cordis.InstanceChanged.On(root, func(_ context.Context, change cordis.InstanceChange) error {
		if change.Instance.ID == id && change.To == cordis.Failed {
			go g.fail(change.Err)
		}
		return nil
	})
	if status := instance.Status(); status.State == cordis.Failed {
		go g.fail(status.Err)
	}
	return json.Marshal(contract.Registration{FrameBytes: init.FrameBytes, Minor: minor,
		Features: capabilities.Features, Services: init.Provides})
}

// reportable keeps the text of a plugin's error on its way to the host,
// which otherwise sees only that a handler failed.
func reportable(err error) error {
	if _, coded := errors.AsType[*contract.Error](err); coded || err == nil {
		return err
	}
	return &contract.Error{Code: contract.ErrorCode(err), Message: err.Error()}
}

// fail reports why the plugin failed and ends the session.
func (g *guest) fail(cause error) {
	g.mu.Lock()
	first := g.failure == nil
	if first {
		g.failure = fmt.Errorf("plugin failed: %w", cause)
	}
	g.mu.Unlock()
	if !first {
		return
	}
	message := "plugin failed"
	if cause != nil {
		message = cause.Error()
	}
	if data, err := json.Marshal(contract.Failure{Error: contract.Error{Code: contract.ErrorCode(cause), Message: message}}); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), wire.DefaultLimits().Grace)
		// The host takes the report once it has the answer to initialize.
		select {
		case <-g.endpoint.Ready():
			g.endpoint.Call(ctx, "failed", data)
		case <-ctx.Done():
		case <-g.endpoint.Done():
		}
		cancel()
	}
	g.endpoint.Close()
}

// prepare takes the callback of a delivery while the reader still holds the
// frame, so that a cancellation confirmed right after cannot lose it.
func (g *guest) prepare(_ context.Context, method string, input json.RawMessage) (wire.Handler, error) {
	if method != "event" {
		return nil, nil
	}
	var delivery contract.EventDelivery
	if err := contract.Decode(input, &delivery); err != nil {
		return nil, err
	}
	if _, err := contract.SubscriptionNumber(g.identity.Activation, delivery.ID); err != nil {
		return nil, err
	}
	if len(delivery.Input) == 0 {
		return nil, contract.Fail(contract.Invalid, "event input is required")
	}
	g.mu.Lock()
	sub := g.events[delivery.ID]
	if sub != nil && sub.once {
		delete(g.events, delivery.ID)
	}
	g.mu.Unlock()
	if sub == nil {
		return nil, contract.Fail(contract.Invalid, "unknown event subscription")
	}
	// The session does not call this for a request that was canceled before
	// its turn, so everything that must be undone starts inside it.
	return func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		if err := g.admit(); err != nil {
			return nil, err
		}
		defer g.active.Done()
		result, err := sub.handler(ctx, delivery.Input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}, nil
}

// started ends the phase in which requests belong to initialize. It runs
// when the plugin's Start returns, before its managed work begins: a
// request tied to initialize is canceled when initialize is answered. It
// waits until every such request is queued on the session, so that none of
// them reaches the host after the answer that ends their parent.
func (g *guest) started() {
	g.starts.Lock()
	g.mu.Lock()
	g.starting = nil
	g.mu.Unlock()
	g.starts.Unlock()
}

// admitted returns the context to send a request with, once the session
// takes it. While the plugin starts, its requests are part of initialize.
// After that the session opens when initialize has been answered, which is
// slightly later than the plugin becoming Active: managed work that calls
// the host right away waits here.
//
// queued must be called once the request is on the session, or was never
// sent.
func (g *guest) admitted(ctx context.Context) (_ context.Context, queued func(), err error) {
	g.starts.RLock()
	g.mu.Lock()
	starting := g.starting
	g.mu.Unlock()
	if starting != nil {
		var once sync.Once
		return wire.WithRequest(ctx, starting), func() { once.Do(g.starts.RUnlock) }, nil
	}
	g.starts.RUnlock()
	select {
	case <-g.endpoint.Ready():
		return ctx, func() {}, nil
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-g.endpoint.Done():
		return nil, nil, contract.Fail(contract.NotReady, "session is closed")
	}
}

// request sends one business request to the host. It waits for a free slot
// instead of failing when the session is at its limit.
func (g *guest) request(ctx context.Context, method string, input any, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if strings.HasPrefix(method, "chain.") {
		// A chain execution holds its requests open while other hops of the
		// same execution need more of them; waiting here could wait for
		// itself, so a full session refuses instead.
		select {
		case g.slots <- struct{}{}:
			defer func() { <-g.slots }()
		default:
			return contract.Fail(contract.NotReady, "session is at its limit of requests in flight")
		}
	} else {
		select {
		case g.slots <- struct{}{}:
			defer func() { <-g.slots }()
		case <-ctx.Done():
			return ctx.Err()
		case <-g.endpoint.Done():
			return contract.Fail(contract.NotReady, "session is closed")
		}
	}
	ctx, queued, err := g.admitted(ctx)
	if err != nil {
		return err
	}
	data, err = g.endpoint.CallWithReceipt(ctx, method, data, func(bool) { queued() })
	if err != nil || output == nil {
		return err
	}
	return contract.Decode(data, output)
}

// dependency forwards the calls of a required service to the host.
type dependency struct {
	guest   *guest
	service contract.Service
}

func (d *dependency) Service() contract.Service { return d.service }

func (d *dependency) Call(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	var output json.RawMessage
	err := d.guest.request(ctx, "call", contract.ServiceCall{
		Required: contract.Requirement{Name: d.service.Name, Major: d.service.Major, MaxMinor: d.service.Minor},
		Method:   method, Input: input,
	}, &output)
	return output, err
}

// Stream forwards a streaming call of a required service to the host.
func (d *dependency) Stream(ctx context.Context, method string, input json.RawMessage) iter.Seq2[json.RawMessage, error] {
	return func(yield func(json.RawMessage, error) bool) {
		data, err := json.Marshal(contract.ServiceCall{
			Required: contract.Requirement{Name: d.service.Name, Major: d.service.Major, MaxMinor: d.service.Minor},
			Method:   method, Input: input,
		})
		if err == nil {
			// A stream outlives the start of the plugin, so it cannot be
			// part of initialize: it begins once the session is open.
			select {
			case <-d.guest.endpoint.Ready():
			case <-ctx.Done():
				err = ctx.Err()
			case <-d.guest.endpoint.Done():
				err = contract.Fail(contract.NotReady, "session is closed")
			}
		}
		if err != nil {
			yield(nil, err)
			return
		}
		// A stream holds one of the session's slots for as long as it runs.
		select {
		case d.guest.slots <- struct{}{}:
			defer func() { <-d.guest.slots }()
		case <-ctx.Done():
			yield(nil, ctx.Err())
			return
		case <-d.guest.endpoint.Done():
			yield(nil, contract.Fail(contract.NotReady, "session is closed"))
			return
		}
		d.guest.endpoint.Stream(ctx, "call", data)(yield)
	}
}

// stream serves the host's streaming calls of the services the plugin provides.
func (g *guest) stream(ctx context.Context, method string, input json.RawMessage) iter.Seq2[json.RawMessage, error] {
	return func(yield func(json.RawMessage, error) bool) {
		var invocation contract.Invocation
		err := contract.Decode(input, &invocation)
		if err == nil && method != "call" {
			err = contract.Fail(contract.Invalid, "unknown plugin stream method")
		}
		if err == nil {
			err = g.admit()
		}
		if err != nil {
			yield(nil, err)
			return
		}
		defer g.active.Done()
		provided, err := contract.EndpointKey(invocation.Service.Name, invocation.Service.Major).Get(g.app.Scope())
		if err != nil || provided == nil || provided.Service().Minor < invocation.Service.Minor {
			yield(nil, contract.Fail(contract.NotReady, "service "+invocation.Service.Key()+" is not provided"))
			return
		}
		provided.Stream(ctx, invocation.Method, invocation.Input)(yield)
	}
}

// Dispatch implements contract.Uplink.
func (g *guest) Dispatch(ctx context.Context, event contract.Event) (contract.EventResult, error) {
	var result contract.EventResult
	err := g.request(ctx, "event", event, &result)
	return result, err
}

// Subscribe implements contract.Uplink.
func (g *guest) Subscribe(ctx context.Context, s *cordis.Scope, kind contract.Kind, name string, options contract.SubscriptionOptions, handler contract.EventHandler) (func(context.Context) error, error) {
	g.register.Lock()
	defer g.register.Unlock()
	g.mu.Lock()
	if g.closing {
		g.mu.Unlock()
		return nil, contract.Fail(contract.NotReady, "plugin is closing")
	}
	g.sequence++
	sub := &subscription{id: g.identity.Activation + "/" + strconv.FormatUint(g.sequence, 10), once: options.Once, handler: handler}
	g.events[sub.id] = sub
	g.mu.Unlock()
	forget := func() {
		g.mu.Lock()
		if g.events[sub.id] == sub {
			delete(g.events, sub.id)
		}
		g.mu.Unlock()
	}
	spec := contract.SubscriptionSpec{ID: sub.id, Kind: kind, Name: name, Options: options}
	if err := g.control(ctx, "event.subscribe", spec, &struct{}{}); err != nil {
		forget()
		return nil, err
	}
	// cancel succeeds once the host has confirmed, and again after that; a
	// failed attempt can be repeated.
	cancel := func(ctx context.Context) error {
		g.mu.Lock()
		gone := g.closing || g.events[sub.id] != sub
		g.mu.Unlock()
		if gone {
			return nil // canceled, consumed, or the session is ending anyway
		}
		var state contract.SubscriptionState
		if err := g.control(ctx, "event.unsubscribe", contract.SubscriptionCancel{ID: sub.id}, &state); err != nil {
			return err
		}
		if !state.Inactive {
			// A malformed acknowledgement cannot establish cancellation.
			g.endpoint.Close()
			return contract.Fail(contract.Invalid, "subscription cancellation not acknowledged")
		}
		forget()
		return nil
	}
	// The registration ends with the scope at the latest.
	s.Defer(cancel)
	return cancel, nil
}

// control sends a subscription request. When the host may have seen a
// request whose outcome is unknown, the two sides can no longer agree on
// the subscriptions and the session ends.
func (g *guest) control(ctx context.Context, method string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	ctx, queued, err := g.admitted(ctx)
	if err != nil {
		return err
	}
	sent := false
	data, err = g.endpoint.CallWithReceipt(ctx, method, data, func(onSession bool) {
		sent = onSession
		queued()
	})
	if err != nil {
		definite := false
		if coded, ok := errors.AsType[*contract.Error](err); ok {
			switch coded.Code {
			case contract.Invalid, contract.NotReady, contract.Denied, contract.Conflict:
				definite = true
			}
		}
		if sent && !definite {
			g.endpoint.Close()
		}
		return err
	}
	if err := contract.Decode(data, output); err != nil {
		g.endpoint.Close()
		return err
	}
	return nil
}
