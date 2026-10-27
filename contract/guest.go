package contract

import (
	"context"
	"encoding/json"
)

// GuestAdapter translates a host-owned public ABI into authorized scoped calls.
// Scope carries only the current activation, committed bindings and cancellation.
// Hosts remain responsible for object scope, projection and domain semantics.
type GuestAdapter func(context.Context, Scope, string, json.RawMessage) (json.RawMessage, error)
