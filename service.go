// Package cordis provides a host for scoped, dynamically loaded plugin packages.
package cordis

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/Cyenoch/cordis/contract"
)

type Service[In, Out any] struct{ Contract contract.Service }

func Provide[In, Out any](scope contract.Registrar, service Service[In, Out], handler func(context.Context, In) (Out, error)) error {
	if err := contract.CheckDataType(reflect.TypeFor[In]()); err != nil {
		return err
	}
	if err := contract.CheckDataType(reflect.TypeFor[Out]()); err != nil {
		return err
	}
	return scope.Provide(service.Contract, func(ctx context.Context, data json.RawMessage) (json.RawMessage, error) {
		var input In
		if err := contract.DecodeData(data, &input); err != nil {
			return nil, err
		}
		value, err := handler(ctx, input)
		if err != nil {
			return nil, err
		}
		return contract.Encode(value)
	})
}

func Call[In, Out any](ctx context.Context, scope contract.Caller, service Service[In, Out], input In) (Out, error) {
	var output Out
	data, err := contract.Encode(input)
	if err != nil {
		return output, fmt.Errorf("encode typed call: %w", err)
	}
	result, err := scope.Call(ctx, contract.Requirement{Name: service.Contract.Name, Major: service.Contract.Major,
		MinMinor: service.Contract.Minor, MaxMinor: service.Contract.Minor}, data)
	if err != nil {
		return output, err
	}
	if err := contract.DecodeData(result, &output); err != nil {
		return output, err
	}
	return output, nil
}
