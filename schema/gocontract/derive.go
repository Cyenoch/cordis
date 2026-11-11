package gocontract

import (
	"encoding/json"
	"fmt"
	"go/types"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/Cyenoch/cordis/schema"
)

// safeInteger is the largest integer a JSON number carries exactly.
const safeInteger = 1<<53 - 1

// emptyObject is the schema of an absent request or response.
var emptyObject = shape{"type": "object"}

// shape is a schema under construction.
type shape map[string]any

func (s shape) raw() json.RawMessage {
	data, _ := json.Marshal(s)
	return data
}

// derive returns the schema of the JSON form encoding/json gives t.
func (s *source) derive(t types.Type) (shape, error) {
	return s.schemaOf(t, nil)
}

func (s *source) schemaOf(t types.Type, visiting []*types.Named) (shape, error) {
	switch t := types.Unalias(t).(type) {
	case *types.Named:
		switch qualified(t) {
		case "time.Time":
			return shape{"type": "string"}, nil
		case "encoding/json.Number":
			// An empty Number encodes as 0: the value would change on the way.
			return nil, fmt.Errorf("json.Number cannot be described by a contract; use a numeric type")
		}
		if method := ownEncoding(t); method != "" {
			return nil, fmt.Errorf("%s has its own JSON encoding (%s), which a contract cannot describe", t, method)
		}
		if slices.Contains(visiting, t) {
			return nil, fmt.Errorf("%s is recursive, which a contract cannot describe", t)
		}
		return s.schemaOf(t.Underlying(), append(visiting, t))
	case *types.Pointer:
		inner, err := s.schemaOf(t.Elem(), visiting)
		if err != nil {
			return nil, err
		}
		inner["nullable"] = true
		return inner, nil
	case *types.Slice:
		// A nil slice encodes as null.
		if basic, ok := t.Elem().Underlying().(*types.Basic); ok && basic.Kind() == types.Uint8 {
			if method := keyEncoding(t.Elem()); method != "" {
				return nil, fmt.Errorf("%s has its own JSON encoding (%s), which a contract cannot describe", t.Elem(), method)
			}
			return shape{"type": "string", "contentEncoding": "base64", "nullable": true}, nil
		}
		items, err := s.schemaOf(t.Elem(), visiting)
		if err != nil {
			return nil, err
		}
		return shape{"type": "array", "items": items, "nullable": true}, nil
	case *types.Array:
		items, err := s.schemaOf(t.Elem(), visiting)
		if err != nil {
			return nil, err
		}
		return shape{"type": "array", "items": items, "minItems": t.Len(), "maxItems": t.Len()}, nil
	case *types.Struct:
		// An embedded type can give an unnamed struct an encoding of its own.
		if methods := types.NewMethodSet(types.NewPointer(t)); methods.Lookup(nil, "MarshalJSON") != nil || methods.Lookup(nil, "MarshalText") != nil {
			return nil, fmt.Errorf("%s has its own JSON encoding through an embedded field, which a contract cannot describe", t)
		}
		object := shape{"type": "object"}
		properties, required := map[string]any{}, []string{}
		if err := s.fields(t, visiting, properties, &required); err != nil {
			return nil, err
		}
		slices.Sort(required)
		object["properties"], object["required"] = properties, required
		return object, nil
	case *types.Basic:
		return s.basic(t)
	case *types.Map:
		// encoding/json also takes integer keys and keys that marshal as
		// text; only plain strings are what they look like on the wire.
		if key, ok := t.Key().Underlying().(*types.Basic); !ok || key.Kind() != types.String || keyEncoding(t.Key()) != "" {
			return nil, fmt.Errorf("map keys must be strings")
		}
		values, err := s.schemaOf(t.Elem(), visiting)
		if err != nil {
			return nil, err
		}
		// A nil map encodes as null.
		return shape{"type": "object", "additionalProperties": values, "nullable": true}, nil
	case *types.Interface:
		return nil, fmt.Errorf("interface types cannot be described by a contract")
	}
	return nil, fmt.Errorf("%s cannot be described by a contract", t)
}

