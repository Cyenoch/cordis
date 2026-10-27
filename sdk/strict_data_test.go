package sdk_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/schema"
	"github.com/Cyenoch/cordis/sdk"
)

func TestStrictDataTypedNullContainers(t *testing.T) {
	for _, tc := range []struct {
		name     string
		register func(*typedBoundaryScope) error
	}{
		{"slice", func(s *typedBoundaryScope) error {
			return sdk.Provide(s, contract.Service{}, func(_ context.Context, x []int) ([]int, error) { return x, nil })
		}},
		{"map", func(s *typedBoundaryScope) error {
			return sdk.Provide(s, contract.Service{}, func(_ context.Context, x map[string]int) (map[string]int, error) { return x, nil })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &typedBoundaryScope{}
			if err := tc.register(s); err != nil {
				t.Fatal(err)
			}
			out, err := s.handler(t.Context(), json.RawMessage(`null`))
			if err == nil {
				t.Errorf("non-nullable %s reached handler and returned %s", tc.name, out)
			}
		})
	}
}
func TestStrictDataNullableContainerRoundTrip(t *testing.T) {
	if raw, err := json.Marshal(schema.NonNull([]int(nil))); err == nil {
		t.Fatalf("non-null value silently became %s", raw)
	}
	before := schema.NonNull([]int{})
	raw, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var after schema.Nullable[[]int]
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	if !after.Valid || after.Value == nil {
		t.Fatal("explicit empty array lost its non-null state")
	}

}
func TestStrictDataTypedFloatUnsafeInteger(t *testing.T) {
	s := &typedBoundaryScope{}
	var received float64
	if err := sdk.Provide(s, contract.Service{}, func(_ context.Context, x float64) (float64, error) { received = x; return x, nil }); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`9007199254740993`)
	out, err := s.handler(t.Context(), raw)
	if err == nil {
		t.Errorf("unsafe integer reached handler as %.0f; output=%s", received, out)
	}
}

type reviewHidden struct {
	Payload any `json:"payload"`
}
type reviewEmbedded struct{ reviewHidden }

func TestStrictDataUnexportedEmbeddedTypeCheck(t *testing.T) {
	s := &typedBoundaryScope{}
	err := sdk.Provide(s, contract.Service{}, func(_ context.Context, x reviewEmbedded) (reviewEmbedded, error) { return x, nil })
	if err == nil {
		out, callErr := s.handler(t.Context(), json.RawMessage(`{"payload":9007199254740993}`))
		t.Errorf("registered promoted arbitrary interface (direct check=%v); out=%s err=%v", contract.CheckDataType(reflect.TypeFor[any]()), out, callErr)
	}
}

type ReviewDiscarded struct {
	Unsupported chan int `json:"value"`
}
type reviewShadowed struct {
	ReviewDiscarded
	Value int `json:"value"`
}

func TestStrictDataShadowedTypeCheck(t *testing.T) {
	in := reviewShadowed{Value: 3}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.CheckDataType(reflect.TypeFor[reviewShadowed]()); err != nil {
		t.Errorf("valid effective fields rejected: wire=%s err=%v", raw, err)
	}
}

func TestStrictDataExactJSONTags(t *testing.T) {
	for _, v := range []any{struct {
		Value int `json:"-,omitempty"`
	}{3}} {
		raw, _ := json.Marshal(v)
		_, err := contract.Encode(v)
		if err != nil {
			t.Errorf("valid JSON=%s rejected by Encode: %v", raw, err)
		}
	}
}

func TestStrictDataNullableUnknownAndArrayControls(t *testing.T) {
	var x schema.Nullable[struct {
		A [2]int `json:"a"`
	}]
	for _, raw := range []string{`{"a":[1,2],"extra":true}`, `{"a":[1]}`, `{"a":[1,null]}`} {
		if err := contract.Decode([]byte(raw), &x); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`null`, `{"a":[1,2]}`} {
		if err := contract.Decode([]byte(raw), &x); err != nil {
			t.Errorf("rejected %s: %v", raw, err)
		}
	}
}

func TestStrictDataNullableTypeCheck(t *testing.T) {
	s := &typedBoundaryScope{}
	if err := sdk.Provide(s, contract.Service{}, func(_ context.Context, x schema.Nullable[any]) (schema.Nullable[any], error) { return x, nil }); err == nil {
		out, callErr := s.handler(t.Context(), json.RawMessage(`9007199254740993`))
		t.Errorf("Nullable[any] registered; out=%s err=%v", out, callErr)
	}
}
