package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Decode rejects duplicate keys, unknown fields, deep values and trailing data.
func Decode(data []byte, target any) error {
	return DecodeWithLimits(data, target, 32, 10000)
}

// Encode applies the same lossless value checks as Decode before returning JSON.
func Encode(value any) ([]byte, error) {
	if err := CheckDataType(reflect.TypeOf(value)); err != nil {
		return nil, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode contract: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := checkValue(dec, reflect.TypeOf(value), 0, 32, 10000, true); err != nil {
		return nil, err
	}
	return data, nil
}

// DecodeData validates author data; protocol envelopes use Decode because their
// optional collections can explicitly permit null without a data codec.
func DecodeData(data []byte, target any) error {
	t := reflect.TypeOf(target)
	if t == nil || t.Kind() != reflect.Pointer || reflect.ValueOf(target).IsNil() {
		return Fail(Invalid, "decode target must be a non-nil pointer")
	}
	if err := CheckDataType(t.Elem()); err != nil {
		return err
	}
	return decode(data, target, 32, 10000, true)
}

// DecodeWithLimits retains exact fields and duplicate-key checks while allowing
// persisted host envelopes to account for their additional structural depth.
func DecodeWithLimits(data []byte, target any, maxDepth, maxArray int) error {
	return decode(data, target, maxDepth, maxArray, false)
}

func decode(data []byte, target any, maxDepth, maxArray int, strictContainers bool) error {
	if !utf8.Valid(data) {
		return Fail(Invalid, "invalid UTF-8 JSON")
	}
	decCheck := json.NewDecoder(bytes.NewReader(data))
	decCheck.UseNumber()
	targetType := reflect.TypeOf(target)
	if targetType == nil || targetType.Kind() != reflect.Pointer || reflect.ValueOf(target).IsNil() {
		return Fail(Invalid, "decode target must be a non-nil pointer")
	}
	if err := checkValue(decCheck, targetType.Elem(), 0, maxDepth, maxArray, strictContainers); err != nil {
		return err
	}
	if _, err := decCheck.Token(); err != io.EOF {
		return Fail(Invalid, "trailing JSON data")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("decode contract: %w", err)
	}
	return nil
}

func CheckJSON(data []byte, maxDepth, maxArray int) error {
	if !utf8.Valid(data) {
		return Fail(Invalid, "invalid UTF-8 JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := checkValue(dec, nil, 0, maxDepth, maxArray, false); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Fail(Invalid, "trailing JSON data")
	}
	return nil
}

func checkValue(dec *json.Decoder, t reflect.Type, depth, maxDepth, maxArray int, strictContainers bool) error {
	if depth > maxDepth {
		return Fail(Invalid, "JSON nesting limit exceeded")
	}
	allowNull := false
	if t != nil {
		allowNull = t.Kind() == reflect.Pointer
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t == rawType || reflect.PointerTo(t).Implements(unmarshalerType) {
			t = nil
		}
	}
	token, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read JSON token: %w", err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		if t != nil && token == nil && !allowNull && (strictContainers || t.Kind() != reflect.Slice && t.Kind() != reflect.Map && t.Kind() != reflect.Interface) {
			return Fail(Invalid, "null requires an explicit nullable codec")
		}
		if number, ok := token.(json.Number); ok && t != nil {
			switch t.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
				value, err := number.Float64()
				if err != nil {
					return Fail(Invalid, "number must be finite")
				}
				if math.Abs(value) >= 9007199254740990 {
					exact, ok := new(big.Rat).SetString(string(number))
					if !ok || exact.IsInt() && new(big.Int).Abs(exact.Num()).Cmp(big.NewInt(9007199254740991)) > 0 {
						return Fail(Invalid, "integer exceeds JSON safe range; use a decimal-string codec")
					}
				}
			}
		}
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return fmt.Errorf("read object key: %w", err)
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return Fail(Invalid, "duplicate or invalid JSON key")
			}
			seen[name] = true
			var child reflect.Type
			if t != nil && t.Kind() == reflect.Struct {
				child = jsonFields(t)[name]
				if child == nil {
					return fmt.Errorf("unknown contract field %q", name)
				}
			} else if t != nil && t.Kind() == reflect.Map {
				child = t.Elem()
			}
			if err := checkValue(dec, child, depth+1, maxDepth, maxArray, strictContainers); err != nil {
				return err
			}
		}
	case '[':
		count := 0
		var child reflect.Type
		if t != nil && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) {
			child = t.Elem()
		}
		for dec.More() {
			count++
			if count > maxArray {
				return Fail(Invalid, "JSON array limit exceeded")
			}
			if err := checkValue(dec, child, depth+1, maxDepth, maxArray, strictContainers); err != nil {
				return err
			}
		}
		if t != nil && t.Kind() == reflect.Array && count != t.Len() {
			return Fail(Invalid, "array length differs from contract")
		}
	default:
		return Fail(Invalid, "unexpected JSON delimiter")
	}
	_, err = dec.Token()
	if err != nil {
		return fmt.Errorf("close JSON value: %w", err)
	}
	return nil
}

var rawType = reflect.TypeFor[json.RawMessage]()
var unmarshalerType = reflect.TypeFor[json.Unmarshaler]()

var fieldCache sync.Map

func validJSONFieldName(name string) bool {
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", r) {
			return false
		}
	}
	return true
}

// JSON promotes anonymous fields, with shallower fields and explicit tags taking
// precedence. Ambiguous fields at the same depth are absent, not guessed.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	if cached, ok := fieldCache.Load(t); ok {
		return cached.(map[string]reflect.Type)
	}
	type candidate struct {
		typeOf reflect.Type
		depth  int
		tagged bool
		count  int
	}
	fields := map[string]candidate{}
	var visit func(reflect.Type, int, map[reflect.Type]bool)
	visit = func(current reflect.Type, depth int, path map[reflect.Type]bool) {
		if path[current] {
			return
		}
		path[current] = true
		defer delete(path, current)
		for n := range current.NumField() {
			field := current.Field(n)
			tag := field.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			if !validJSONFieldName(name) {
				name = ""
			}
			inner := field.Type
			if inner.Kind() == reflect.Pointer {
				inner = inner.Elem()
			}
			if field.Anonymous && name == "" && inner.Kind() == reflect.Struct {
				visit(inner, depth+1, path)
				continue
			}
			if !field.IsExported() {
				continue
			}
			tagged := name != ""
			if !tagged {
				name = field.Name
			}
			previous, exists := fields[name]
			if !exists || depth < previous.depth || depth == previous.depth && tagged && !previous.tagged {
				fields[name] = candidate{field.Type, depth, tagged, 1}
			} else if depth == previous.depth && tagged == previous.tagged {
				previous.count++
				fields[name] = previous
			}
		}
	}
	visit(t, 0, map[reflect.Type]bool{})
	result := map[string]reflect.Type{}
	for name, candidate := range fields {
		if candidate.count == 1 {
			result[name] = candidate.typeOf
		}
	}
	actual, _ := fieldCache.LoadOrStore(t, result)
	return actual.(map[string]reflect.Type)
}