func (s *source) basic(t *types.Basic) (shape, error) {
	integer := func(minimum, maximum int64) (shape, error) {
		return shape{"type": "integer", "minimum": minimum, "maximum": maximum}, nil
	}
	switch t.Kind() {
	case types.Bool:
		return shape{"type": "boolean"}, nil
	case types.String:
		return shape{"type": "string"}, nil
	case types.Float32, types.Float64:
		return shape{"type": "number"}, nil
	case types.Int8:
		return integer(-1<<7, 1<<7-1)
	case types.Int16:
		return integer(-1<<15, 1<<15-1)
	case types.Int32:
		return integer(-1<<31, 1<<31-1)
	case types.Int, types.Int64:
		return integer(-safeInteger, safeInteger)
	case types.Uint8:
		return integer(0, 1<<8-1)
	case types.Uint16:
		return integer(0, 1<<16-1)
	case types.Uint32:
		return integer(0, 1<<32-1)
	case types.Uint, types.Uint64, types.Uintptr:
		return integer(0, safeInteger)
	case types.Invalid:
		return nil, s.invalid()
	}
	return nil, fmt.Errorf("%s cannot be described by a contract", t)
}

// fields adds the JSON fields of a struct, promoting those of embedded
// structs as encoding/json does: a field of the struct itself hides a
// promoted one of the same name.
func (s *source) fields(t *types.Struct, visiting []*types.Named, properties map[string]any, required *[]string) error {
	type promotion struct {
		field *types.Var
		named *types.Named
		inner *types.Struct
	}
	var promoted []promotion
	for index := range t.NumFields() {
		field := t.Field(index)
		tag := reflect.StructTag(t.Tag(index))
		name, options, _ := strings.Cut(tag.Get("json"), ",")
		if tag.Get("json") == "-" {
			continue
		}
		fail := func(err error) error {
			return s.within(field.Pos(), "field "+field.Name(), err)
		}
		// What encoding/json makes of other characters depends on its version.
		if !validTagName(name) {
			return fail(fmt.Errorf("JSON name %q: use letters, digits, '_', '-', '.' and '$'", name))
		}
		if field.Embedded() && name == "" {
			embedded, ok := types.Unalias(field.Type()).(*types.Named)
			if _, pointer := types.Unalias(field.Type()).(*types.Pointer); pointer {
				return fail(fmt.Errorf("embedded pointers are not supported"))
			}
			if inner, isStruct := field.Type().Underlying().(*types.Struct); ok && isStruct && qualified(embedded) != "time.Time" && ownEncoding(embedded) == "" {
				if slices.Contains(visiting, embedded) {
					return fail(fmt.Errorf("%s is recursive", embedded))
				}
				promoted = append(promoted, promotion{field, embedded, inner})
				continue
			}
		}
		if !field.Exported() {
			continue
		}
		if name == "" {
			name = field.Name()
		}
		if _, exists := properties[name]; exists {
			return fail(fmt.Errorf("JSON name %q is used twice", name))
		}
		optional, quoted := false, false
		for option := range strings.SplitSeq(options, ",") {
			switch option {
			case "omitempty", "omitzero":
				optional = true
			case "string":
				quoted = true
			}
		}
		var property shape
		var err error
		if quoted {
			property, err = quotedInteger(field.Type())
		} else {
			property, err = s.schemaOf(field.Type(), visiting)
		}
		if err == nil {
			err = constrain(property, tag.Get("cordis"))
		}
		if err == nil && tag.Get("cordis") != "" {
			// Constraints can contradict each other or the type; say so here,
			// at the field, not when the whole contract is put together.
			err = schema.Check(property.raw())
		}
		if err != nil {
			return fail(err)
		}
		properties[name] = property
		if !optional {
			*required = append(*required, name)
		}
	}
	fromEmbedded := map[string]bool{}
	for _, p := range promoted {
		inner, innerRequired := map[string]any{}, []string{}
		if err := s.fields(p.inner, append(visiting, p.named), inner, &innerRequired); err != nil {
			return err
		}
		for name, property := range inner {
			if fromEmbedded[name] {
				// encoding/json drops both silently; say so instead.
				return s.errorf(p.field.Pos(), "field %s: JSON name %q is promoted from two embedded structs", p.field.Name(), name)
			}
			if _, hidden := properties[name]; hidden {
				continue
			}
			fromEmbedded[name] = true
			properties[name] = property
			if slices.Contains(innerRequired, name) {
				*required = append(*required, name)
			}
		}
	}
	return nil
}

