package contract

import "encoding/json"

// ServiceCall is a guest's call of one method of a service it requires. The
// host resolves the provider from the calling entry's bindings.
type ServiceCall struct {
	Required Requirement     `json:"required"`
	Method   string          `json:"method"`
	Input    json.RawMessage `json:"input"`
}
