package schema_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Cyenoch/cordis/schema"
)

func TestExport(t *testing.T) {
	t.Parallel()
	source := json.RawMessage(`{"type":"object","properties":{"optional":{"type":"boolean"},"value":{"type":"string","nullable":true,"enum":[null,"ok"]},"choice":{"oneOf":[{"type":"object","properties":{"kind":{"type":"string","const":"a"}},"required":["kind"]},{"type":"object","properties":{"kind":{"type":"string","const":"b"}},"required":["kind"]}],"discriminator":{"propertyName":"kind"}}},"required":["choice","value"]}`)
	raw, err := schema.Export(source)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	if obj["$schema"] != "https://json-schema.org/draft/2020-12/schema" || obj["additionalProperties"] != false {
		t.Fatalf("invalid standard export: %s", raw)
	}
	props := obj["properties"].(map[string]any)
	choice := props["choice"].(map[string]any)
	if _, ok := choice["discriminator"]; ok {
		t.Fatal("private discriminator exported")
	}
	value := props["value"].(map[string]any)
	if _, ok := value["nullable"]; ok {
		t.Fatal("private nullable exported")
	}
	if len(value["anyOf"].([]any)) != 2 || len(value["enum"].([]any)) != 2 {
		t.Fatal("nullable/enum intersection lost")
	}
}

// Run explicitly where Python jsonschema is supplied; no Go dependency or
// network installation is hidden inside normal unit tests.
func TestExportIndependentValidator(t *testing.T) {
	if os.Getenv("CORDIS_SCHEMA_STANDARD_VALIDATOR") != "1" {
		t.Skip("set CORDIS_SCHEMA_STANDARD_VALIDATOR=1 with Python jsonschema installed")
	}
	data, err := os.ReadFile("testdata/export-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Schema  json.RawMessage   `json:"schema"`
		Valid   []json.RawMessage `json:"valid"`
		Invalid []json.RawMessage `json:"invalid"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	standard := []map[string]any{}
	for _, v := range vectors {
		exported, err := schema.Export(v.Schema)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range v.Valid {
			if err := schema.Validate(v.Schema, value); err != nil {
				t.Fatalf("invalid valid vector: %s: %v", value, err)
			}
		}
		for _, value := range v.Invalid {
			if err := schema.Validate(v.Schema, value); err == nil {
				t.Fatalf("valid invalid vector: %s", value)
			}
		}
		standard = append(standard, map[string]any{"schema": exported, "valid": v.Valid, "invalid": v.Invalid})
	}
	raw, err := json.Marshal(standard)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "vectors.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "python3", "testdata/validate-export.py", path, "contract.schema.json")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("independent validator: %v\n%s", err, output)
	}
	t.Log(string(output))
}
