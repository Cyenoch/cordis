package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/Cyenoch/cordis/schema"
)

// A schema and a value from anywhere: compiling and validating never panic,
// a schema that compiles also exports, and validating twice agrees.
func FuzzValidate(f *testing.F) {
	for _, seed := range [][2]string{
		{`{"type":"object","properties":{"a":{"type":"integer","minimum":0}},"required":["a"]}`, `{"a":1}`},
		{`{"type":"array","items":{"type":"string","minLength":1,"pattern":"^a"},"maxItems":2,"nullable":true}`, `["ab",null]`},
		{`{"type":"string","format":"int64"}`, `"-9223372036854775808"`},
		{`{"type":"string","contentEncoding":"base64"}`, `"aGk="`},
		{`{"type":"number","enum":[1,2.5,1e2]}`, `1e2`},
		{`{"oneOf":[{"type":"object","properties":{"k":{"type":"string","const":"a"}},"required":["k"]},{"type":"object","properties":{"k":{"type":"string","const":"b"}},"required":["k"]}],"discriminator":{"propertyName":"k"}}`, `{"k":"b"}`},
		{`{"$defs":{"x":{"type":"boolean"}},"type":"array","items":{"$ref":"#/$defs/x"}}`, `[true]`},
		{`{"type":"integer","minimum":5,"maximum":1}`, `3`},
		{`{}`, `{}`}, {`null`, `null`}, {`{"type":"object","properties":{"a":{"$ref":"#"}}}`, `{"a":{}}`},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, source, value string) {
		if len(source) > 4096 || len(value) > 4096 {
			t.Skip()
		}
		compiled, err := schema.Compile(json.RawMessage(source))
		if err != nil {
			return
		}
		if _, err := schema.Export(json.RawMessage(source)); err != nil {
			t.Fatalf("a schema that compiles does not export: %v\n%s", err, source)
		}
		first, second := compiled.Validate(json.RawMessage(value)), compiled.Validate(json.RawMessage(value))
		if (first == nil) != (second == nil) {
			t.Fatalf("validation is not stable: %v then %v", first, second)
		}
		if first == nil && !json.Valid([]byte(value)) {
			t.Fatalf("accepted invalid JSON %q", value)
		}
	})
}
