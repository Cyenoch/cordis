// Package plugintest runs real SDK definitions and codecs with explicit test
// service ports. Process execution is covered by runtime integration tests.
package plugintest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
)

// Options configures only business inputs and test doubles. Ports do not grant
// production services.
type Options struct {
	Config   json.RawMessage
	Business contract.Caller
}

// Harness owns one initialized, published definition. Always Close it, including
// after a test fails. Cancellation of New's context bounds startup, not lifetime.
type Harness struct {
	endpoint     *wire.Endpoint
	registration contract.Registration
	serveDone    chan struct{}
	serveErr     error
	closeOnce    sync.Once
	closeDone    chan struct{}
	closeErr     error
}

// New validates configuration through the definition and executes its real Start.
// Failed initialization drains registered resources before returning its error.
func New(ctx context.Context, definition contract.Definition, options Options) (*Harness, error) {
	guestIn, hostOut := io.Pipe()
	hostIn, guestOut := io.Pipe()
	guest := &wire.Transport{Reader: guestIn, Writer: guestOut, Closers: []io.Closer{guestIn, guestOut}}
	host := &wire.Transport{Reader: hostIn, Writer: hostOut, Closers: []io.Closer{hostIn, hostOut}}
	h := &Harness{serveDone: make(chan struct{}), closeDone: make(chan struct{})}
	identity := contract.Identity{Package: "plugintest", Entry: definition.ID, Activation: contract.NewID()}
	endpoint, err := wire.New(wire.Options{FrameBytes: contract.MaxFrameBytes, Transport: host, Identity: identity, Handler: options.handle})
	if err != nil {
		return nil, errors.Join(err, guest.Close(), host.Close())
	}
	h.endpoint = endpoint
	go func() {
		h.serveErr = sdk.RunTransport(context.WithoutCancel(ctx), definition, guest)
		h.serveErr = errors.Join(h.serveErr, guest.Close())
		close(h.serveDone)
	}()
	config := options.Config
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	input, err := json.Marshal(contract.Initialize{
		FrameBytes: contract.MaxFrameBytes, Identity: identity, Protocol: contract.Protocol{Major: 1},
		Definition: definition.ID, Config: config,
	})
	if err == nil {
		var output json.RawMessage
		output, err = endpoint.Call(ctx, "initialize", input)
		if err == nil {
			err = contract.Decode(output, &h.registration)
		}
	}
	if err == nil {
		_, err = endpoint.Call(ctx, "activation.publish", json.RawMessage(`{}`))
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("start test definition %q: %w", definition.ID, err), h.Close(context.WithoutCancel(ctx)))
	}
	return h, nil
}

func (o Options) handle(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "call":
		var call contract.ServiceCall
		if err := contract.Decode(input, &call); err != nil {
			return nil, err
		}
		if o.Business != nil {
			return o.Business.Call(ctx, call.Required, call.Input)
		}
	}
	return nil, contract.Fail(contract.NotReady, "test port not configured: "+method)
}

// Call implements contract.Caller, so typed and generated clients use the same
// request/response codecs as production instead of invoking handlers directly.
func (h *Harness) Call(ctx context.Context, required contract.Requirement, input json.RawMessage) (json.RawMessage, error) {
	for _, service := range h.registration.Services {
		if required.Accepts(service) {
			data, err := json.Marshal(contract.Invocation{Service: service, Input: input})
			if err != nil {
				return nil, err
			}
			return h.endpoint.Call(ctx, "call", data)
		}
	}
	return nil, contract.Fail(contract.NotReady, "compatible test service not provided")
}

// Deliver exercises the definition's real event handlers and dispatch behavior.
func (h *Harness) Deliver(ctx context.Context, event contract.Event) ([]contract.EventResult, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	data, err = h.endpoint.Call(ctx, "event", data)
	if err != nil {
		return nil, err
	}
	var results []contract.EventResult
	err = contract.Decode(data, &results)
	return results, err
}

// Retire cancels activation work and ordinary calls without releasing resources.
func (h *Harness) Retire(ctx context.Context) error {
	_, err := h.endpoint.Call(ctx, "activation.retire", json.RawMessage(`{}`))
	return err
}

// Wait returns the local SDK failure, including the original managed-work cause.
func (h *Harness) Wait(ctx context.Context) error {
	select {
	case <-h.serveDone:
		return h.serveErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close starts one shared shutdown. A canceled wait does not claim resources
// have drained, interrupt cleanup ports or create another shutdown operation.
func (h *Harness) Close(ctx context.Context) error {
	h.closeOnce.Do(func() {
		go func() {
			cleanupCtx := context.WithoutCancel(ctx)
			_, shutdownErr := h.endpoint.Call(cleanupCtx, "shutdown", json.RawMessage(`{}`))
			closeErr := h.endpoint.Close()
			h.endpoint.Wait()
			<-h.serveDone
			h.closeErr = errors.Join(shutdownErr, closeErr, h.serveErr)
			close(h.closeDone)
		}()
	})
	select {
	case <-h.closeDone:
		return h.closeErr
	default:
	}
	select {
	case <-h.closeDone:
		return h.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
