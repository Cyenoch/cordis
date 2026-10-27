package contract

import (
	"context"
	"encoding/json"
)

// PluginSession adapts lifecycle and business operations without wrapping a
// host's existing wire protocol or forcing its public method names to change.
type PluginSession interface {
	Initialize(context.Context, Initialize) (Registration, error)
	Invoke(context.Context, Invocation) (json.RawMessage, error)
	Deliver(context.Context, Event) ([]EventResult, error)
	Shutdown(context.Context) error
}
