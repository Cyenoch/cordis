package schema_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/schema"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		definition string
		value      string
		path       string
		keyword    string
	}{
		{"object valid", `{"type":"object","properties":{"count":{"type":"integer","minimum":1,"maximum":3}},"required":["count"]}`, `{"count":2}`, "", ""},
		{"field range", `{"type":"object","properties":{"count":{"type":"integer","minimum":1}},"required":["count"]}`, `{"count":0}`, "/count", "minimum"},
		{"upper range", `{"type":"number","maximum":0.3}`, `0.30000000000000001`, "", "maximum"},
		{"required", `{"type":"object","properties":{"a/b~":{"type":"boolean"}},"required":["a/b~"]}`, `{}`, "/a~1b~0", "required"},
		{"unknown property", `{"type":"object","properties":{}}`, `{"extra":true}`, "/extra", "additionalProperties"},
		{"nullable accepted", `{"type":"string","nullable":true}`, `null`, "", ""},
		{"implicit null rejected", `{"type":"string"}`, `null`, "", "nullable"},
		{"wrong type", `{"type":"boolean"}`, `"false"`, "", "type"},
		{"safe integer", `{"type":"integer"}`, `9007199254740991`, "", ""},
		{"unsafe integer", `{"type":"integer"}`, `9007199254740992`, "", "precision"},
		{"unsafe exponent", `{"type":"number"}`, `9.007199254740992e15`, "", "precision"},
		{"overflow", `{"type":"number"}`, `1e309`, "", "precision"},
		{"underflow", `{"type":"number"}`, `1e-400`, "", "precision"},
		{"int64 string", `{"type":"string","format":"int64"}`, `"9223372036854775807"`, "", ""},
		{"int64 low", `{"type":"string","format":"int64"}`, `"-9223372036854775808"`, "", ""},
		{"int64 overflow", `{"type":"string","format":"int64"}`, `"9223372036854775808"`, "", "format"},
		{"int64 canonical", `{"type":"string","format":"int64"}`, `"01"`, "", "format"},
		{"int64 numeric rejected", `{"type":"string","format":"int64"}`, `42`, "", "type"},
		{"base64", `{"type":"string","contentEncoding":"base64"}`, `"aGk="`, "", ""},
		{"base64 noncanonical", `{"type":"string","format":"byte"}`, `"aGl="`, "", "contentEncoding"},
		{"unicode length", `{"type":"string","maxLength":1}`, `"你"`, "", ""},
		{"string pattern", `{"type":"string","pattern":"^[a-z]+$"}`, `"ABC"`, "", "pattern"},
		{"array field", `{"type":"array","items":{"type":"integer"}}`, `[0,1.5]`, "/1", "type"},
		{"array bound", `{"type":"array","items":{"type":"boolean"},"maxItems":1}`, `[true,false]`, "", "maxItems"},
		{"enum numeric equality", `{"type":"number","enum":[1,2]}`, `1.0`, "", ""},
		{"enum reject", `{"type":"string","enum":["a"]}`, `"b"`, "", "enum"},
		{"duplicate key", `{"type":"object","properties":{"x":{"type":"integer"}}}`, `{"x":1,"x":2}`, "", "json"},
		{"trailing value", `{"type":"boolean"}`, `true false`, "", "json"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := schema.Validate(json.RawMessage(tt.definition), json.RawMessage(tt.value))
			if tt.keyword == "" {
				if err != nil {
					t.Fatalf("valid value rejected: %v", err)
				}
				return
			}
			var fe *schema.FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("want FieldError, got %v", err)
			}
			if fe.Path != tt.path || fe.Keyword != tt.keyword {
				t.Fatalf("got %s %s, want %s %s", fe.Path, fe.Keyword, tt.path, tt.keyword)
			}
		})
	}
}

