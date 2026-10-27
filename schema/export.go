package schema

import (
	"bytes"
	"encoding/json"
)

// Export projects the restricted data contract into JSON Schema 2020-12.
// Runtime-only constraints are documented with $comment, never silently claimed.
func Export(raw json.RawMessage) (json.RawMessage, error) {
	compiled, err := Compile(raw)
	if err != nil {
		return nil, err
	}
	obj := exportNode(compiled.root)
	obj["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	return json.Marshal(obj)
}

func exportNode(n *node) map[string]any {
	obj := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(n.raw))
	decoder.UseNumber()
	_ = decoder.Decode(&obj)
	delete(obj, "nullable")
	delete(obj, "discriminator")
	switch n.kind {
	case "object":
		props := map[string]any{}
		for _, key := range sortedKeys(n.properties) {
			props[key] = exportNode(n.properties[key])
		}
		obj["properties"] = props
		obj["additionalProperties"] = false
	case "array":
		obj["items"] = exportNode(n.items)
	case "union":
		variants := []any{}
		for _, tag := range sortedKeys(n.variants) {
			variants = append(variants, exportNode(n.variants[tag]))
		}
		obj["oneOf"] = variants
	case "number", "integer":
		obj["$comment"] = "Cordis additionally rejects float64 overflow and nonzero underflow, and mathematical integers outside [-9007199254740991,9007199254740991]; standard number validation is not equivalent."
	case "string":
		// Go RE2 is not ECMAScript regex: retain the rule as an annotation instead
		// of exporting potentially invalid syntax or a subtly different predicate.
		if n.pattern != nil {
			delete(obj, "pattern")
			obj["$comment"] = "Runtime Go RE2 pattern (not projected as ECMAScript regex): " + n.pattern.String()
		}
		switch n.format {
		case "int64", "uint64":
			delete(obj, "format")
			pattern := "^(0|[1-9][0-9]*)$"
			if n.format == "int64" {
				pattern = "^(0|-?[1-9][0-9]*)$"
			}
			obj["pattern"] = pattern
			addExportComment(obj, "Runtime also checks the full "+n.format+" range; decimal string pattern alone is not equivalent.")
		case "byte":
			delete(obj, "format")
			obj["contentEncoding"] = "base64"
			obj["pattern"] = "^([A-Za-z0-9+/]{4})*([A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$"
			addExportComment(obj, "Runtime requires canonical padded base64, including zero tail bits and no line breaks; contentEncoding is an annotation, and this pattern is not equivalent.")
		}
	}
	if n.nullable {
		// enum and const constrain null too, so keep them outside the null branch.
		outer := map[string]any{"anyOf": []any{obj, map[string]any{"type": "null"}}}
		for _, key := range []string{"enum", "const", "title", "description"} {
			if value, ok := obj[key]; ok {
				outer[key] = value
				delete(obj, key)
			}
		}
		return outer
	}
	return obj
}

func addExportComment(obj map[string]any, text string) {
	if existing, ok := obj["$comment"].(string); ok {
		obj["$comment"] = existing + "; " + text
		return
	}
	obj["$comment"] = text
}
