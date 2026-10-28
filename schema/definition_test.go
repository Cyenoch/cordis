package schema_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/schema"
)

func TestMissingConsumerRangeRetainsFieldLocation(t *testing.T) {
	source := `{"schema":"cordis-go.contract/v1","services":[{"name":"echo","version":1,"methods":[{"name":"echo","request":{"type":"string"},"response":{"type":"string"}}]}],"definitions":[{"id":"client","requires":[{"name":"source","service":"echo","maxMinor":0}]}]}`
	_, err := schema.Parse([]byte(source))
	var field *schema.FieldError
	if !errors.As(err, &field) || field.Path != "/definitions/0/requires/0/minMinor" || field.Keyword != "required" {
		t.Fatalf("missing consumer range lost its field location: %v", err)
	}
}

func TestDeclarationSpecsAndFingerprint(t *testing.T) {
	t.Parallel()
	source := `{"schema":"cordis-go.contract/v1","types":{"Config":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}},"services":[{"name":"echo","version":"1.1","methods":[{"name":"echo","request":{"type":"string"},"response":{"type":"string"}}]}],"definitions":[{"id":"viewer","config":{"$ref":"#/types/Config"},"requires":[{"name":"source","service":"echo","minMinor":1,"maxMinor":3}]},{"id":"provider","provides":["echo"]}]}`
	d, err := schema.Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	specs, err := d.Specs()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 || specs[0].ID != "provider" || len(specs[0].Provides) != 1 || specs[1].Requires[0].Requirement.MaxMinor != 3 {
		t.Fatalf("unexpected specs: %+v", specs)
	}
	if err := schema.Validate(specs[1].ConfigSchema, []byte(`{}`)); err == nil {
		t.Fatal("resolved required config field lost")
	}
	c := specs[1].Requires[0]
	if err := c.Check("echo", 1, 1, c.Contract); err != nil {
		t.Fatal(err)
	}
	if specs[1].Requirements()[0] != c.Requirement {
		t.Fatal("runtime and Manifest route differ")
	}
	fp, err := d.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	// Equivalent ordering and numeric spelling normalize to the same identity.
	reordered := strings.Replace(source, `"required":["title"]`, `"additionalProperties":false,"required":["title"]`, 1)
	d2, err := schema.Parse([]byte(reordered))
	if err != nil {
		t.Fatal(err)
	}
	fp2, err := d2.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if fp != fp2 || len(fp) != 64 {
		t.Fatalf("unstable fingerprint: %s != %s", fp, fp2)
	}
	d.Services[0].Minor = 2
	next, err := d.Specs()
	if err != nil {
		t.Fatal(err)
	}
	if next[1].Requires[0].Contract != c.Contract {
		t.Fatal("provider minor changed local method identity")
	}
	d.Services[0].Methods[0].Request = json.RawMessage(`{"type":"string","minLength":1}`)
	next, err = d.Specs()
	if err != nil {
		t.Fatal(err)
	}
	if next[1].Requires[0].Contract == c.Contract {
		t.Fatal("changed method schema retained consumer identity")
	}
	changed, _ := d.Fingerprint()
	if fp == changed {
		t.Fatal("changed declaration retained build fingerprint")
	}
}

