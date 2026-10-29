package wire

import (
	"encoding/json"

	"github.com/Cyenoch/cordis/contract"
)

func decodeInitialize(data json.RawMessage) (contract.Initialize, error) {
	var input contract.Initialize
	if err := decodeRequired(data, &input, "frameBytes", "identity", "protocol", "definition", "config"); err != nil {
		return input, err
	}
	return input, contract.ValidateFrameBytes(input.FrameBytes)
}

func decodeRegistration(data json.RawMessage, frameBytes int) error {
	var registration contract.Registration
	if err := decodeRequired(data, &registration, "frameBytes", "minor", "features", "services"); err != nil {
		return err
	}
	if registration.FrameBytes != frameBytes {
		return contract.Fail(contract.Invalid, "registration frameBytes mismatch")
	}
	return nil
}

func decodeRequired(data json.RawMessage, target any, required ...string) error {
	if err := contract.Decode(data, target); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range required {
		if _, exists := fields[name]; !exists {
			return contract.Fail(contract.Invalid, "missing session field: "+name)
		}
	}
	return nil
}
