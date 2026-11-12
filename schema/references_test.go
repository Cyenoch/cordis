package schema_test

import (
	"encoding/json"
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
		if err := schema.Check(raw); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("leaf %d: Check = %v, want %q", tt.leaf, err, tt.want)
		}
	}
}