// validTagName reports whether name is a JSON field name every version of
// encoding/json reads the same way.
func validTagName(name string) bool {
	for _, r := range name {
		if !strings.ContainsRune("_-.$", r) && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// quotedInteger is the schema of a 64-bit integer with the "string" option,
// the lossless way to carry one.
func quotedInteger(t types.Type) (shape, error) {
	if basic, ok := t.Underlying().(*types.Basic); ok {
		switch basic.Kind() {
		case types.Int64:
			return shape{"type": "string", "format": "int64"}, nil
		case types.Uint64:
			return shape{"type": "string", "format": "uint64"}, nil
		}
	}
	return nil, fmt.Errorf(`the "string" option is only supported on int64 and uint64`)
}

// constrain applies a cordis struct tag to the schema of its field.
func constrain(property shape, tag string) error {
	for tag != "" {
		var item string
		// A pattern may contain commas, so it takes the rest of the tag.
		if strings.HasPrefix(tag, "pattern=") {
			item, tag = tag, ""
		} else {
			item, tag, _ = strings.Cut(tag, ",")
		}
		key, value, found := strings.Cut(item, "=")
		if !found || value == "" {
			return fmt.Errorf("cordis tag: %q needs a value", item)
		}
		kind := property["type"]
		accepts := func(kinds ...string) error {
			if !slices.Contains(kinds, kind.(string)) {
				return fmt.Errorf("cordis tag: %s does not apply to a %s", key, kind)
			}
			return nil
		}
		switch key {
		case "minLength", "maxLength", "minItems", "maxItems":
			applies := "array"
			if strings.HasSuffix(key, "Length") {
				applies = "string"
			}
			if err := accepts(applies); err != nil {
				return err
			}
			count, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("cordis tag: %s: %w", key, err)
			}
			property[key] = count
		case "minimum", "maximum":
			if err := accepts("integer", "number"); err != nil {
				return err
			}
			if !validNumber(value) {
				return fmt.Errorf("cordis tag: %s: %q is not a number", key, value)
			}
			property[key] = json.Number(value)
		case "pattern":
			if err := accepts("string"); err != nil {
				return err
			}
			property[key] = value
		case "enum":
			if err := accepts("string", "integer", "number"); err != nil {
				return err
			}
			values := []any{}
			for option := range strings.SplitSeq(value, "|") {
				if kind == "string" {
					values = append(values, option)
				} else if validNumber(option) {
					values = append(values, json.Number(option))
				} else {
					return fmt.Errorf("cordis tag: enum: %q is not a number", option)
				}
			}
			// A nil pointer encodes as null, which the enum has to allow.
			if property["nullable"] == true {
				values = append(values, nil)
			}
			property[key] = values
		default:
			return fmt.Errorf("cordis tag: unknown constraint %q", key)
		}
	}
	return nil
}

// keyEncoding names the method that changes how a map key type is written.
func keyEncoding(t types.Type) string {
	if named, ok := types.Unalias(t).(*types.Named); ok {
		return ownEncoding(named)
	}
	return ""
}

func validNumber(text string) bool {
	return text != "" && (text[0] == '-' || text[0] >= '0' && text[0] <= '9') && json.Valid([]byte(text))
}

func qualified(t *types.Named) string {
	if t.Obj().Pkg() == nil {
		return t.Obj().Name()
	}
	return t.Obj().Pkg().Path() + "." + t.Obj().Name()
}

// ownEncoding names the method that gives t a JSON form its Go shape does
// not show.
func ownEncoding(t *types.Named) string {
	methods := types.NewMethodSet(types.NewPointer(t))
	for _, name := range []string{"MarshalJSON", "UnmarshalJSON", "MarshalText", "UnmarshalText"} {
		if methods.Lookup(nil, name) != nil {
			return name
		}
	}
	return ""
}
