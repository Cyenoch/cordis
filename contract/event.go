package contract

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"github.com/Cyenoch/cordis"
)

const MaxSubscriptions = 256

// Kind tells the two families of keys that cross a session apart. Each has
// its own name space.
type Kind string

const (
	KindEvent Kind = "event"
	KindQuery Kind = "query"
)

// SubscriptionOptions are the options of a guest listener or middleware:
// run once, or before the others.
type SubscriptionOptions struct {
	Once    bool `json:"once"`
	Prepend bool `json:"prepend"`
}

// SubscriptionSpec registers a guest listener, query handler or chain
// middleware with the host.
type SubscriptionSpec struct {
	ID      string              `json:"id"`
	Kind    Kind                `json:"kind"`
	Name    string              `json:"name"`
	Options SubscriptionOptions `json:"options"`
}

// SubscriptionCancel removes a guest registration by ID.
type SubscriptionCancel struct {
	ID string `json:"id"`
}

// SubscriptionState acknowledges a cancellation; Inactive confirms it.
type SubscriptionState struct {
	Inactive bool `json:"inactive"`
}

// EventDelivery is the host's delivery of one emission to a guest listener.
type EventDelivery struct {
	ID    string          `json:"id"`
	Input json.RawMessage `json:"input"`
}

// SubscriptionNumber checks an activation-owned, canonical positive sequence.
func SubscriptionNumber(activation, id string) (uint64, error) {
	value, ok := strings.CutPrefix(id, activation+"/")
	n, err := strconv.ParseUint(value, 10, 64)
	if activation == "" || !ok || err != nil || n == 0 || strconv.FormatUint(n, 10) != value {
		return 0, Fail(Invalid, "invalid subscription identity")
	}
	return n, nil
}

// Uplink is how the keys of this package reach the host from a guest
// process. The guest runtime provides it at its root scope before it loads
// any plugin; in a host process nobody provides it and the keys act on the
// local kernel. Plugin code does not use it.
type Uplink interface {
	// Subscribe registers handler with the host for the lifetime of s.
	Subscribe(ctx context.Context, s *cordis.Scope, kind Kind, name string, options SubscriptionOptions, handler EventHandler) (cancel func(context.Context) error, err error)
	// Dispatch runs the host's listeners or handlers.
	Dispatch(ctx context.Context, event Event) (EventResult, error)
}

// UplinkKey is the service under which a guest runtime provides its Uplink.
var UplinkKey = cordis.NewService[Uplink]("cordis/uplink")

func uplink(s *cordis.Scope) (Uplink, bool) {
	up, err := UplinkKey.Get(s)
	return up, err == nil && up != nil
}

// RawEvent is the kernel event that carries the encoded payloads of the
// EventKey with this name. Bridges use it; plugins use EventKey.
func RawEvent(name string) cordis.Event[json.RawMessage] {
	return cordis.NewEvent[json.RawMessage]("wire:" + name)
}

// RawQuery is the kernel query behind the QueryKey with this name.
func RawQuery(name string) cordis.Query[json.RawMessage, json.RawMessage] {
	return cordis.NewQuery[json.RawMessage, json.RawMessage]("wire:" + name)
}

func subscriptionOptions(options []cordis.ListenOption) (result SubscriptionOptions) {
	for _, option := range options {
		switch option {
		case cordis.Once:
			result.Once = true
		case cordis.Prepend:
			result.Prepend = true
		}
	}
	return result
}

func local(stop func()) func(context.Context) error {
	return func(context.Context) error {
		stop()
		return nil
	}
}

// EventKey is an event whose payload crosses process boundaries as
// validated JSON. Its operations mirror cordis.Event; registering and
// canceling take a context and return an error because in a guest process
// they are a round trip to the host.
type EventKey[T any] struct{ name string }

// NewEventKey returns the key of the event named name.
func NewEventKey[T any](name string) EventKey[T] { return EventKey[T]{name: name} }

// Name is the event's name on the session.
func (k EventKey[T]) Name() string { return k.name }

