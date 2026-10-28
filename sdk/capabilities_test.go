package sdk

import "testing"

func TestAdvertisedCapabilitiesAreNegotiable(t *testing.T) {
	peer := capabilities.Protocol
	peer.RequiredFeatures = capabilities.Features
	if _, err := capabilities.Protocol.Negotiate(peer); err != nil {
		t.Fatalf("SDK advertises unsupported capabilities: %v", err)
	}
}
