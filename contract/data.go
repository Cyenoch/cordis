package contract

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// CheckDataType rejects process-local state and ambiguous raw 64-bit numbers at
// registration. Generated contracts use schema validation and explicit codecs.
func CheckDataType(t reflect.Type) error { return checkDataType(t, map[reflect.Type]bool{}) }

func checkDataType(t reflect.Type, visiting map[reflect.Type]bool) error {
	if t == nil {
		return Fail(Invalid, "untyped service data")
	}
	if t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface || t.Kind() == reflect.Func || t.Kind() == reflect.Chan || t.Kind() == reflect.UnsafePointer {
		return fmt.Errorf("nonserializable service type %s", t)
	}
	if t.Implements(reflect.TypeFor[json.Marshaler]()) && reflect.PointerTo(t).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return nil
	}
	if visiting[t] {
		return fmt.Errorf("recursive service type %s", t)
	}
	visiting[t] = true
	defer delete(visiting, t)
	switch t.Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Float32, reflect.Float64:
		return nil
	case reflect.Int64, reflect.Uint64:
		return fmt.Errorf("service type %s requires an explicit decimal-string codec", t)
	case reflect.Array, reflect.Slice:
		return checkDataType(t.Elem(), visiting)
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return fmt.Errorf("service map keys must be strings: %s", t)
		}
		return checkDataType(t.Elem(), visiting)
	case reflect.Struct:
		for i := range t.NumField() {
			field := t.Field(i)
			if !field.IsExported() || field.Tag.Get("json") == "-" {
				continue
			}
			if err := checkDataType(field.Type, visiting); err != nil {
				return fmt.Errorf("service field %s: %w", field.Name, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported service data type %s", t)
	}
}
