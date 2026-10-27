package cordis

import (
	"context"
	"encoding/json"

	"github.com/Cyenoch/cordis/contract"
)

type genericPlugin struct{ session contract.Session }

func (s genericPlugin) Initialize(ctx context.Context, input contract.Initialize) (contract.Registration, error) {
	var output contract.Registration
	data, err := json.Marshal(input)
	if err != nil {
		return output, err
	}
	data, err = s.session.Call(ctx, "initialize", data)
	if err != nil {
		return output, err
	}
	err = contract.Decode(data, &output)
	return output, err
}
func (s genericPlugin) Invoke(ctx context.Context, input contract.Invocation) (json.RawMessage, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return s.session.Call(ctx, "call", data)
}
func (s genericPlugin) Deliver(ctx context.Context, input contract.Event) ([]contract.EventResult, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	data, err = s.session.Call(ctx, "event", data)
	if err != nil {
		return nil, err
	}
	var results []contract.EventResult
	err = contract.Decode(data, &results)
	return results, err
}
func (s genericPlugin) Shutdown(ctx context.Context) error {
	_, err := s.session.Call(ctx, "shutdown", json.RawMessage(`{}`))
	return err
}