func TestValidateTaggedUnion(t *testing.T) {
	t.Parallel()
	definition := json.RawMessage(`{"oneOf":[{"type":"object","properties":{"kind":{"type":"string","const":"text"},"text":{"type":"string"}},"required":["kind","text"]},{"type":"object","properties":{"kind":{"type":"string","const":"number"},"number":{"type":"number"}},"required":["kind","number"]}],"discriminator":{"propertyName":"kind"}}`)
	compiled, err := schema.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, value string
		valid       bool
	}{
		{"text", `{"kind":"text","text":"hello"}`, true},
		{"number", `{"kind":"number","number":3}`, true},
		{"unknown tag", `{"kind":"other","text":"hello"}`, false},
		{"missing tag", `{"text":"hello"}`, false},
		{"wrong branch", `{"kind":"text","number":3}`, false},
		{"extra branch", `{"kind":"text","text":"hello","number":3}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := compiled.Validate(json.RawMessage(tt.value))
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, got %v", tt.valid, err)
			}
		})
	}
}

func TestCompileRejectsUnsupportedSchemas(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, value string }{
		{"boolean schema", `true`},
		{"unknown keyword", `{"type":"string","default":"a"}`},
		{"arbitrary interface", `{"type":"interface"}`},
		{"ref", `{"$ref":"#/self"}`},
		{"type array", `{"type":["string","null"]}`},
		{"additional values", `{"type":"object","additionalProperties":true}`},
		{"undeclared required", `{"type":"object","required":["x"]}`},
		{"duplicate required", `{"type":"object","properties":{"x":{"type":"string"}},"required":["x","x"]}`},
		{"array without items", `{"type":"array"}`},
		{"negative length", `{"type":"string","minLength":-1}`},
		{"null length", `{"type":"string","minLength":null}`},
		{"impossible range", `{"type":"number","minimum":2,"maximum":1}`},
		{"null nullable", `{"type":"string","nullable":null}`},
		{"wrong enum", `{"type":"integer","enum":["x"]}`},
		{"duplicate enum", `{"type":"number","enum":[1,1.0]}`},
		{"bad pattern", `{"type":"string","pattern":"["}`},
		{"wrong const", `{"type":"string","const":1}`},
		{"unsupported format", `{"type":"string","format":"uuid"}`},
		{"untagged union", `{"oneOf":[{"type":"string"},{"type":"number"}]}`},
		{"tag not required", `{"oneOf":[{"type":"object","properties":{"kind":{"type":"string","const":"a"}}},{"type":"object","properties":{"kind":{"type":"string","const":"b"}},"required":["kind"]}],"discriminator":{"propertyName":"kind"}}`},
		{"duplicate schema key", `{"type":"string","type":"number"}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := schema.Compile(json.RawMessage(tt.value)); err == nil {
				t.Fatal("invalid schema accepted")
			}
		})
	}
}

// An object whose additionalProperties is a schema is a map: any key, every
// value held to that schema.
func TestMaps(t *testing.T) {
	const source = `{"type":"object","additionalProperties":{"type":"array","items":{"type":"integer","minimum":0}},"nullable":true}`
	compiled, err := schema.Compile(json.RawMessage(source))
	if err != nil {
		t.Fatal(err)
	}
	for value, valid := range map[string]bool{
		`{}`:                 true,
		`{"a":[1,2],"b":[]}`: true,
		`null`:               true,
		`{"a":[-1]}`:         false,
		`{"a":1}`:            false,
		`{"a":null}`:         false,
		`[]`:                 false,
		`{"a":[1],"a":[2]}`:  false,
	} {
		if err := compiled.Validate(json.RawMessage(value)); (err == nil) != valid {
			t.Errorf("Validate(%s) = %v, want valid=%v", value, err, valid)
		}
	}
	exported, err := schema.Export(json.RawMessage(source))
	if err != nil || !strings.Contains(string(exported), `"additionalProperties":{`) {
		t.Fatalf("Export = %s, %v", exported, err)
	}
	for name, invalid := range map[string]string{
		"with properties": `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":{"type":"string"}}`,
		"with required":   `{"type":"object","required":[],"additionalProperties":{"type":"string"}}`,
		"open":            `{"type":"object","additionalProperties":true}`,
		"invalid values":  `{"type":"object","additionalProperties":{"type":"nothing"}}`,
	} {
		if err := schema.Check(json.RawMessage(invalid)); err == nil {
			t.Errorf("%s: accepted %s", name, invalid)
		}
	}
	// Two spellings of one map have one fingerprint.
	a, err := schema.Parse([]byte(`{"schema":"cordis-go.contract/v1","definitions":[{"id":"main","config":{"type":"object","properties":{"m":{"type":"object","additionalProperties":{"type":"string","nullable":false}}}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := schema.Parse([]byte(`{"schema":"cordis-go.contract/v1","definitions":[{"id":"main","config":{"properties":{"m":{"additionalProperties":{"type":"string"},"type":"object"}},"type":"object"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := a.Fingerprint()
	second, _ := b.Fingerprint()
	if first != second {
		t.Fatal("equal maps have different fingerprints")
	}
}

// A value past the JSON limits is located once, without the code its check
// carries, which the caller adds.
func TestJSONLimitNamesWhere(t *testing.T) {
	t.Parallel()
	value := `{"a":{"b":[` + strings.Repeat("0,", 10000) + `0]}}`
	err := schema.Validate(json.RawMessage(`{"type":"object"}`), json.RawMessage(value))
	if err == nil || err.Error() != "/a/b: json: JSON array limit exceeded" {
		t.Fatalf("over-limit value = %v", err)
	}
}
