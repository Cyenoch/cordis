// Package sdk is the Go author interface for external native and WASI plugins.
package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/wire"
)

type Client struct {
	mu            sync.Mutex
	serial        chan struct{}
	endpoint      *wire.Endpoint
	definitions   map[string]contract.Definition
	definition    contract.Definition
	identity      contract.Identity
	config        json.RawMessage
	ctx           context.Context
	cancel        context.CancelFunc
	services      map[string]contract.Handler
	serviceInfo   []contract.Service
	events        map[string][]contract.EventHandler
	cleanups      []contract.Cleanup
	initialized   bool
	starting      bool
	closed        bool
	closeErr      error
	closeDone     chan struct{}
	endpointReady chan struct{}
	contributions []contract.Contribution
	tasks         map[string]*Task
}

// Serve runs a command-style guest using standard input/output for protocol only.
func Serve(ctx context.Context, definition contract.Definition) error {
	transport, err := stdioTransport()
	if err != nil {
		return err
	}
	return ServeTransport(ctx, definition, transport)
}

func ServeTransport(ctx context.Context, definition contract.Definition, transport io.ReadWriteCloser) error {
	return serveDefinitions(ctx, transport, []contract.Definition{definition})
}

func serve(ctx context.Context, definitions map[string]contract.Definition, transport io.ReadWriteCloser) error {
	client := &Client{serial: make(chan struct{}, 1), definitions: definitions, services: map[string]contract.Handler{},
		events: map[string][]contract.EventHandler{}, cleanups: []contract.Cleanup{}, serviceInfo: []contract.Service{},
		contributions: []contract.Contribution{}, closeDone: make(chan struct{}), endpointReady: make(chan struct{})}
	client.ctx, client.cancel = context.WithCancel(ctx)
	endpoint, err := wire.New(wire.Options{Transport: transport, Guest: true, Handler: client.handle})
	if err != nil {
		client.cancel()
		return err
	}
	client.endpoint = endpoint
	close(client.endpointReady)
	select {
	case <-ctx.Done():
		if err := endpoint.Close(); err != nil {
			return err
		}
	case <-endpoint.Done():
	}
	cleanupErr := client.close(context.WithoutCancel(ctx))
	if err := endpoint.Close(); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	endpoint.Wait()
	err = endpoint.Err()
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return errors.Join(err, cleanupErr)
}

func (c *Client) Identity() contract.Identity { c.mu.Lock(); defer c.mu.Unlock(); return c.identity }
func (c *Client) Config() json.RawMessage     { c.mu.Lock(); defer c.mu.Unlock(); return c.config }
func (c *Client) Context() context.Context    { return c.ctx }
func (c *Client) Defer(cleanup contract.Cleanup) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed && !c.starting {
		return contract.Fail(contract.NotReady, "plugin is closing")
	}
	if cleanup == nil {
		return contract.Fail(contract.Invalid, "nil cleanup")
	}
	c.cleanups = append(c.cleanups, cleanup)
	return nil
}

func (c *Client) Provide(service contract.Service, handler contract.Handler) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.initialized {
		return contract.Fail(contract.NotReady, "services register during initialization only")
	}
	if handler == nil || service.Name == "" || service.Major == 0 {
		return contract.Fail(contract.Invalid, "invalid service registration")
	}
	if _, exists := c.services[service.Key()]; exists {
		return contract.Fail(contract.Conflict, "duplicate service registration")
	}
	c.services[service.Key()] = handler
	c.serviceInfo = append(c.serviceInfo, service)
	return nil
}

func (c *Client) Call(ctx context.Context, required contract.Requirement, input json.RawMessage) (json.RawMessage, error) {
	request := contract.ServiceCall{Required: required, Input: input}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode service call: %w", err)
	}
	return c.endpoint.Call(ctx, "call", data)
}

func (c *Client) InvokeHost(ctx context.Context, capability string, input json.RawMessage) (json.RawMessage, error) {
	data, err := json.Marshal(contract.HostCall{Capability: capability, Input: input})
	if err != nil {
		return nil, fmt.Errorf("encode host invocation: %w", err)
	}
	return c.endpoint.Call(ctx, "host.invoke", data)
}

func (c *Client) On(name string, handler contract.EventHandler) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.initialized {
		return contract.Fail(contract.NotReady, "subscriptions register during initialization only")
	}
	if name == "" || handler == nil {
		return contract.Fail(contract.Invalid, "invalid event subscription")
	}
	c.events[name] = append(c.events[name], handler)
	return nil
}

func (c *Client) Emit(ctx context.Context, name string, input json.RawMessage, mode contract.Dispatch) ([]contract.EventResult, error) {
	data, err := json.Marshal(contract.Event{Name: name, Input: input, Mode: mode})
	if err != nil {
		return nil, fmt.Errorf("encode event: %w", err)
	}
	output, err := c.endpoint.Call(ctx, "event", data)
	if err != nil {
		return nil, err
	}
	var result []contract.EventResult
	if err := contract.Decode(output, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) Contribute(value contract.Contribution) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.initialized {
		return contract.Fail(contract.NotReady, "contributions register during initialization only")
	}
	c.contributions = append(c.contributions, value)
	return nil
}