func TestDeclarationRejectsInvalidGraph(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, source string }{
		{"float version", `{"services":[{"name":"echo","version":1.2,"methods":[]}]}`},
		{"patch version", `{"services":[{"name":"echo","version":"1.2.3","methods":[]}]}`},
		{"old major minor", `{"services":[{"name":"echo","major":1,"minor":0,"methods":[]}]}`},
		{"missing version", `{"services":[{"name":"echo","methods":[]}]}`},
		{"unknown reference", `{"definitions":[{"id":"config","config":{"$ref":"#/types/Missing"}}]}`},
		{"unused type cycle", `{"types":{"A":{"$ref":"#/types/B"},"B":{"$ref":"#/types/A"}}}`},
		{"unknown service", `{"definitions":[{"id":"config","provides":["missing"]}]}`},
		{"null config", `{"definitions":[{"id":"config","config":null}]}`},
		{"network ref", `{"types":{"A":{"$ref":"https://example.com/a"}}}`},
		{"ref siblings", `{"types":{"A":{"type":"string"},"B":{"$ref":"#/types/A","maxLength":2}}}`},
		{"null definition", `{"definitions":[null]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := strings.Replace(tt.source, "{", `{"schema":"cordis-go.contract/v1",`, 1)
			if _, err := schema.Parse([]byte(raw)); err == nil {
				t.Fatal("invalid declaration accepted")
			}
		})
	}
}

func TestCompileLocalDefinitions(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"$defs":{"Name":{"type":"string","minLength":2}},"type":"object","properties":{"name":{"$ref":"#/$defs/Name"}},"required":["name"]}`)
	compiled, err := schema.Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate([]byte(`{"name":"ok"}`)); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate([]byte(`{"name":"x"}`)); err == nil {
		t.Fatal("reference constraint lost")
	}
	for _, raw := range []string{
		`{"$defs":{"X":{"$ref":"#/$defs/X"}},"type":"string"}`,
		`{"$defs":{"X":{"type":"string","unknown":true}},"type":"string"}`,
		`{"$ref":"file:///tmp/schema.json"}`,
		`{"$defs":{"X":{"type":"string"}},"$ref":"#/$defs/X/nested"}`,
	} {
		if _, err := schema.Compile([]byte(raw)); err == nil {
			t.Fatalf("invalid local graph accepted: %s", raw)
		}
	}
}

func TestFingerprintCanonicalNumbersAndSetOrder(t *testing.T) {
	t.Parallel()
	source := `{"schema":"cordis-go.contract/v1","types":{"Root":{"type":"object","properties":{"a":{"type":"number","minimum":0.1,"enum":[0.2,0.1]},"b":{"type":"boolean"}},"required":["b","a"]}},"definitions":[{"id":"config","config":{"$ref":"#/types/Root"}}]}`
	d, err := schema.Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	reordered := strings.ReplaceAll(strings.ReplaceAll(source, `"required":["b","a"]`, `"required":["a","b"]`), `"enum":[0.2,0.1]`, `"enum":[0.10,2e-1]`)
	d2, err := schema.Parse([]byte(reordered))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := d.Fingerprint()
	b, _ := d2.Fingerprint()
	if a != b {
		t.Fatalf("set/numeric spelling affects fingerprint: %s != %s", a, b)
	}
}

func TestConsumerRangeMustBeExplicitIncludingZero(t *testing.T) {
	t.Parallel()
	source := `{"schema":"cordis-go.contract/v1","services":[{"name":"echo","version":1,"methods":[{"name":"echo","request":{"type":"string"},"response":{"type":"string"}}]}],"definitions":[{"id":"client","requires":[{"name":"source","service":"echo","minMinor":0,"maxMinor":0}]}]}`
	d, err := schema.Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	specs, err := d.Specs()
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Requires[0].Requirement.MinMinor != 0 || specs[0].Requires[0].Requirement.MaxMinor != 0 {
		t.Fatal("explicit zero range changed")
	}
	for _, key := range []string{"minMinor", "maxMinor"} {
		t.Run(key, func(t *testing.T) {
			for _, raw := range []string{
				strings.Replace(source, `,"`+key+`":0`, "", 1),
				strings.Replace(source, `"`+key+`":0`, `"`+key+`":null`, 1),
			} {
				if _, err := schema.Parse([]byte(raw)); err == nil {
					t.Fatalf("missing/null range accepted: %s", raw)
				}
			}
		})
	}
}

func TestNamedTypeRetainsItsLocalDefinitionScope(t *testing.T) {
	t.Parallel()
	source := `{"schema":"cordis-go.contract/v1","types":{"Settings":{"$defs":{"Value":{"type":"string","minLength":2}},"type":"object","properties":{"value":{"$ref":"#/$defs/Value"}},"required":["value"]}},"definitions":[{"id":"panel","config":{"$defs":{"Value":{"type":"boolean"}},"$ref":"#/types/Settings"}}]}`
	d, err := schema.Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	specs, err := d.Specs()
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(specs[0].ConfigSchema, []byte(`{"value":"ok"}`)); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(specs[0].ConfigSchema, []byte(`{"value":true}`)); err == nil {
		t.Fatal("reference resolved against caller's local definitions")
	}
}

func TestConsumerMethodIdentityRejectsDifferentLocalContract(t *testing.T) {
	t.Parallel()
	source := `{"schema":"cordis-go.contract/v1","services":[{"name":"echo","version":"1.1","methods":[{"name":"echo","request":{"type":"string"},"response":{"type":"string"}}]}],"definitions":[{"id":"client","requires":[{"name":"source","service":"echo","minMinor":1,"maxMinor":3}]}]}`
	a, err := schema.Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	b, err := schema.Parse([]byte(strings.Replace(source, `"request":{"type":"string"}`, `"request":{"type":"boolean"}`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.Specs()
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Specs()
	if err != nil {
		t.Fatal(err)
	}
	consumer := first[0].Requires[0]
	wrong := second[0].Requires[0]
	if err := wrong.Check("echo", 1, 1, consumer.Contract); err == nil {
		t.Fatal("same service/major with a different method schema accepted")
	}
}
