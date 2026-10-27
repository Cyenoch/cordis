package contract_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestNamespaceDataCodec(t *testing.T) {
	value := []byte{0, 0xff, 0x80, 1}
	data, err := contract.Encode(contract.NamespaceRequest{Operation: contract.NamespaceWrite, Key: "k", Value: value})
	if err != nil {
		t.Fatal(err)
	}
	var request contract.NamespaceRequest
	if err := contract.DecodeData(data, &request); err != nil || !bytes.Equal(request.Value, value) {
		t.Fatalf("request bytes = %x, %v", request.Value, err)
	}
	for _, raw := range []string{
		`{"operation":"write","key":"k","value":null}`,
		`{"operation":"write","key":"k","value":"not base64"}`,
		`{"operation":"write","key":"k","key":"other"}`,
		`{"operation":"write","key":"k","namespace":"other"}`,
	} {
		if err := contract.DecodeData([]byte(raw), &request); err == nil {
			t.Fatalf("accepted invalid request: %s", raw)
		}
	}
	var result contract.NamespaceResult
	for _, raw := range []string{
		`{"outcome":"committed","cause":"/private/path"}`,
		`{"outcome":"ok","value":null}`,
		`{"outcome":"ok","outcome":"committed"}`,
	} {
		if err := contract.DecodeData([]byte(raw), &result); err == nil {
			t.Fatalf("accepted invalid result: %s", raw)
		}
	}
}

func TestNamespaceResultRejectsContradictoryEvidence(t *testing.T) {
	for _, test := range []struct {
		operation contract.NamespaceOperation
		result    contract.NamespaceResult
	}{
		{operation: contract.NamespaceWrite, result: contract.NamespaceResult{}},
		{operation: contract.NamespaceWrite, result: contract.NamespaceResult{Outcome: "submission-unknown"}},
		{operation: contract.NamespaceRead, result: contract.NamespaceResult{Outcome: contract.NamespaceCommitted}},
		{operation: contract.NamespaceWrite, result: contract.NamespaceResult{Outcome: contract.NamespaceMissing}},
		{operation: contract.NamespaceDelete, result: contract.NamespaceResult{Outcome: contract.NamespaceOK, Value: []byte("v")}},
		{operation: contract.NamespaceRead, result: contract.NamespaceResult{Outcome: contract.NamespaceDenied, Value: []byte("secret")}},
	} {
		if err := test.result.Validate(test.operation); err == nil {
			data, err := json.Marshal(test.result)
			if err != nil {
				t.Fatal(err)
			}
			t.Fatalf("accepted contradictory result for %s: %s", test.operation, data)
		}
	}
}