func (c *Client) handle(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	select {
	case <-c.endpointReady:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	switch method {
	case "task.cancel":
		return c.cancelTask(input)
	case "health":
		return json.RawMessage(`{"ready":true}`), nil
	case "shutdown":
		c.cancel()
		if err := c.close(ctx); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	case "initialize":
		return c.start(ctx, input)
	}
	callbackCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.ctx, cancel)
	defer func() { stop(); cancel() }()
	ctx = callbackCtx
	select {
	case c.serial <- struct{}{}:
		defer func() { <-c.serial }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	c.mu.Lock()
	ready := c.initialized && !c.closed
	c.mu.Unlock()
	if !ready {
		return nil, contract.Fail(contract.NotReady, "plugin is not active")
	}
	switch method {
	case "call":
		var request contract.Invocation
		if err := contract.Decode(input, &request); err != nil {
			return nil, err
		}
		c.mu.Lock()
		handler := c.services[request.Service.Key()]
		compatible := false
		for _, service := range c.serviceInfo {
			if service.Key() == request.Service.Key() && service.Minor >= request.Service.Minor {
				compatible = true
			}
		}
		c.mu.Unlock()
		if handler == nil || !compatible {
			return nil, contract.Fail(contract.NotReady, "compatible service not provided")
		}
		return handler(ctx, request.Input)
	case "event":
		var event contract.Event
		if err := contract.Decode(input, &event); err != nil {
			return nil, err
		}
		c.mu.Lock()
		handlers := append([]contract.EventHandler{}, c.events[event.Name]...)
		c.mu.Unlock()
		results := []contract.EventResult{}
		var failures []error
		for n, handler := range handlers {
			result, err := handler(ctx, event.Input)
			if err != nil {
				if event.Mode != contract.Parallel {
					return nil, err
				}
				failures = append(failures, fmt.Errorf("event %s listener %d: %w", event.Name, n, err))
			}
			results = append(results, result)
			if event.Mode == contract.First && result.Handled {
				break
			}
		}
		if len(failures) > 0 {
			return nil, errors.Join(failures...)
		}
		return json.Marshal(results)
	default:
		return nil, contract.Fail(contract.Invalid, "unknown plugin method")
	}
}

func (c *Client) initialize(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var init contract.Initialize
	if err := contract.Decode(input, &init); err != nil {
		return nil, err
	}
	minor, err := init.Protocol.Negotiate(contract.Protocol{Major: 1})
	if err != nil {
		return nil, err
	}
	definition, exists := c.definitions[init.Definition]
	if init.Identity.Activation != c.endpoint.Identity().Activation || !exists {
		return nil, contract.Fail(contract.Invalid, "initialization identity mismatch")
	}
	c.mu.Lock()
	if c.initialized || c.identity.Activation != "" {
		c.mu.Unlock()
		return nil, contract.Fail(contract.Invalid, "plugin already initialized")
	}
	c.definition = definition
	c.identity = init.Identity
	c.config = init.Config
	c.mu.Unlock()
	if c.definition.Validate != nil {
		if err := c.definition.Validate(init.Config); err != nil {
			return nil, err
		}
	}
	if c.definition.Start != nil {
		if err := c.definition.Start(ctx, c); err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || ctx.Err() != nil {
		return nil, contract.Fail(contract.Canceled, "initialization canceled")
	}
	c.initialized = true
	subscriptions := make([]string, 0, len(c.events))
	for name := range c.events {
		subscriptions = append(subscriptions, name)
	}
	return json.Marshal(contract.Registration{Minor: minor, Features: []string{"cancel", "parent", "tasks", "events", "children"}, Services: c.serviceInfo, Subscriptions: subscriptions, Contributions: c.contributions})
}

func (c *Client) close(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		done := c.closeDone
		c.mu.Unlock()
		select {
		case <-done:
			c.mu.Lock()
			err := c.closeErr
			c.mu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.closed = true
	c.mu.Unlock()
	c.cancel()
	select {
	case c.serial <- struct{}{}:
	case <-ctx.Done():
		go func() {
			c.serial <- struct{}{}
			result := c.releaseCleanups(context.WithoutCancel(ctx))
			<-c.serial
			c.mu.Lock()
			c.closeErr = result
			close(c.closeDone)
			c.mu.Unlock()
		}()
		return ctx.Err()
	}
	result := c.releaseCleanups(ctx)
	<-c.serial
	c.mu.Lock()
	c.closeErr = result
	close(c.closeDone)
	c.mu.Unlock()
	return result
}

func (c *Client) start(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	startCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.ctx, cancel)
	defer func() { stop(); cancel() }()
	select {
	case c.serial <- struct{}{}:
	case <-startCtx.Done():
		return nil, startCtx.Err()
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		<-c.serial
		return nil, contract.Fail(contract.NotReady, "plugin closing")
	}
	c.starting = true
	c.mu.Unlock()
	data, err := c.initialize(startCtx, input)
	c.mu.Lock()
	c.starting = false
	c.mu.Unlock()
	<-c.serial
	// The host must initiate shutdown with its bound cleanup context. Reusing
	// a failed initialize parent would invalidate deferred service releases.
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (c *Client) releaseCleanups(ctx context.Context) error {
	c.mu.Lock()
	cleanups := c.cleanups
	c.cleanups = nil
	c.mu.Unlock()
	var result error
	for i := len(cleanups) - 1; i >= 0; i-- {
		result = errors.Join(result, cleanups[i](ctx))
	}
	return result
}
