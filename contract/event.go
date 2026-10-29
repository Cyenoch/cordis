package contract

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

const MaxSubscriptions = 256

type SubscriptionOptions struct {
	Once    bool `json:"once"`
	Prepend bool `json:"prepend"`
}

// Subscription revokes future admission without waiting for current callbacks.
// Cancel is idempotent after confirmed cancellation or once consumption.
type Subscription interface {
	Cancel(context.Context) error
}

type SubscriptionSpec struct {
	ID      string              `json:"id"`
	Name    string              `json:"name"`
	Options SubscriptionOptions `json:"options"`
}

type SubscriptionCancel struct {
	ID string `json:"id"`
}

type SubscriptionState struct {
	Inactive bool `json:"inactive"`
}

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

type EventKey[T any] struct {
	Name     string
	Mode     Dispatch
	Validate func(T) error
}

type EventSubscriber interface {
	On(context.Context, string, SubscriptionOptions, EventHandler) (Subscription, error)
}
type EventEmitter interface {
	Emit(context.Context, string, json.RawMessage, Dispatch) ([]EventResult, error)
}

// Subscribe validates typed messages before invoking a listener. Mode remains
// explicit at the event declaration; zero values never imply unhandled results.
func Subscribe[T any](ctx context.Context, scope EventSubscriber, event EventKey[T], options SubscriptionOptions, handler func(context.Context, T) (EventResult, error)) (Subscription, error) {
	if err := CheckDataType(reflect.TypeFor[T]()); err != nil {
		return nil, err
	}
	if handler == nil || event.Name == "" || event.Mode != Sequential && event.Mode != Parallel && event.Mode != First {
		return nil, Fail(Invalid, "invalid event declaration or handler")
	}
	return scope.On(ctx, event.Name, options, func(ctx context.Context, data json.RawMessage) (EventResult, error) {
		var input T
		if err := DecodeData(data, &input); err != nil {
			return EventResult{}, err
		}
		if event.Validate != nil {
			if err := event.Validate(input); err != nil {
				return EventResult{}, err
			}
		}
		return handler(ctx, input)
	})
}

func Publish[T any](ctx context.Context, scope EventEmitter, event EventKey[T], input T) ([]EventResult, error) {
	if event.Validate != nil {
		if err := event.Validate(input); err != nil {
			return nil, err
		}
	}
	data, err := Encode(input)
	if err != nil {
		return nil, err
	}
	return scope.Emit(ctx, event.Name, data, event.Mode)
}
