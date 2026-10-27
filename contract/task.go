package contract

import "encoding/json"

type TaskRequest struct {
	ID      string          `json:"id"`
	Token   string          `json:"token"`
	State   string          `json:"state,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Failure Code            `json:"failure,omitempty"`
}
