package packagehost

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestIndependentSigningInteroperabilityVector(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../schema/testdata/interoperability.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		SigningVectors []struct {
			Name     string           `json:"name"`
			Files    []IntegrityEntry `json:"files"`
			InputHex string           `json:"inputHex"`
			Digest   string           `json:"digestSHA256"`
			Ed25519  struct {
				Seed      string `json:"seedHex"`
				Public    string `json:"publicKeyHex"`
				Signature string `json:"signatureBase64"`
			} `json:"ed25519"`
		} `json:"signingVectors"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.SigningVectors) == 0 {
		t.Fatal("missing independent signing vector")
	}
	for _, v := range fixtures.SigningVectors {
		t.Run(v.Name, func(t *testing.T) {
			input, err := SignatureInput(v.Files)
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(input) != v.InputHex {
				t.Fatalf("canonical UTF-8 binary layout differs: %x", input)
			}
			hash := sha256.Sum256(input)
			if hex.EncodeToString(hash[:]) != v.Digest {
				t.Fatal("package digest differs")
			}
			seed, err := hex.DecodeString(v.Ed25519.Seed)
			if err != nil {
				t.Fatal(err)
			}
			key := ed25519.NewKeyFromSeed(seed)
			public, err := hex.DecodeString(v.Ed25519.Public)
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(key.Public().(ed25519.PublicKey)) != hex.EncodeToString(public) {
				t.Fatal("public key differs")
			}
			signature, err := base64.StdEncoding.DecodeString(v.Ed25519.Signature)
			if err != nil {
				t.Fatal(err)
			}
			if !ed25519.Verify(public, input, signature) || base64.StdEncoding.EncodeToString(ed25519.Sign(key, input)) != v.Ed25519.Signature {
				t.Fatal("fixed independent Ed25519 signature differs")
			}
			manifest := contract.Manifest{ID: "fixture.interop", Publisher: "fixture"}
			trust := TrustPolicy{Keys: map[string]TrustedKey{"fixture": {PublicKey: public, Publisher: "fixture"}}, Publishers: map[string][]string{"fixture": {"fixture.interop"}}}
			signed := &Signature{KeyID: "fixture", Signature: v.Ed25519.Signature}
			if err := trust.verify(manifest, v.Digest, input, signed, false); err != nil {
				t.Fatal(err)
			}
			input[len(input)-1] ^= 1
			if err := trust.verify(manifest, v.Digest, input, signed, false); err == nil {
				t.Fatal("tampered signed bytes accepted")
			}
		})
	}
}
