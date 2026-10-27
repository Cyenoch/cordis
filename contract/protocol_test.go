package contract_test

import (
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestProtocolSelectsHighestCommonMinorAndRejectsRequiredUnknownFeatures(t *testing.T) {
	host := contract.Protocol{Major: 1, MinMinor: 1, MaxMinor: 4}
	plugin := contract.Protocol{Major: 1, MinMinor: 2, MaxMinor: 3, RequiredFeatures: []string{"cancel", "parent", "children"}}
	if minor, err := host.Negotiate(plugin); err != nil || minor != 3 {
		t.Fatalf("wrong negotiation: %d %v", minor, err)
	}
	for _, incompatible := range []contract.Protocol{{Major: 2}, {Major: 1, MinMinor: 5, MaxMinor: 8}, {Major: 1, RequiredFeatures: []string{"arbitrary-host-objects"}}} {
		if _, err := host.Negotiate(incompatible); err == nil {
			t.Fatalf("incompatible protocol accepted: %+v", incompatible)
		}
	}
}
