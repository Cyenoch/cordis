package contract

import (
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
