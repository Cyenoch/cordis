package sdk

import (
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestEmbeddedCapabilitiesAreReadableByBuildTools(t *testing.T) {
	var value struct {
		Schema   string            `json:"schema"`
		Protocol contract.Protocol `json:"protocol"`
		Features []string          `json:"features"`
	}
	if err := contract.Decode(capabilitiesJSON, &value); err != nil {
		t.Fatal(err)
	}
	if value.Schema != "cordis-go.sdk/v1" {
		t.Fatalf("SDK metadata schema = %q", value.Schema)
	}
	peer := value.Protocol
	peer.RequiredFeatures = value.Features
	if _, err := capabilities.Protocol.Negotiate(peer); err != nil {
		t.Fatalf("SDK advertises unsupported capabilities: %v", err)
	}
}
