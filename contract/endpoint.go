package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"

	"github.com/Cyenoch/cordis"
)

// Endpoint is a service that can cross a process boundary: methods are
// addressed by name and exchange validated JSON data. Generated code adapts
// a typed implementation to an Endpoint and an Endpoint back to a typed
// client, so plugin code rarely touches this interface.
type Endpoint interface {
	// Service identifies the contract and the exact version provided.
	Service() Service
	Call(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error)
	// Stream calls a method whose results arrive one by one. Nothing is
	// called until iteration starts; iteration ends after the last item or
	// with a final non-nil error, and stopping early cancels the provider.
	Stream(ctx context.Context, method string, input json.RawMessage) iter.Seq2[json.RawMessage, error]
}

// EndpointKey returns the kernel service key for a contract service. All
// minors of one major share a key; use Require to constrain the minor.
func EndpointKey(name string, major uint32) cordis.Service[Endpoint] {
	return cordis.NewService[Endpoint](Service{Name: name, Major: major}.Key())
}

// VersionError reports a provider whose minor is outside the required range.
// It is the reason a consumer waits, so callers can show what is visible.
type VersionError struct {
	Required Requirement
	Visible  Service
}

// Error names the service key, the provided version and the required minors.
func (e *VersionError) Error() string {
	return fmt.Sprintf("%s: incompatible version: provided %d.%d, required minor %d..%d",
		e.Required.Key(), e.Visible.Major, e.Visible.Minor, e.Required.MinMinor, e.Required.MaxMinor)
}

// Is matches an *Error with code Incompatible.
func (e *VersionError) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && other != nil && other.Code == Incompatible
}

// Require is a requirement on key that only accepts providers whose minor
// lies in [minMinor, maxMinor]. Until one is available the instance waits
// with a *VersionError as the reason.
func Require(key cordis.Service[Endpoint], minMinor, maxMinor uint32) cordis.Requirement {
	return key.Where(func(endpoint Endpoint) error {
		provided := endpoint.Service()
		required := Requirement{Name: provided.Name, Major: provided.Major, MinMinor: minMinor, MaxMinor: maxMinor}
		if !required.Accepts(provided) {
			return &VersionError{Required: required, Visible: provided}
		}
		return nil
	})
}

// Handler is one method of an endpoint.
type Handler func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)

// Typed adapts a typed function to a Handler: the input is strictly decoded
// and the output encoded with the rules of this package.
func Typed[In, Out any](fn func(context.Context, In) (Out, error)) Handler {
	return func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		var in In
		if err := DecodeData(input, &in); err != nil {
			return nil, err
		}
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		return Encode(out)
	}
}

// StreamHandler is one streaming method of an endpoint.
type StreamHandler func(ctx context.Context, input json.RawMessage) iter.Seq2[json.RawMessage, error]

// TypedStream adapts a typed streaming function to a StreamHandler.
func TypedStream[In, Out any](fn func(context.Context, In) iter.Seq2[Out, error]) StreamHandler {
	return func(ctx context.Context, input json.RawMessage) iter.Seq2[json.RawMessage, error] {
		return func(yield func(json.RawMessage, error) bool) {
			var in In
			if err := DecodeData(input, &in); err != nil {
				yield(nil, err)
				return
			}
			for out, err := range fn(ctx, in) {
				if err != nil {
					yield(nil, err)
					return
				}
				item, err := Encode(out)
				if err != nil {
					yield(nil, err)
					return
				}
				if !yield(item, nil) {
					return
				}
			}
		}
	}
}

// Methods returns an endpoint that serves the given methods. Generated code
// builds endpoints from a typed implementation; this is for the rest.
func Methods(service Service, methods map[string]Handler) Endpoint {
	return ServiceMethods{Provides: service, Calls: methods}
}

// ServiceMethods is an endpoint made of handlers, for services with
// streaming methods.
type ServiceMethods struct {
	Provides Service
	Calls    map[string]Handler
	Streams  map[string]StreamHandler
}

// Service is the provided service.
func (m ServiceMethods) Service() Service { return m.Provides }

// Call runs the handler of method, or fails for an unknown one.
func (m ServiceMethods) Call(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	handler := m.Calls[method]
	if handler == nil {
		return nil, Fail(Invalid, fmt.Sprintf("service %s has no method %q", m.Provides.Key(), method))
	}
	return handler(ctx, input)
}

// Stream runs the stream handler of method, or fails for an unknown one.
func (m ServiceMethods) Stream(ctx context.Context, method string, input json.RawMessage) iter.Seq2[json.RawMessage, error] {
	handler := m.Streams[method]
	if handler == nil {
		return FailedStream(Fail(Invalid, fmt.Sprintf("service %s has no streaming method %q", m.Provides.Key(), method)))
	}
	return handler(ctx, input)
}

// FailedStream is a stream that ends with err at once.
func FailedStream(err error) iter.Seq2[json.RawMessage, error] {
	return func(yield func(json.RawMessage, error) bool) { yield(nil, err) }
}

// InvokeStream calls one streaming method of an endpoint with typed input
// and items.
func InvokeStream[In, Out any](ctx context.Context, endpoint Endpoint, method string, in In) iter.Seq2[Out, error] {
	return func(yield func(Out, error) bool) {
		var zero Out
		input, err := Encode(in)
		if err != nil {
			yield(zero, err)
			return
		}
		for item, err := range endpoint.Stream(ctx, method, input) {
			if err != nil {
				yield(zero, err)
				return
			}
			var out Out
			if err := DecodeData(item, &out); err != nil {
				yield(zero, err)
				return
			}
			if !yield(out, nil) {
				return
			}
		}
	}
}

// Invoke calls one method of an endpoint with typed input and output.
func Invoke[In, Out any](ctx context.Context, endpoint Endpoint, method string, in In) (out Out, err error) {
	input, err := Encode(in)
	if err != nil {
		return out, err
	}
	output, err := endpoint.Call(ctx, method, input)
	if err != nil {
		return out, err
	}
	err = DecodeData(output, &out)
	return out, err
}
