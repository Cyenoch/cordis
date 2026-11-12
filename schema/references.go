package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Cyenoch/cordis/contract"
)

// newResolver keeps the shared types in compact form, so that a reference is
// charged for the bytes it copies and never for the layout of its source.
func newResolver(types map[string]json.RawMessage) (*resolver, error) {
	r := &resolver{types: make(map[string]json.RawMessage, len(types)), active: map[string]bool{}}
	for _, name := range sortedKeys(types) {
		// Decoding replaces each invalid byte with U+FFFD, three bytes long, so a
		// copy could outgrow the bytes charged for it.
		if !utf8.Valid(types[name]) {
			return nil, fmt.Errorf("type %q: schema is not valid UTF-8", name)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, types[name]); err != nil {
			return nil, fmt.Errorf("type %q: %w", name, jsonField("/schema", err))
		}
		r.types[name] = compact.Bytes()
	}
	return r, nil
}

// resolve expands references before compilation, so every downstream consumer
// sees the same finite schema rather than needing its own reference resolver.
func (r *resolver) resolve(raw json.RawMessage) (json.RawMessage, error) {
	if err := contract.CheckJSON(raw, 32, 10000); err != nil {
		return nil, jsonField("/schema", err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, jsonField("/schema", err)
	}
	return r.expandRoot(compact.Bytes(), "root", 0)
}

// expandRoot expands a root document or a referenced type. Its body, without
// $defs, is charged here; each $defs entry is charged when a reference visits
// it, so no byte of $defs is charged as part of the body as well.
func (r *resolver) expandRoot(raw json.RawMessage, scope string, depth int) (json.RawMessage, error) {
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
	// Marshal escapes HTML and line separators, so data is the body as it is
	// copied into the expansion.
	data, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	if err := r.consume(len(data)); err != nil {
		return nil, err
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

// maxResolvedBytes bounds the expanded schemas of one resolver: the bytes
// copied into them, each counted once. It bounds the result, not the work:
// every level of nesting decodes and re-encodes its subtree, so the work grows
// with the depth at which the bytes sit.
const maxResolvedBytes = 1 << 20

// consume charges compact bytes before they are copied into an expansion: the
// body of a root document or type at every expansion, and a $defs target at
// every visit.
func (r *resolver) consume(size int) error {
	if size > maxResolvedBytes-r.bytes {
		return field("/schema", "limit", fmt.Sprintf("expanded schemas exceed the cumulative byte limit of %d", maxResolvedBytes))
	}
	r.bytes += size
	return nil
}

// encodedSize is the size of compact JSON once encoded again: RawMessage
// encoding escapes HTML and line separators.
func encodedSize(raw []byte) int {
	return len(raw) + 5*(bytes.Count(raw, []byte("<"))+bytes.Count(raw, []byte(">"))+bytes.Count(raw, []byte("&"))) +
		3*(bytes.Count(raw, []byte("\u2028"))+bytes.Count(raw, []byte("\u2029")))
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
	if strings.HasPrefix(ref, "#/$defs/") {
		if err := r.consume(encodedSize(value)); err != nil {
			return nil, err
		}
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
	r.nodes++
	if depth > 32 || r.nodes > 10000 {
		return nil, field("/schema", "limit", "expanded schema exceeds depth or node limit")
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
		if obj["properties"], err = json.Marshal(children); err != nil {
			return nil, err
		}
	}
	for _, keyword := range []string{"items", "additionalProperties"} {
		value, exists := obj[keyword]
		if !exists || keyword == "additionalProperties" && string(bytes.TrimSpace(value)) == "false" {
			continue
		}
		child, err := r.expand(value, depth+1)
		if err != nil {
			return nil, err
		}
		obj[keyword] = child
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
		var err error
		if obj["oneOf"], err = json.Marshal(children); err != nil {
			return nil, err
		}
	}
	return json.Marshal(obj)
}
