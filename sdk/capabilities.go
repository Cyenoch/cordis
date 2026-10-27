package sdk

import (
	_ "embed"
	"encoding/json"

	"github.com/Cyenoch/cordis/contract"
)

//go:embed capabilities.json
var capabilitiesJSON []byte

type sdkCapabilities struct {
	Schema   string            `json:"schema"`
	Protocol contract.Protocol `json:"protocol"`
	Features []string          `json:"features"`
}

var capabilities = func() sdkCapabilities {
	var value sdkCapabilities
	if err := json.Unmarshal(capabilitiesJSON, &value); err != nil {
		panic(err)
	}
	return value
}()
