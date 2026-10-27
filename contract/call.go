package contract

import "encoding/json"

// ServiceCall requests a compatible provider from the calling scope's committed
// bindings. Invocation remains the resolved, exact-version host-to-guest call.
type ServiceCall struct {
	Required Requirement     `json:"required"`
	Input    json.RawMessage `json:"input"`
}
