package contract

import (
	"context"
	"encoding/json"
	"reflect"
)

type EventKey[T any] struct {
	Name     string
	Mode     Dispatch
	Validate func(T) error
}

type EventSubscriber interface {
	On(string, EventHandler) error
}
type EventEmitter interface {
	Emit(context.Context, string, json.RawMessage, Dispatch) ([]EventResult, error)
}

// Subscribe validates typed messages before invoking a listener. Mode remains
// explicit at the event declaration; zero values never imply unhandled results.
func Subscribe[T any](scope EventSubscriber, event EventKey[T], handler func(context.Context, T) (EventResult, error)) error {
	if err := CheckDataType(reflect.TypeFor[T]()); err != nil {
		return err
	}
	if event.Name == "" || event.Mode != Sequential && event.Mode != Parallel && event.Mode != First {
		return Fail(Invalid, "invalid event declaration")
	}
	return scope.On(event.Name, func(ctx context.Context, data json.RawMessage) (EventResult, error) {
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
