package contract

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// CheckDataType rejects process-local state and ambiguous raw 64-bit numbers at
// registration. Generated contracts use schema validation and explicit codecs.
func CheckDataType(t reflect.Type) error { return checkDataType(t, map[reflect.Type]bool{}) }

// DataType describes the payload of a framework codec such as Nullable. Custom
// codecs remain responsible for their own wire representation.
type DataType interface{ ContractDataType() reflect.Type }

func checkDataType(t reflect.Type, visiting map[reflect.Type]bool) error {
	if t == nil {
		return Fail(Invalid, "untyped service data")
	}
	if t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface || t.Kind() == reflect.Func || t.Kind() == reflect.Chan || t.Kind() == reflect.UnsafePointer {
		return fmt.Errorf("nonserializable service type %s", t)
	}
	if visiting[t] {
		return fmt.Errorf("recursive service type %s", t)
	}
	visiting[t] = true
	defer delete(visiting, t)
	if t.Implements(reflect.TypeFor[DataType]()) {
		return checkDataType(reflect.Zero(t).Interface().(DataType).ContractDataType(), visiting)
	}
	if t.Implements(reflect.TypeFor[json.Marshaler]()) && reflect.PointerTo(t).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return nil
	}
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
		for n := range t.NumField() {
			field := t.Field(n)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if field.IsExported() && !validJSONFieldName(name) {
				return fmt.Errorf("service field %s: invalid JSON tag", field.Name)
			}
		}
		for name, fieldType := range jsonFields(t) {
			if err := checkDataType(fieldType, visiting); err != nil {
				return fmt.Errorf("service field %s: %w", name, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported service data type %s", t)
	}
}
