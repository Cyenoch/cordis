package contract_test

import (
	"encoding/json"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/schema"
)

func TestNumberRequiresExactPortableNumericToken(t *testing.T) {
	t.Parallel()
	for _, input := range []string{`"42"`, `"1e999"`, `null`, `{}`, `[]`, `9007199254740992`, `-9007199254740992`, `9.007199254740992e15`, `1e999`, `01`, `NaN`} {
		t.Run(input, func(t *testing.T) {
			var value json.Number
			if err := contract.DecodeData([]byte(input), &value); err == nil {
				t.Fatalf("accepted nonportable number %s", input)
			}
		})
	}
	for _, input := range []json.Number{"", "9007199254740992", "1e999", "NaN", "01"} {
		if _, err := contract.Encode(input); err == nil {
			t.Fatalf("encoded nonportable number %s", input)
		}
	}
	for _, input := range []string{"0.10000000000000001", "9007199254740991", "-9007199254740991", "2.25e-23"} {
		var value json.Number
		if err := contract.DecodeData([]byte(input), &value); err != nil {
			t.Fatal(err)
		}
		output, err := contract.Encode(value)
		if err != nil || string(output) != input {
			t.Fatalf("number changed: %s -> %s (%v)", input, output, err)
		}
	}
}

func TestNumberRulesApplyInsideSharedCodecs(t *testing.T) {
	t.Parallel()
	type payload struct {
		Values []json.Number                `json:"values"`
		Maybe  schema.Nullable[json.Number] `json:"maybe"`
	}
	for _, input := range []string{`{"values":["42"],"maybe":null}`, `{"values":[42],"maybe":"42"}`, `{"values":[42],"maybe":9007199254740992}`} {
		var value payload
		if err := contract.DecodeData([]byte(input), &value); err == nil {
			t.Fatalf("accepted invalid nested number %s", input)
		}
	}
	var value payload
	if _, err := contract.Encode(payload{Values: []json.Number{""}}); err == nil {
		t.Fatal("empty nested json.Number became zero")
	}
	if err := contract.DecodeData([]byte(`{"values":[0.10000000000000001],"maybe":null}`), &value); err != nil {
		t.Fatal(err)
	}
	if _, err := contract.Encode(value); err != nil {
		t.Fatal(err)
	}
}
