package schema_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/schema"
)

func TestReferenceExpansionHasSharedByteBudget(t *testing.T) {
	for _, count := range []int{1, 100} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			properties := map[string]any{}
			for i := range count {
				properties[fmt.Sprint(i)] = map[string]string{"$ref": "#/$defs/large"}
			}
			raw, err := json.Marshal(map[string]any{
				"type": "object", "properties": properties,
				"$defs": map[string]any{"large": map[string]string{"type": "string", "description": strings.Repeat("x", 32<<10)}},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = schema.Export(raw)
			if count == 1 && err != nil {
				t.Fatal(err)
			}
			if count == 100 && (err == nil || !strings.Contains(err.Error(), "byte limit")) {
				t.Fatalf("reference amplification not bounded: %v", err)
			}
		})
	}
}

func TestDeclarationSharesReferenceBudgetAcrossDefinitions(t *testing.T) {
	large, err := json.Marshal(map[string]string{"type": "string", "description": strings.Repeat("x", 16<<10)})
	if err != nil {
		t.Fatal(err)
	}
	d := schema.Definition{Schema: schema.DefinitionSchema, Types: map[string]json.RawMessage{"large": large}}
	for i := range 100 {
		d.Definitions = append(d.Definitions, schema.PluginDefinition{ID: fmt.Sprintf("definition%d", i), Config: json.RawMessage(`{"$ref":"#/types/large"}`)})
	}
	if _, err := d.Specs(); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("separate definitions bypassed graph budget: %v", err)
	}
}

// nested is a schema whose one leaf, a string described by leaf bytes, sits
// depth objects down.
func nested(depth, leaf int) map[string]any {
	s := map[string]any{"type": "string", "description": strings.Repeat("x", leaf)}
	for range depth {
		s = map[string]any{"type": "object", "properties": map[string]any{"a": s}, "required": []string{"a"}}
	}
	return s
}

// The budget bounds the expanded schema, so a byte deep in the tree counts
// once, not once for every object around it.
func TestExpansionBudgetCountsEachByteOnce(t *testing.T) {
	raw, err := json.Marshal(nested(15, 600<<10))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Check(raw); err != nil {
		t.Fatal(err)
	}
}

// Whitespace in a contract is not expanded, so its layout cannot change
// whether the contract fits: a type referenced many times costs its compact
// size each time.
func TestExpansionBudgetIgnoresWhitespace(t *testing.T) {
	deep, err := json.Marshal(nested(12, 8<<10))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		references int
		want       string
	}{{100, ""}, {130, "byte limit"}} {
		d := schema.Definition{Schema: schema.DefinitionSchema, Types: map[string]json.RawMessage{"Deep": deep}}
		for i := range tt.references {
			d.Definitions = append(d.Definitions, schema.PluginDefinition{ID: fmt.Sprintf("definition%d", i), Config: json.RawMessage(`{"$ref":"#/types/Deep"}`)})
		}
		compact, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		indented, err := json.MarshalIndent(d, "", strings.Repeat(" ", 256))
		if err != nil {
			t.Fatal(err)
		}
		for layout, data := range map[string][]byte{"compact": compact, "indented": indented} {
			_, err := schema.Parse(data)
			if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Errorf("%d references, %s: Parse = %v, want %q", tt.references, layout, err, tt.want)
			}
		}
	}
}

// Ten layers of $defs, each referring to the one below ten times, would
// expand to ten billion leaves; the limits stop it before the copies exist.
func TestReferenceAmplificationIsRejected(t *testing.T) {
	for _, tt := range []struct {
		leaf int
		want string
	}{{0, "node limit"}, {20 << 10, "byte limit"}} {
		defs := map[string]any{"a0": map[string]any{"type": "string", "description": strings.Repeat("x", tt.leaf)}}
		for layer := 1; layer < 10; layer++ {
			properties := map[string]any{}
			for i := range 10 {
				properties[fmt.Sprint("p", i)] = map[string]string{"$ref": fmt.Sprintf("#/$defs/a%d", layer-1)}
			}
			defs[fmt.Sprint("a", layer)] = map[string]any{"type": "object", "properties": properties}
		}
		raw, err := json.Marshal(map[string]any{"$ref": "#/$defs/a9", "$defs": defs})
		if err != nil {
			t.Fatal(err)
		}
		var fe *schema.FieldError
		if err := schema.Check(raw); err == nil || !strings.Contains(err.Error(), tt.want) || !errors.As(err, &fe) || fe.Keyword != "limit" {
			t.Errorf("leaf %d: Check = %v, want %q with keyword limit", tt.leaf, err, tt.want)
		}
	}
}

