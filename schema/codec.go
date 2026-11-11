package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"

	"github.com/Cyenoch/cordis/contract"
)

// Codec ties a Go type to the schema of its JSON form. Every value is
// validated on the way out and on the way in, so a type whose encoding does
// not fit the schema fails at the boundary instead of reaching the other
// side. Code generated from Go source uses one Codec per request, response
// and configuration type.
type Codec[T any] struct {
	source json.RawMessage
	schema *Schema
	err    error
}

// NewCodec compiles source. A schema that does not compile makes every use
// of the codec fail with the compile error.
func NewCodec[T any](source string) *Codec[T] {
	c := &Codec[T]{source: json.RawMessage(source)}
	c.schema, c.err = Compile(c.source)
	return c
}

// Schema returns the schema the codec validates against.
func (c *Codec[T]) Schema() json.RawMessage { return append(json.RawMessage(nil), c.source...) }

// Encode returns the JSON form of value after validating it.
func (c *Codec[T]) Encode(value T) (json.RawMessage, error) {
	if c.err != nil {
		return nil, c.err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode %T: %w", value, err)
	}
	if err := c.schema.Validate(data); err != nil {
		return nil, err
	}
	return data, nil
}

// Decode validates data and decodes it.
func (c *Codec[T]) Decode(data json.RawMessage) (value T, err error) {
	if c.err != nil {
		return value, c.err
	}
	if err := c.schema.Validate(data); err != nil {
		return value, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, fmt.Errorf("decode %T: %w", value, err)
	}
	return value, nil
}

// Check reports whether value satisfies the schema.
func (c *Codec[T]) Check(value T) error {
	_, err := c.Encode(value)
	return err
}

// Operation is one service method: its name on the wire and the codecs of its
// request and of its response, or of one item when the method streams.
type Operation[In, Out any] struct {
	Name string
	In   *Codec[In]
	Out  *Codec[Out]
}

// NewOperation builds an operation from the schemas of its request and response.
func NewOperation[In, Out any](name, request, response string) Operation[In, Out] {
	return Operation[In, Out]{Name: name, In: NewCodec[In](request), Out: NewCodec[Out](response)}
}

// Serve answers one call with fn. A request or response that does not fit
// the contract is reported with its reason, so the caller of another process
// sees more than a failed handler.
func (m Operation[In, Out]) Serve(ctx context.Context, input json.RawMessage, fn func(context.Context, In) (Out, error)) (json.RawMessage, error) {
	in, err := m.In.Decode(input)
	if err != nil {
		return nil, m.rejected(contract.Invalid, "request", err)
	}
	out, err := fn(ctx, in)
	if err != nil {
		return nil, err
	}
	output, err := m.Out.Encode(out)
	if err != nil {
		return nil, m.rejected(contract.PluginFailed, "response", err)
	}
	return output, nil
}

func (m Operation[In, Out]) rejected(code contract.Code, side string, err error) error {
	return &contract.Error{Code: code, Message: fmt.Sprintf("%s %s: %v", m.Name, side, err)}
}

// Call calls the method on endpoint.
func (m Operation[In, Out]) Call(ctx context.Context, endpoint contract.Endpoint, in In) (out Out, err error) {
	input, err := m.In.Encode(in)
	if err != nil {
		return out, err
	}
	output, err := endpoint.Call(ctx, m.Name, input)
	if err != nil {
		return out, err
	}
	return m.Out.Decode(output)
}

// ServeStream answers one streaming call with fn.
func (m Operation[In, Out]) ServeStream(ctx context.Context, input json.RawMessage, fn func(context.Context, In) iter.Seq2[Out, error]) iter.Seq2[json.RawMessage, error] {
	return func(yield func(json.RawMessage, error) bool) {
		in, err := m.In.Decode(input)
		if err != nil {
			yield(nil, m.rejected(contract.Invalid, "request", err))
			return
		}
		for out, err := range fn(ctx, in) {
			if err != nil {
				yield(nil, err)
				return
			}
			item, err := m.Out.Encode(out)
			if err != nil {
				yield(nil, m.rejected(contract.PluginFailed, "item", err))
				return
			}
			if !yield(item, nil) {
				return
			}
		}
	}
}

// CallStream calls the streaming method on endpoint.
func (m Operation[In, Out]) CallStream(ctx context.Context, endpoint contract.Endpoint, in In) iter.Seq2[Out, error] {
	return func(yield func(Out, error) bool) {
		var zero Out
		input, err := m.In.Encode(in)
		if err != nil {
			yield(zero, err)
			return
		}
		for item, err := range endpoint.Stream(ctx, m.Name, input) {
			var out Out
			if err == nil {
				out, err = m.Out.Decode(item)
			}
			if err != nil {
				yield(zero, err)
				return
			}
			if !yield(out, nil) {
				return
			}
		}
	}
}
