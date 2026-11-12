package packagehost

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestTrustPolicyDigestApprovalAndSignatures(t *testing.T) {
	t.Parallel()
	artifact := sha256.Sum256([]byte("fixture artifact"))
	input, err := SignatureInput([]IntegrityEntry{{"plugin", 16, hex.EncodeToString(artifact[:])}})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(input)
	digest := hex.EncodeToString(sum[:])
	other := sha256.Sum256([]byte("other content"))
	newKey := func() (ed25519.PublicKey, ed25519.PrivateKey) {
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return pub, key
	}
	trustedPub, trustedKey := newKey()
	revokedPub, revokedKey := newKey()
	_, strangerKey := newKey()
	sign := func(id string, key ed25519.PrivateKey) *Signature {
		return &Signature{id, base64.StdEncoding.EncodeToString(ed25519.Sign(key, input))}
	}
	cases := []struct {
		name     string
		sig      *Signature
		approved string
		publish  func(*TrustPolicy)
		trusted  bool
	}{
		{name: "unsigned, approved digest", approved: digest, trusted: true},
		{name: "unknown key, approved digest", sig: sign("stranger", strangerKey), approved: digest, trusted: true},
		{name: "revoked key, approved digest", sig: sign("revoked", revokedKey), approved: digest, trusted: true},
		{name: "trusted key fails verification, approved digest", sig: sign("trusted", strangerKey), approved: digest, trusted: true},
		{name: "trusted key", sig: sign("trusted", trustedKey), trusted: true},
		{name: "unsigned"},
		{name: "unsigned, other digest approved", approved: hex.EncodeToString(other[:])},
		{name: "unknown key", sig: sign("stranger", strangerKey)},
		{name: "revoked key", sig: sign("revoked", revokedKey)},
		{name: "trusted key fails verification", sig: sign("trusted", strangerKey)},
		{name: "revoked publisher, approved digest", approved: digest, publish: func(p *TrustPolicy) {
			p.RevokedPublishers = map[string]bool{"example": true}
		}},
		{name: "unauthorized publisher, approved digest", approved: digest, publish: func(p *TrustPolicy) {
			p.Publishers = map[string][]string{"example": {"other"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := TrustPolicy{
				Keys: map[string]TrustedKey{
					"trusted": {PublicKey: trustedPub, Publisher: "example"},
					"revoked": {PublicKey: revokedPub, Publisher: "example", Revoked: true},
				},
				Publishers: map[string][]string{"example": {"demo"}},
				DevDigests: map[string]bool{tc.approved: tc.approved != ""},
			}
			if tc.publish != nil {
				tc.publish(&policy)
			}
			err := policy.verify(manifestFixture("demo", "1.0.0"), digest, input, tc.sig, false)
			if tc.trusted && err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if !tc.trusted && err == nil {
				t.Fatal("trusted")
			}
		})
	}
}
