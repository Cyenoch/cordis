package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Cyenoch/cordis/contract"
)

// References are expanded before compilation, so every downstream consumer sees
// the same finite schema rather than needing its own reference resolver.
func resolveSchema(raw json.RawMessage, types map[string]json.RawMessage) (json.RawMessage, error) {
	r := resolver{types: types, active: map[string]bool{}}
	return r.resolve(raw)
}

func (r *resolver) resolve(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > maxResolvedBytes {
		return nil, field("/schema", "$ref", "schema exceeds byte limit")
	}
	if err := contract.CheckJSON(raw, 32, 10000); err != nil {
		return nil, field("/schema", "json", err.Error())
	}

	return r.expandRoot(raw, "root", 0)
}

func (r *resolver) expandRoot(raw json.RawMessage, scope string, depth int) (json.RawMessage, error) {
	if err := r.consume(len(raw)); err != nil {
		return nil, err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil || root == nil {
		return nil, field("/schema", "schema", "expected a schema object")
	}
	defs := map[string]json.RawMessage{}
	if value, exists := root["$defs"]; exists {
		if err := json.Unmarshal(value, &defs); err != nil || defs == nil {
			return nil, field("/schema/$defs", "$defs", "expected object")
		}
		delete(root, "$defs")
	}
	oldDefs, oldScope := r.defs, r.scope
	r.defs, r.scope = defs, scope
	defer func() { r.defs, r.scope = oldDefs, oldScope }()
	for _, name := range sortedKeys(defs) {
		expanded, err := r.reference("#/$defs/"+escapeReference(name), depth)
		if err != nil {
			return nil, err
		}
		if _, err := compile(expanded, pointer("/schema/$defs", name), depth); err != nil {
			return nil, err
		}
	}
	data, err := r.marshalObject(root)
	if err != nil {
		return nil, err
	}
	return r.expand(data, depth)
}

type resolver struct {
	types  map[string]json.RawMessage
	defs   map[string]json.RawMessage
	scope  string
	active map[string]bool
	nodes  int
	bytes  int
}

const maxResolvedBytes = 1 << 20

func (r *resolver) consume(size int) error {
	if size > maxResolvedBytes-r.bytes {
		return field("/schema", "$ref", "expanded schema exceeds cumulative byte limit")
	}
	r.bytes += size
	return nil
}

// Reserve the serialized copy before encoding it. RawMessage encoding can
// escape HTML and line separators, so include that expansion in the budget.
func encodedSize(raw []byte) int {
	return len(raw) + 5*(bytes.Count(raw, []byte("<"))+bytes.Count(raw, []byte(">"))+bytes.Count(raw, []byte("&"))) +
		3*(bytes.Count(raw, []byte("\u2028"))+bytes.Count(raw, []byte("\u2029")))
}

func (r *resolver) marshalObject(object map[string]json.RawMessage) (json.RawMessage, error) {
	size := 2
	for key, raw := range object {
		size += 6*len(key) + 4 + encodedSize(raw)
	}
	if err := r.consume(size); err != nil {
		return nil, err
	}
	return json.Marshal(object)
}

func escapeReference(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
}

func (r *resolver) reference(ref string, depth int) (json.RawMessage, error) {
	var values map[string]json.RawMessage
	var name string
	switch {
	case strings.HasPrefix(ref, "#/$defs/"):
		values, name = r.defs, strings.TrimPrefix(ref, "#/$defs/")
	case strings.HasPrefix(ref, "#/types/"):
		values, name = r.types, strings.TrimPrefix(ref, "#/types/")
	default:
		return nil, field("/schema", "$ref", "only #/$defs/Name and #/types/Name are supported")
	}
	if name == "" || strings.Contains(name, "/") {
		return nil, field("/schema", "$ref", "expected a single local name")
	}
	name = strings.ReplaceAll(strings.ReplaceAll(name, "~1", "/"), "~0", "~")
	if escapeReference(name) != strings.TrimPrefix(strings.TrimPrefix(ref, "#/$defs/"), "#/types/") {
		return nil, field("/schema", "$ref", "invalid JSON Pointer escape")
	}
	value, exists := values[name]
	if !exists {
		return nil, field("/schema", "$ref", "unknown reference: "+ref)
	}
	key := ref
	if strings.HasPrefix(ref, "#/$defs/") {
		key = r.scope + "\x00" + ref
	}
	if r.active[key] {
		return nil, field("/schema", "$ref", "cyclic reference: "+ref)
	}
	r.active[key] = true
	var result json.RawMessage
	var err error
	if strings.HasPrefix(ref, "#/types/") {
		result, err = r.expandRoot(value, "type "+name, depth+1)
	} else {
		result, err = r.expand(value, depth+1)
	}
	delete(r.active, key)
	return result, err
}

func (r *resolver) expand(raw json.RawMessage, depth int) (json.RawMessage, error) {
	// Charge each copied reference before decoding or growing its parent output.
	if err := r.consume(len(raw)); err != nil {
		return nil, err
	}
	r.nodes++
	if depth > 32 || r.nodes > 10000 {
		return nil, field("/schema", "$ref", "expanded schema exceeds depth or node limit")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, field("/schema", "schema", "expected a schema object")
	}
	if value, exists := obj["$ref"]; exists {
		var ref string
		if err := json.Unmarshal(value, &ref); err != nil || len(obj) != 1 {
			return nil, field("/schema", "$ref", "reference must be a string without sibling keywords")
		}
		return r.reference(ref, depth)
	}
	if value, exists := obj["properties"]; exists {
		var children map[string]json.RawMessage
		if err := json.Unmarshal(value, &children); err != nil || children == nil {
			return nil, field("/schema", "properties", "expected object")
		}
		for _, name := range sortedKeys(children) {
			child, err := r.expand(children[name], depth+1)
			if err != nil {
				return nil, fmt.Errorf("property %q: %w", name, err)
			}
			children[name] = child
		}
		var err error
		obj["properties"], err = r.marshalObject(children)
		if err != nil {
			return nil, err
		}
	}
	if value, exists := obj["items"]; exists {
		child, err := r.expand(value, depth+1)
		if err != nil {
			return nil, err
		}
		obj["items"] = child
	}
	if value, exists := obj["oneOf"]; exists {
		var children []json.RawMessage
		if err := json.Unmarshal(value, &children); err != nil {
			return nil, field("/schema", "oneOf", "expected array")
		}
		for i, child := range children {
			resolved, err := r.expand(child, depth+1)
			if err != nil {
				return nil, err
			}
			children[i] = resolved
		}
		size := 2
		for _, child := range children {
			size += 1 + encodedSize(child)
		}
		if err := r.consume(size); err != nil {
			return nil, err
		}
		obj["oneOf"], _ = json.Marshal(children)
	}
	return r.marshalObject(obj)
}