// On registers fn for the lifetime of s. The returned cancel removes it
// earlier; it does not wait for calls already admitted, and canceling again
// succeeds.
func (k EventKey[T]) On(ctx context.Context, s *cordis.Scope, fn func(context.Context, T) error, options ...cordis.ListenOption) (cancel func(context.Context) error, err error) {
	if err := CheckDataType(reflect.TypeFor[T]()); err != nil {
		return nil, err
	}
	if k.name == "" || fn == nil {
		return nil, Fail(Invalid, "event name and listener are required")
	}
	listener := func(ctx context.Context, data json.RawMessage) error {
		var payload T
		if err := DecodeData(data, &payload); err != nil {
			return err
		}
		return fn(ctx, payload)
	}
	if up, remote := uplink(s); remote {
		return up.Subscribe(ctx, s, KindEvent, k.name, subscriptionOptions(options),
			func(ctx context.Context, data json.RawMessage) (EventResult, error) {
				return EventResult{}, listener(ctx, data)
			})
	}
	return local(RawEvent(k.name).On(s, listener, options...)), nil
}

// Emit calls the listeners one after another and stops at the first error.
func (k EventKey[T]) Emit(ctx context.Context, s *cordis.Scope, payload T) error {
	return k.emit(ctx, s, payload, Sequential)
}

// EmitParallel calls the listeners concurrently and joins their errors.
func (k EventKey[T]) EmitParallel(ctx context.Context, s *cordis.Scope, payload T) error {
	return k.emit(ctx, s, payload, Parallel)
}

func (k EventKey[T]) emit(ctx context.Context, s *cordis.Scope, payload T, mode Dispatch) error {
	data, err := Encode(payload)
	if err != nil {
		return err
	}
	if up, remote := uplink(s); remote {
		_, err := up.Dispatch(ctx, Event{Kind: KindEvent, Name: k.name, Input: data, Mode: mode})
		return err
	}
	if mode == Parallel {
		return RawEvent(k.name).EmitParallel(ctx, s, data)
	}
	return RawEvent(k.name).Emit(ctx, s, data)
}

// QueryKey is a query whose input and answer cross process boundaries as
// validated JSON. Its operations mirror cordis.Query.
type QueryKey[In, Out any] struct{ name string }

// NewQueryKey returns the key of the query named name.
func NewQueryKey[In, Out any](name string) QueryKey[In, Out] { return QueryKey[In, Out]{name: name} }

// Name is the query's name on the session.
func (k QueryKey[In, Out]) Name() string { return k.name }

// Handle registers fn for the lifetime of s. fn reports whether it answered;
// a zero answer is still an answer.
func (k QueryKey[In, Out]) Handle(ctx context.Context, s *cordis.Scope, fn func(context.Context, In) (Out, bool, error), options ...cordis.ListenOption) (cancel func(context.Context) error, err error) {
	for _, t := range []reflect.Type{reflect.TypeFor[In](), reflect.TypeFor[Out]()} {
		if err := CheckDataType(t); err != nil {
			return nil, err
		}
	}
	if k.name == "" || fn == nil {
		return nil, Fail(Invalid, "query name and handler are required")
	}
	handler := func(ctx context.Context, data json.RawMessage) (json.RawMessage, bool, error) {
		var in In
		if err := DecodeData(data, &in); err != nil {
			return nil, false, err
		}
		out, handled, err := fn(ctx, in)
		if err != nil || !handled {
			return nil, false, err
		}
		value, err := Encode(out)
		return value, err == nil, err
	}
	if up, remote := uplink(s); remote {
		return up.Subscribe(ctx, s, KindQuery, k.name, subscriptionOptions(options),
			func(ctx context.Context, data json.RawMessage) (EventResult, error) {
				value, handled, err := handler(ctx, data)
				return EventResult{Handled: handled, Value: value}, err
			})
	}
	return local(RawQuery(k.name).Handle(s, handler, options...)), nil
}

// Ask calls the handlers in order until one answers.
func (k QueryKey[In, Out]) Ask(ctx context.Context, s *cordis.Scope, in In) (out Out, handled bool, err error) {
	data, err := Encode(in)
	if err != nil {
		return out, false, err
	}
	var value json.RawMessage
	if up, remote := uplink(s); remote {
		var result EventResult
		result, err = up.Dispatch(ctx, Event{Kind: KindQuery, Name: k.name, Input: data, Mode: First})
		value, handled = result.Value, result.Handled
	} else {
		value, handled, err = RawQuery(k.name).Ask(ctx, s, data)
	}
	if err != nil || !handled {
		return out, false, err
	}
	err = DecodeData(value, &out)
	return out, err == nil, err
}
