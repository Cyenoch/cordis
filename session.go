package cordis

import (
	"context"
	"encoding/json"

	"github.com/Cyenoch/cordis/contract"
)

type genericPlugin struct{ session contract.Session }

func (s genericPlugin) Publish(ctx context.Context) error {
	_, err := s.session.Call(ctx, "activation.publish", json.RawMessage(`{}`))
	return err
}

func (s genericPlugin) Retire(ctx context.Context) error {
	_, err := s.session.Call(ctx, "activation.retire", json.RawMessage(`{}`))
	return err
}

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
func (s genericPlugin) Deliver(ctx context.Context, input contract.EventDelivery, receipt func(bool)) (contract.EventResult, error) {
	data, err := json.Marshal(input)
	if err != nil {
		receipt(false)
		return contract.EventResult{}, err
	}
	data, err = s.session.CallWithReceipt(ctx, "event", data, receipt)
	if err != nil {
		return contract.EventResult{}, err
	}
	var result struct {
		Handled *bool           `json:"handled"`
		Value   json.RawMessage `json:"value"`
	}
	if err := contract.Decode(data, &result); err != nil {
		return contract.EventResult{}, err
	}
	if result.Handled == nil {
		return contract.EventResult{}, contract.Fail(contract.Invalid, "event result requires handled")
	}
	return contract.EventResult{Handled: *result.Handled, Value: result.Value}, nil
}
func (s genericPlugin) Shutdown(ctx context.Context) error {
	_, err := s.session.Call(ctx, "shutdown", json.RawMessage(`{}`))
	return err
}

func (s genericPlugin) ForgetSubscription(ctx context.Context, id string) error {
	data, err := json.Marshal(contract.SubscriptionCancel{ID: id})
	if err != nil {
		return err
	}
	data, err = s.session.Call(ctx, "event.unsubscribe", data)
	if err != nil {
		return err
	}
	var state contract.SubscriptionState
	if err := contract.Decode(data, &state); err != nil {
		return err
	}
	if !state.Inactive {
		return contract.Fail(contract.Invalid, "subscription retirement not acknowledged")
	}
	return nil
}