// A $defs entry is charged when it is referenced, not again as part of the
// document that holds it: this schema expands to a few bytes and was accepted
// before $defs counted twice.
func TestDefsAreNotChargedAsPartOfTheirDocument(t *testing.T) {
	raw := json.RawMessage(`{"type":"string","$defs":{"d":{"type":"string","description":"` + strings.Repeat("<", 100_000) + `"}}}`)
	if err := schema.Check(raw); err != nil {
		t.Fatalf("Check: %v", err)
	}
	contract := `{"schema":"cordis-go.contract/v1","definitions":[{"id":"main","config":` + string(raw) + `}]}`
	if _, err := schema.Parse([]byte(contract)); err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

// Encoding a schema escapes <, > and & to six bytes each, and U+2028 and
// U+2029 from three to six, so the budget charges them at their escaped size:
// five copies of 100,000 literal "<" expand to about 3 MB, and five copies of
// 50,000 U+2028 to about 1.5 MB, while five copies of 100,000 "x" fit.
func TestExpansionBudgetChargesEscapedBytes(t *testing.T) {
	for _, tt := range []struct {
		char  string
		count int
		want  string
	}{{"x", 100_000, ""}, {"<", 100_000, "byte limit"}, {"\u2028", 50_000, "byte limit"}} {
		raw := `{"type":"object","properties":{` +
			`"p0":{"$ref":"#/$defs/d"},"p1":{"$ref":"#/$defs/d"},"p2":{"$ref":"#/$defs/d"},"p3":{"$ref":"#/$defs/d"},"p4":{"$ref":"#/$defs/d"}},` +
			`"$defs":{"d":{"type":"string","description":"` + strings.Repeat(tt.char, tt.count) + `"}}}`
		err := schema.Check(json.RawMessage(raw))
		if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
			t.Errorf("%q: Check = %v, want %q", tt.char, err, tt.want)
		}
	}
}

// An expansion limit is reported with the keyword limit, also when the schema
// has no reference at all.
func TestExpansionLimitErrorNamesLimit(t *testing.T) {
	raw := json.RawMessage(`{"type":"string","description":"` + strings.Repeat("<", 200_000) + `"}`)
	var fe *schema.FieldError
	if err := schema.Check(raw); !errors.As(err, &fe) || fe.Path != "/schema" || fe.Keyword != "limit" || !strings.Contains(fe.Message, "byte limit") {
		t.Fatalf("Check = %v, want /schema: limit: ... byte limit ...", err)
	}
}

// A Budget is shared by every schema checked against it: each of these
// schemas fits alone, and the second one exhausts the budget.
func TestBudgetIsSharedAcrossSchemas(t *testing.T) {
	raw, err := json.Marshal(nested(3, 600<<10))
	if err != nil {
		t.Fatal(err)
	}
	budget := schema.NewBudget()
	if err := budget.Check(raw); err != nil {
		t.Fatal(err)
	}
	if err := budget.Check(raw); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("second Check = %v, want the byte limit", err)
	}
	if err := schema.Check(raw); err != nil {
		t.Fatalf("package-level Check shares a budget: %v", err)
	}

	// The node limit is shared too: 2,501 nodes each, the fourth schema
	// exceeds 10,000.
	properties := map[string]any{}
	for i := range 2500 {
		properties[fmt.Sprint("p", i)] = map[string]string{"type": "string"}
	}
	wide, err := json.Marshal(map[string]any{"type": "object", "properties": properties})
	if err != nil {
		t.Fatal(err)
	}
	budget = schema.NewBudget()
	for i := range 3 {
		if err := budget.Check(wide); err != nil {
			t.Fatalf("Check %d: %v", i+1, err)
		}
	}
	if err := budget.Check(wide); err == nil || !strings.Contains(err.Error(), "node limit") {
		t.Fatalf("fourth Check = %v, want the node limit", err)
	}
}

// Every expansion of a type checks each entry of its $defs, referenced or not,
// and the check copies the entry, so it is charged when the contract checks the
// type and again at every reference to it: one reference to a type with a
// 400 KiB $defs entry fits, two do not.
func TestTypeDefsAreChargedAtEachReference(t *testing.T) {
	typ, err := json.Marshal(map[string]any{
		"type":  "string",
		"$defs": map[string]any{"big": map[string]string{"type": "string", "description": strings.Repeat("x", 400<<10)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		references int
		want       string
	}{{1, ""}, {2, "byte limit"}} {
		d := schema.Definition{Schema: schema.DefinitionSchema, Types: map[string]json.RawMessage{"T": typ}}
		for i := range tt.references {
			d.Definitions = append(d.Definitions, schema.PluginDefinition{ID: fmt.Sprintf("definition%d", i), Config: json.RawMessage(`{"type":"object","properties":{"t":{"$ref":"#/types/T"}}}`)})
		}
		data, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := schema.Parse(data); tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
			t.Errorf("%d references: Parse = %v, want %q", tt.references, err, tt.want)
		}
	}
}
