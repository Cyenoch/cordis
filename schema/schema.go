// Package schema implements the deliberately restricted Cordis JSON data contract.
// Unknown keywords and ambiguous unions are errors, not ignored annotations.
package schema

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Cyenoch/cordis/contract"
)

// FieldError identifies an invalid instance or schema field using a JSON Pointer.
// The empty pointer denotes the document root. Schema errors start with /schema.
type FieldError struct {
	Path    string
	Keyword string
	Message string
}

func (e *FieldError) Error() string {
	path := e.Path
	if path == "" {
		path = "/"
	}
	return fmt.Sprintf("%s: %s: %s", path, e.Keyword, e.Message)
}

func field(path, keyword, message string) error {
	return &FieldError{Path: path, Keyword: keyword, Message: message}
}

func pointer(path, key string) string {
	return path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

// Schema is an immutable compiled data schema and is safe for concurrent validation.
// Construct it with Compile; a zero Schema rejects every value.
type Schema struct{ root *node }

type node struct {
	kind          string
	nullable      bool
	properties    map[string]*node
	required      map[string]bool
	requiredNames []string
	items         *node
	minimum       *big.Rat
	maximum       *big.Rat
	minLength     *int
	maxLength     *int
	minItems      *int
	maxItems      *int
	pattern       *regexp.Regexp
	format        string
	enum          []json.RawMessage
	constant      json.RawMessage
	discriminator string
	variants      map[string]*node
	raw           json.RawMessage
}

// Compile validates the schema itself before preparing a reusable validator.
func Compile(data json.RawMessage) (*Schema, error) {
	if !utf8.Valid(data) {
		return nil, field("/schema", "json", "invalid UTF-8")
	}
	if err := contract.CheckJSON(data, 32, 10000); err != nil {
		return nil, field("/schema", "json", err.Error())
	}
	data, err := resolveSchema(data, nil)
	if err != nil {
		return nil, err
	}
	root, err := compile(data, "/schema", 0)
	if err != nil {
		return nil, err
	}
	if err := normalizeNode(root); err != nil {
		return nil, err
	}
	return &Schema{root: root}, nil
}

// Check rejects invalid schemas without validating an instance.
func Check(data json.RawMessage) error {
	_, err := Compile(data)
	return err
}

// Validate checks both the schema and an instance. Use Compile for repeated calls.
func Validate(schema json.RawMessage, value json.RawMessage) error {
	compiled, err := Compile(schema)
	if err != nil {
		return err
	}
	return compiled.Validate(value)
}

// Validate rejects duplicate keys, trailing JSON, unknown properties, and values
// outside the declared data contract. It does not insert defaults or coerce types.
func (s *Schema) Validate(value json.RawMessage) error {
	if s == nil || s.root == nil {
		return field("/schema", "schema", "schema is not compiled")
	}
	if !utf8.Valid(value) {
		return field("", "json", "invalid UTF-8")
	}
	if err := contract.CheckJSON(value, 32, 10000); err != nil {
		return field("", "json", err.Error())
	}
	return s.root.validate(value, "")
}

func compile(raw json.RawMessage, path string, depth int) (*node, error) {
	if depth > 32 {
		return nil, field(path, "depth", "schema nesting exceeds 32")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, field(path, "schema", "expected a schema object")
	}
	n := &node{raw: append(json.RawMessage(nil), raw...)}
	allowed := map[string]bool{"type": true, "nullable": true, "title": true, "description": true, "enum": true, "const": true}
	for _, name := range []string{"title", "description"} {
		if v, ok := obj[name]; ok {
			var text string
			if err := json.Unmarshal(v, &text); err != nil || isNull(v) {
				return nil, field(pointer(path, name), name, "expected string")
			}
		}
	}
	if v, ok := obj["nullable"]; ok {
		if err := json.Unmarshal(v, &n.nullable); err != nil || isNull(v) {
			return nil, field(pointer(path, "nullable"), "nullable", "expected boolean")
		}
	}
	if v, ok := obj["oneOf"]; ok {
		allowed["oneOf"], allowed["discriminator"] = true, true
		if _, present := obj["type"]; present {
			return nil, field(path, "oneOf", "tagged unions must omit type")
		}
		n.kind = "union"
		var disc struct {
			PropertyName string `json:"propertyName"`
		}
		if err := contract.Decode(obj["discriminator"], &disc); err != nil || disc.PropertyName == "" {
			return nil, field(pointer(path, "discriminator"), "discriminator", "expected {propertyName: nonempty string}")
		}
		n.discriminator = disc.PropertyName
		var variants []json.RawMessage
		if err := json.Unmarshal(v, &variants); err != nil || len(variants) < 2 {
			return nil, field(pointer(path, "oneOf"), "oneOf", "expected at least two object variants")
		}
		n.variants = make(map[string]*node, len(variants))
		for i, variant := range variants {
			vp := pointer(pointer(path, "oneOf"), strconv.Itoa(i))
			child, err := compile(variant, vp, depth+1)
			if err != nil {
				return nil, err
			}
			if child.kind != "object" || child.nullable || !child.required[n.discriminator] {
				return nil, field(vp, "discriminator", "variant must be a nonnullable object requiring the tag")
			}
			tag := child.properties[n.discriminator]
			if tag == nil || tag.kind != "string" || tag.nullable || tag.constant == nil {
				return nil, field(vp, "discriminator", "tag must be a nonnullable string with const")
			}
			var name string
			if err := json.Unmarshal(tag.constant, &name); err != nil || name == "" {
				return nil, field(vp, "discriminator", "tag const must be a nonempty string")
			}
			if _, exists := n.variants[name]; exists {
				return nil, field(vp, "discriminator", "duplicate tag value")
			}
			n.variants[name] = child
		}
	} else {
		if err := json.Unmarshal(obj["type"], &n.kind); err != nil || n.kind == "" {
			return nil, field(pointer(path, "type"), "type", "expected a supported type string")
		}
		switch n.kind {
		case "object":
			allowed["properties"], allowed["required"], allowed["additionalProperties"] = true, true, true
			n.properties, n.required = map[string]*node{}, map[string]bool{}
			if v, ok := obj["additionalProperties"]; ok && string(bytes.TrimSpace(v)) != "false" {
				return nil, field(pointer(path, "additionalProperties"), "additionalProperties", "only false is supported; objects are closed")
			}
			if v, ok := obj["properties"]; ok {
				var props map[string]json.RawMessage
				if err := json.Unmarshal(v, &props); err != nil || props == nil {
					return nil, field(pointer(path, "properties"), "properties", "expected object")
				}
				for _, name := range sortedKeys(props) {
					child, err := compile(props[name], pointer(pointer(path, "properties"), name), depth+1)
					if err != nil {
						return nil, err
					}
					n.properties[name] = child
				}
			}
			if v, ok := obj["required"]; ok {
				var required []string
				if err := json.Unmarshal(v, &required); err != nil || isNull(v) {
					return nil, field(pointer(path, "required"), "required", "expected string array")
				}
				for _, name := range required {
					if n.required[name] || n.properties[name] == nil {
						return nil, field(pointer(path, "required"), "required", "duplicate or undeclared required property")
					}
					n.required[name] = true
				}
			}
		case "array":
			allowed["items"], allowed["minItems"], allowed["maxItems"] = true, true, true
			child, err := compile(obj["items"], pointer(path, "items"), depth+1)
			if err != nil {
				return nil, err
			}
			n.items = child
			n.minItems, err = lengthKeyword(obj, "minItems", path)
			if err != nil {
				return nil, err
			}
			n.maxItems, err = lengthKeyword(obj, "maxItems", path)
			if err != nil {
				return nil, err
			}
			if n.minItems != nil && n.maxItems != nil && *n.minItems > *n.maxItems {
				return nil, field(path, "minItems", "minimum exceeds maximum")
			}
		case "string":
			allowed["minLength"], allowed["maxLength"], allowed["pattern"], allowed["format"], allowed["contentEncoding"] = true, true, true, true, true
			var err error
			n.minLength, err = lengthKeyword(obj, "minLength", path)
			if err != nil {
				return nil, err
			}
			n.maxLength, err = lengthKeyword(obj, "maxLength", path)
			if err != nil {
				return nil, err
			}
			if n.minLength != nil && n.maxLength != nil && *n.minLength > *n.maxLength {
				return nil, field(path, "minLength", "minimum exceeds maximum")
			}
			if v, ok := obj["pattern"]; ok {
				var pattern string
				if err := json.Unmarshal(v, &pattern); err != nil || isNull(v) {
					return nil, field(pointer(path, "pattern"), "pattern", "expected string")
				}
				n.pattern, err = regexp.Compile(pattern)
				if err != nil {
					return nil, field(pointer(path, "pattern"), "pattern", err.Error())
				}
			}
			if v, ok := obj["format"]; ok {
				if err := json.Unmarshal(v, &n.format); err != nil || (n.format != "int64" && n.format != "uint64" && n.format != "byte") {
					return nil, field(pointer(path, "format"), "format", "supported formats are int64, uint64, byte")
				}
			}
			if v, ok := obj["contentEncoding"]; ok {
				var encoding string
				if err := json.Unmarshal(v, &encoding); err != nil || encoding != "base64" || (n.format != "" && n.format != "byte") {
					return nil, field(pointer(path, "contentEncoding"), "contentEncoding", "only base64 is supported and cannot accompany integer formats")
				}
				n.format = "byte"
			}
		case "number", "integer":
			allowed["minimum"], allowed["maximum"] = true, true
			var err error
			n.minimum, err = numberKeyword(obj, "minimum", path)
			if err != nil {
				return nil, err
			}
			n.maximum, err = numberKeyword(obj, "maximum", path)
			if err != nil {
				return nil, err
			}
			if n.minimum != nil && n.maximum != nil && n.minimum.Cmp(n.maximum) > 0 {
				return nil, field(path, "minimum", "minimum exceeds maximum")
			}
		case "boolean":
		default:
			return nil, field(pointer(path, "type"), "type", "unsupported type; use explicit nullable for null")
		}
	}
	for _, name := range sortedKeys(obj) {
		if !allowed[name] {
			return nil, field(pointer(path, name), "keyword", "unsupported keyword")
		}
	}
	if v, ok := obj["enum"]; ok {
		if err := json.Unmarshal(v, &n.enum); err != nil || len(n.enum) == 0 {
			return nil, field(pointer(path, "enum"), "enum", "expected nonempty array")
		}
	}
	if v, ok := obj["const"]; ok {
		n.constant = append(json.RawMessage(nil), v...)
	}
	n.requiredNames = sortedKeys(n.required)
	// Validate literals against all structural constraints without applying enum/const
	// recursively to themselves. This catches unusable schemas at registration time.
	literals := append([]json.RawMessage(nil), n.enum...)
	if n.constant != nil {
		literals = append(literals, n.constant)
	}
	plain := *n
	plain.enum, plain.constant = nil, nil
	for i, literal := range literals {
		if err := plain.validate(literal, path); err != nil {
			return nil, field(path, "literal", err.Error())
		}
		if i < len(n.enum) {
			for j := range i {
				if equalJSON(n.enum[j], literal) {
					return nil, field(path, "enum", "duplicate enum value")
				}
			}
		}
	}
	if n.constant != nil && len(n.enum) > 0 {
		found := false
		for _, literal := range n.enum {
			if equalJSON(literal, n.constant) {
				found = true
			}
		}
		if !found {
			return nil, field(path, "const", "const is not in enum")
		}
	}
	return n, nil
}

func lengthKeyword(obj map[string]json.RawMessage, key, path string) (*int, error) {
	raw, ok := obj[key]
	if !ok {
		return nil, nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil || isNull(raw) || value < 0 {
		return nil, field(pointer(path, key), key, "expected nonnegative integer")
	}
	return &value, nil
}

func numberKeyword(obj map[string]json.RawMessage, key, path string) (*big.Rat, error) {
	raw, ok := obj[key]
	if !ok {
		return nil, nil
	}
	value, err := numeric(raw)
	if err != nil {
		return nil, field(pointer(path, key), key, err.Error())
	}
	return value, nil
}

func numeric(raw json.RawMessage) (*big.Rat, error) {
	data := bytes.TrimSpace(raw)
	if len(data) == 0 || (data[0] != '-' && (data[0] < '0' || data[0] > '9')) {
		return nil, fmt.Errorf("expected number")
	}
	mantissa := string(data)
	if index := strings.IndexAny(mantissa, "eE"); index >= 0 {
		mantissa = mantissa[:index]
	}
	nonzero := false
	for _, digit := range mantissa {
		if digit >= '1' && digit <= '9' {
			nonzero = true
			break
		}
	}
	if !nonzero {
		return new(big.Rat), nil
	}
	f, err := strconv.ParseFloat(string(data), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, fmt.Errorf("expected finite float64 number")
	}
	if f == 0 {
		return nil, fmt.Errorf("number underflows float64 precision")
	}
	value, ok := new(big.Rat).SetString(string(data))
	if !ok {
		return nil, fmt.Errorf("invalid number")
	}
	if value.IsInt() && new(big.Int).Abs(value.Num()).Cmp(big.NewInt(9007199254740991)) > 0 {
		return nil, fmt.Errorf("integer precision exceeds JSON safe range; use string format int64 or uint64")
	}
	return value, nil
}

func (n *node) validate(raw json.RawMessage, path string) error {
	if isNull(raw) {
		if !n.nullable {
			return field(path, "nullable", "null is not allowed")
		}
	} else {
		switch n.kind {
		case "object":
			var value map[string]json.RawMessage
			if err := json.Unmarshal(raw, &value); err != nil || value == nil {
				return field(path, "type", "expected object")
			}
			for _, name := range n.requiredNames {
				if _, ok := value[name]; !ok {
					return field(pointer(path, name), "required", "missing required property")
				}
			}
			for _, name := range sortedKeys(value) {
				child, ok := n.properties[name]
				if !ok {
					return field(pointer(path, name), "additionalProperties", "unknown property")
				}
				if err := child.validate(value[name], pointer(path, name)); err != nil {
					return err
				}
			}
		case "array":
			var value []json.RawMessage
			if err := json.Unmarshal(raw, &value); err != nil || value == nil {
				return field(path, "type", "expected array")
			}
			if n.minItems != nil && len(value) < *n.minItems {
				return field(path, "minItems", "too few items")
			}
			if n.maxItems != nil && len(value) > *n.maxItems {
				return field(path, "maxItems", "too many items")
			}
			for i, item := range value {
				if err := n.items.validate(item, pointer(path, strconv.Itoa(i))); err != nil {
					return err
				}
			}
		case "string":
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return field(path, "type", "expected string")
			}
			if n.minLength != nil && utf8.RuneCountInString(value) < *n.minLength {
				return field(path, "minLength", "string is too short")
			}
			if n.maxLength != nil && utf8.RuneCountInString(value) > *n.maxLength {
				return field(path, "maxLength", "string is too long")
			}
			if n.pattern != nil && !n.pattern.MatchString(value) {
				return field(path, "pattern", "string does not match pattern")
			}
			switch n.format {
			case "byte":
				decoded, err := base64.StdEncoding.Strict().DecodeString(value)
				if err != nil || base64.StdEncoding.EncodeToString(decoded) != value {
					return field(path, "contentEncoding", "expected canonical padded base64")
				}
			case "int64":
				parsed, err := strconv.ParseInt(value, 10, 64)
				if err != nil || strconv.FormatInt(parsed, 10) != value {
					return field(path, "format", "expected canonical signed 64-bit decimal string")
				}
			case "uint64":
				parsed, err := strconv.ParseUint(value, 10, 64)
				if err != nil || strconv.FormatUint(parsed, 10) != value {
					return field(path, "format", "expected canonical unsigned 64-bit decimal string")
				}
			}
		case "boolean":
			var value bool
			if err := json.Unmarshal(raw, &value); err != nil {
				return field(path, "type", "expected boolean")
			}
		case "number", "integer":
			value, err := numeric(raw)
			if err != nil {
				return field(path, "precision", err.Error())
			}
			if n.kind == "integer" && !value.IsInt() {
				return field(path, "type", "expected integer")
			}
			if n.minimum != nil && value.Cmp(n.minimum) < 0 {
				return field(path, "minimum", "number is below minimum")
			}
			if n.maximum != nil && value.Cmp(n.maximum) > 0 {
				return field(path, "maximum", "number exceeds maximum")
			}
		case "union":
			var value map[string]json.RawMessage
			if err := json.Unmarshal(raw, &value); err != nil || value == nil {
				return field(path, "type", "expected tagged object")
			}
			var tag string
			if err := json.Unmarshal(value[n.discriminator], &tag); err != nil || isNull(value[n.discriminator]) {
				return field(pointer(path, n.discriminator), "discriminator", "missing or non-string tag")
			}
			variant := n.variants[tag]
			if variant == nil {
				return field(pointer(path, n.discriminator), "discriminator", "unknown tag")
			}
			if err := variant.validate(raw, path); err != nil {
				return err
			}
		default:
			return field(path, "type", "unsupported schema")
		}
	}
	if n.constant != nil && !equalJSON(raw, n.constant) {
		return field(path, "const", "value differs from const")
	}
	if len(n.enum) > 0 {
		for _, allowed := range n.enum {
			if equalJSON(raw, allowed) {
				return nil
			}
		}
		return field(path, "enum", "value is not in enum")
	}
	return nil
}

func isNull(raw []byte) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func equalJSON(a, b json.RawMessage) bool {
	var left, right any
	da, db := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	da.UseNumber()
	db.UseNumber()
	if da.Decode(&left) != nil || db.Decode(&right) != nil {
		return false
	}
	return equalValue(left, right)
}

func equalValue(a, b any) bool {
	switch left := a.(type) {
	case json.Number:
		right, ok := b.(json.Number)
		if !ok {
			return false
		}
		lr, lerr := numeric(json.RawMessage(left))
		rr, rerr := numeric(json.RawMessage(right))
		return lerr == nil && rerr == nil && lr.Cmp(rr) == 0
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for key, value := range left {
			other, exists := right[key]
			if !exists || !equalValue(value, other) {
				return false
			}
		}
		return true
	case []any:
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for i, value := range left {
			if !equalValue(value, right[i]) {
				return false
			}
		}
		return true
	case string:
		right, ok := b.(string)
		return ok && left == right
	case bool:
		right, ok := b.(bool)
		return ok && left == right
	case nil:
		return b == nil
	default:
		return false
	}
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
