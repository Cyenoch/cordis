package contract

import (
	"context"
	"encoding/json"
)

func (i *Invocation) UnmarshalJSON(data []byte) error {
	type invocation Invocation
	var value invocation
	if err := Decode(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, exists := fields["cleanup"]; !exists {
		return Fail(Invalid, "invocation requires cleanup classification")
	}
	*i = Invocation(value)
	return nil
}

// PluginSession adapts lifecycle and business operations without wrapping a
// host's existing wire protocol or forcing its public method names to change.
type PluginSession interface {
	Initialize(context.Context, Initialize) (Registration, error)
	Publish(context.Context) error
	Retire(context.Context) error
	Invoke(context.Context, Invocation) (json.RawMessage, error)
	Deliver(context.Context, Event) ([]EventResult, error)
	Shutdown(context.Context) error
}
