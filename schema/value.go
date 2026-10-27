package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// Optional preserves absence independently from the zero value or explicit null.
// Generated object codecs omit a field exactly when Present is false.
type Optional[T any] struct {
	Present bool
	Value   T
}

// Some constructs a present optional field, including present zero values.
func Some[T any](value T) Optional[T] { return Optional[T]{Present: true, Value: value} }

// Nullable represents explicit JSON null without exposing Go pointer semantics.
// Valid=false encodes null; Valid=true encodes Value.
type Nullable[T any] struct {
	Valid bool
	Value T
}

func NonNull[T any](value T) Nullable[T] { return Nullable[T]{Valid: true, Value: value} }

func (n Nullable[T]) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	data, err := json.Marshal(n.Value)
	if err != nil {
		return nil, fmt.Errorf("marshal nullable value: %w", err)
	}
	return data, nil
}

func (n *Nullable[T]) UnmarshalJSON(data []byte) error {
	if n == nil {
		return fmt.Errorf("unmarshal nullable value: nil receiver")
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*n = Nullable[T]{}
		return nil
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("unmarshal nullable value: %w", err)
	}
	*n = NonNull(value)
	return nil
}

// Int64 and Uint64 have canonical decimal-string JSON representations.
type Int64 int64
type Uint64 uint64

func (n Int64) MarshalJSON() ([]byte, error)  { return json.Marshal(strconv.FormatInt(int64(n), 10)) }
func (n Uint64) MarshalJSON() ([]byte, error) { return json.Marshal(strconv.FormatUint(uint64(n), 10)) }

func (n *Int64) UnmarshalJSON(data []byte) error {
	if n == nil {
		return fmt.Errorf("unmarshal int64: nil receiver")
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil || isNull(data) {
		return fmt.Errorf("unmarshal int64: expected decimal string")
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || strconv.FormatInt(value, 10) != text {
		return fmt.Errorf("unmarshal int64: invalid canonical decimal string")
	}
	*n = Int64(value)
	return nil
}

func (n *Uint64) UnmarshalJSON(data []byte) error {
	if n == nil {
		return fmt.Errorf("unmarshal uint64: nil receiver")
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil || isNull(data) {
		return fmt.Errorf("unmarshal uint64: expected decimal string")
	}
	value, err := strconv.ParseUint(text, 10, 64)
	if err != nil || strconv.FormatUint(value, 10) != text {
		return fmt.Errorf("unmarshal uint64: invalid canonical decimal string")
	}
	*n = Uint64(value)
	return nil
}
