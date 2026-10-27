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
	large, err := json.Marshal(map[string]string{"type": "string", "description": strings.Repeat("x", 8<<10)})
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
