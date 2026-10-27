package sdk_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/schema"
	"github.com/Cyenoch/cordis/sdk"
)

type typedBoundaryScope struct {
	contract.Scope
	handler contract.Handler
}

func (s *typedBoundaryScope) Provide(_ contract.Service, h contract.Handler) error {
	s.handler = h
	return nil
}
func (s *typedBoundaryScope) Call(ctx context.Context, _ contract.Requirement, in json.RawMessage) (json.RawMessage, error) {
	return s.handler(ctx, in)
}
func TestTypedFixedArrayRejectsLossyInput(t *testing.T) {
	s := &typedBoundaryScope{}
	calls := 0
	if err := sdk.Provide(s, contract.Service{Name: "sum", Major: 1}, func(_ context.Context, v [2]int) ([2]int, error) { calls++; return v, nil }); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`[1,2,3]`, `[1]`} {
		out, err := s.handler(t.Context(), json.RawMessage(raw))
		t.Logf("%s -> %s, err=%v", raw, out, err)
		if err == nil {
			t.Errorf("accepted lossy array")
		}
	}
	t.Logf("invalid inputs reached handler %d times", calls)
}
func TestTypedRejectImplicitNull(t *testing.T) {
	s := &typedBoundaryScope{}
	calls := 0
	if err := sdk.Provide(s, contract.Service{Name: "update", Major: 1}, func(_ context.Context, v struct {
		Enabled bool `json:"enabled"`
	}) (bool, error) {
		calls++
		return v.Enabled, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `{"enabled":null}`} {
		out, err := s.handler(t.Context(), json.RawMessage(raw))
		t.Logf("%s -> %s, err=%v", raw, out, err)
		if err == nil {
			t.Errorf("accepted implicit null as a concrete false value")
		}
	}
	t.Logf("invalid inputs reached handler %d times", calls)
}
func TestTypedNullableRetainsUnknownFieldRejection(t *testing.T) {
	type payload struct {
		Value schema.Nullable[struct {
			ID string `json:"id"`
		}] `json:"value"`
	}
	s := &typedBoundaryScope{}
	calls := 0
	if err := sdk.Provide(s, contract.Service{Name: "update", Major: 1}, func(_ context.Context, v payload) (payload, error) { calls++; return v, nil }); err != nil {
		t.Fatal(err)
	}
	out, err := s.handler(t.Context(), json.RawMessage(`{"value":{"id":"one","unexpected":true}}`))
	t.Logf("handler calls=%d, out=%s err=%v", calls, out, err)
	if err == nil {
		t.Error("nullable silently discarded an unknown request field")
	}
}
func TestTypedIntKeepsSafeWireRange(t *testing.T) {
	s := &typedBoundaryScope{}
	if err := sdk.Provide(s, contract.Service{Name: "sequence", Major: 1}, func(context.Context, struct{}) (int, error) { return int(9007199254740993), nil }); err != nil {
		t.Fatal(err)
	}
	out, err := s.handler(t.Context(), json.RawMessage(`{}`))
	t.Logf("wire integer=%s err=%v", out, err)
	if err == nil {
		t.Error("raw int bypassed the decimal-string requirement for 64-bit values")
	}
}

type EmbeddedBase struct {
	ID string `json:"id"`
}
type auditEmbedded struct {
	EmbeddedBase
	Name string `json:"name"`
}

func TestTypedEmbeddedStructRoundTrip(t *testing.T) {
	s := &typedBoundaryScope{}
	if err := sdk.Provide(s, contract.Service{Name: "echo", Major: 1}, func(_ context.Context, v auditEmbedded) (auditEmbedded, error) { return v, nil }); err != nil {
		t.Fatal(err)
	}
	in := auditEmbedded{EmbeddedBase: EmbeddedBase{ID: "one"}, Name: "alice"}
	out, err := sdk.Call[auditEmbedded, auditEmbedded](t.Context(), s, contract.Requirement{Name: "echo", Major: 1}, in)
	if err != nil {
		t.Fatalf("registered serializable struct cannot roundtrip: %v", err)
	}
	if out != in {
		t.Fatalf("output=%+v want=%+v", out, in)
	}
}
