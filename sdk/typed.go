package sdk

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/Cyenoch/cordis/contract"
)

// Provide binds a typed handler without exposing protocol encoding to authors.
func Provide[In, Out any](scope contract.Registrar, service contract.Service, handler func(context.Context, In) (Out, error)) error {
	if err := contract.CheckDataType(reflect.TypeFor[In]()); err != nil {
		return err
	}
	if err := contract.CheckDataType(reflect.TypeFor[Out]()); err != nil {
		return err
	}
	return scope.Provide(service, func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		var request In
		if err := contract.DecodeData(input, &request); err != nil {
			return nil, err
		}
		response, err := handler(ctx, request)
		if err != nil {
			return nil, err
		}
		return contract.Encode(response)
	})
}

func Call[In, Out any](ctx context.Context, scope contract.Caller, required contract.Requirement, input In) (Out, error) {
	var result Out
	data, err := contract.Encode(input)
	if err != nil {
		return result, err
	}
	output, err := scope.Call(ctx, required, data)
	if err != nil {
		return result, err
	}
	if err := contract.DecodeData(output, &result); err != nil {
		return result, err
	}
	return result, nil
}
