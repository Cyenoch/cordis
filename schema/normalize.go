package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// normalizeNode keeps constraints exact while erasing spelling, map order and
// unordered declaration order from the descriptor used by generation and hashes.
func normalizeNode(n *node) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(n.raw, &obj); err != nil {
		return err
	}
	if !n.nullable {
		delete(obj, "nullable")
	}
	switch n.kind {
	case "object":
		props := map[string]json.RawMessage{}
		for _, key := range sortedKeys(n.properties) {
			child := n.properties[key]
			if err := normalizeNode(child); err != nil {
				return err
			}
			props[key] = child.raw
		}
		obj["properties"], _ = json.Marshal(props)
		obj["required"], _ = json.Marshal(n.requiredNames)
		obj["additionalProperties"] = json.RawMessage("false")
	case "array":
		if err := normalizeNode(n.items); err != nil {
			return err
		}
		obj["items"] = n.items.raw
	case "union":
		variants := []json.RawMessage{}
		for _, tag := range sortedKeys(n.variants) {
			child := n.variants[tag]
			if err := normalizeNode(child); err != nil {
				return err
			}
			variants = append(variants, child.raw)
		}
		obj["oneOf"], _ = json.Marshal(variants)
	}
	if n.format == "byte" {
		delete(obj, "format")
		obj["contentEncoding"] = json.RawMessage(`"base64"`)
	}
	if len(n.enum) > 0 {
		values := make([]json.RawMessage, len(n.enum))
		for i, value := range n.enum {
			normalized, err := canonicalJSON(value)
			if err != nil {
				return err
			}
			values[i] = normalized
		}
		sort.Slice(values, func(i, j int) bool { return bytes.Compare(values[i], values[j]) < 0 })
		obj["enum"], _ = json.Marshal(values)
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	n.raw, err = canonicalJSON(data)
	return err
}

func canonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	value = canonicalValue(value)
	return json.Marshal(value)
}

func canonicalValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for k, item := range v {
			v[k] = canonicalValue(item)
		}
	case []any:
		for i, item := range v {
			v[i] = canonicalValue(item)
		}
	case json.Number:
		r, err := numeric(json.RawMessage(v))
		if err == nil {
			text := r.FloatString(r.Denom().BitLen())
			if strings.Contains(text, ".") {
				text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
			}
			if text == "-0" {
				text = "0"
			}
			return json.Number(text)
		}
	}
	return value
}

func fingerprint(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
