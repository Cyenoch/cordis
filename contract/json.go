package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// Decode rejects duplicate keys, unknown fields, deep values and trailing data.
func Decode(data []byte, target any) error {
	return DecodeWithLimits(data, target, 32, 10000)
}

// DecodeWithLimits retains exact fields and duplicate-key checks while allowing
// persisted host envelopes to account for their additional structural depth.
func DecodeWithLimits(data []byte, target any, maxDepth, maxArray int) error {
	if !utf8.Valid(data) {
		return Fail(Invalid, "invalid UTF-8 JSON")
	}
	decCheck := json.NewDecoder(bytes.NewReader(data))
	decCheck.UseNumber()
	if err := checkValue(decCheck, reflect.TypeOf(target), 0, maxDepth, maxArray); err != nil {
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
	if err := checkValue(dec, nil, 0, maxDepth, maxArray); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Fail(Invalid, "trailing JSON data")
	}
	return nil
}

func checkValue(dec *json.Decoder, t reflect.Type, depth, maxDepth, maxArray int) error {
	if depth > maxDepth {
		return Fail(Invalid, "JSON nesting limit exceeded")
	}
	if t != nil {
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
				for i := range t.NumField() {
					field := t.Field(i)
					if !field.IsExported() {
						continue
					}
					fieldName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
					if fieldName == "" {
						fieldName = field.Name
					}
					if fieldName == name {
						child = field.Type
						break
					}
				}
				if child == nil {
					return fmt.Errorf("unknown contract field %q", name)
				}
			} else if t != nil && t.Kind() == reflect.Map {
				child = t.Elem()
			}
			if err := checkValue(dec, child, depth+1, maxDepth, maxArray); err != nil {
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
			if err := checkValue(dec, child, depth+1, maxDepth, maxArray); err != nil {
				return err
			}
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
