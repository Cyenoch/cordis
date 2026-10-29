package contract_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestScopeIdentitySchemaInteroperability(t *testing.T) {
	raw, err := os.ReadFile("../schema/testdata/interoperability.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Vectors []struct {
			Name     string          `json:"name"`
			Pointer  string          `json:"pointer"`
			Accept   bool            `json:"accept"`
			Instance json.RawMessage `json:"instance"`
		} `json:"schemaVectors"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, vector := range fixtures.Vectors {
		if vector.Pointer != "/$defs/serviceKey" && vector.Pointer != "/$defs/subscriptionID" {
			continue
		}
		t.Run(vector.Name, func(t *testing.T) {
			var key string
			if err := json.Unmarshal(vector.Instance, &key); err != nil {
				t.Fatal(err)
			}
			var err error
			if vector.Pointer == "/$defs/serviceKey" {
				err = contract.CheckIsolate([]string{key})
			} else {
				_, err = contract.SubscriptionNumber("activation", key)
			}
			if (err == nil) != vector.Accept {
				t.Fatalf("schema/runtime disagreement: %v", err)
			}
		})
	}
}
