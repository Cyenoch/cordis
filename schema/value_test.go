package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/Cyenoch/cordis/schema"
)

func TestNullable(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		input schema.Nullable[string]
		wire  string
	}{
		{"null", schema.Nullable[string]{}, "null"},
		{"non-null zero", schema.NonNull(""), `""`},
		{"non-null value", schema.NonNull("text"), `"text"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.wire {
				t.Fatalf("got %s, want %s", data, tt.wire)
			}
			value := schema.NonNull("old")
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			if value != tt.input {
				t.Fatalf("round trip differs: %+v", value)
			}
		})
	}
	original := schema.NonNull("old")
	if err := json.Unmarshal([]byte("42"), &original); err == nil {
		t.Fatal("wrong type accepted")
	}
	if original != schema.NonNull("old") {
		t.Fatal("failed decode modified receiver")
	}
}

func TestInt64(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, wire string
		want       schema.Int64
		valid      bool
	}{
		{"max", `"9223372036854775807"`, schema.Int64(9223372036854775807), true},
		{"min", `"-9223372036854775808"`, schema.Int64(-9223372036854775808), true},
		{"zero", `"0"`, 0, true},
		{"overflow", `"9223372036854775808"`, 0, false},
		{"null", `null`, 0, false},
		{"number", `0`, 0, false},
		{"negative zero", `"-0"`, 0, false},
		{"plus", `"+1"`, 0, false},
		{"leading zero", `"01"`, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			value := schema.Int64(17)
			err := json.Unmarshal([]byte(tt.wire), &value)
			if !tt.valid {
				if err == nil {
					t.Fatal("invalid integer accepted")
				}
				if value != 17 {
					t.Fatal("failed decode modified receiver")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if value != tt.want {
				t.Fatalf("got %d, want %d", value, tt.want)
			}
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.wire {
				t.Fatalf("wire changed: %s", data)
			}
		})
	}
}

func TestUint64(t *testing.T) {
	t.Parallel()
	var value schema.Uint64
	if err := json.Unmarshal([]byte(`"18446744073709551615"`), &value); err != nil {
		t.Fatal(err)
	}
	if value != schema.Uint64(18446744073709551615) {
		t.Fatal("uint64 lost precision")
	}
	for _, raw := range []string{`"18446744073709551616"`, `"-1"`, `"00"`, `null`} {
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			t.Fatalf("invalid uint64 accepted: %s", raw)
		}
	}
}
