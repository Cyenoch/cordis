package contract_test

import (
	"reflect"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/schema"
)

func TestRemoteDataRejectsProcessLocalAndAmbiguousValues(t *testing.T) {
	types := []reflect.Type{reflect.TypeFor[any](), reflect.TypeFor[*int](), reflect.TypeFor[chan int](), reflect.TypeFor[func()](), reflect.TypeFor[int64](), reflect.TypeFor[struct{ Connection chan int }](), reflect.TypeFor[map[int]string]()}
	for _, typ := range types {
		if err := contract.CheckDataType(typ); err == nil {
			t.Fatalf("nonportable type accepted: %s", typ)
		}
	}
	type payload struct {
		Count schema.Int64            `json:"count"`
		Name  string                  `json:"name"`
		Note  schema.Nullable[string] `json:"note"`
	}
	if err := contract.CheckDataType(reflect.TypeFor[payload]()); err != nil {
		t.Fatal(err)
	}
}
