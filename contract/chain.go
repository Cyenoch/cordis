package contract

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/Cyenoch/cordis"
)

// KindChain names the registrations of middleware, next to KindEvent and
// KindQuery.
const KindChain Kind = "chain"

// RawMiddleware and RawHandler are the encoded forms that cross a session.
type (
	RawMiddleware = cordis.Middleware[json.RawMessage, json.RawMessage]
	RawHandler    = cordis.Handler[json.RawMessage, json.RawMessage]
)

// ChainUplink is the part of an Uplink that carries middleware chains. A
// guest runtime implements it; it reports a host that lacks the feature.
type ChainUplink interface {
	UseChain(ctx context.Context, s *cordis.Scope, name string, options SubscriptionOptions, middleware RawMiddleware) (cancel func(context.Context) error, err error)
	DoChain(ctx context.Context, name string, input json.RawMessage, final RawHandler) (json.RawMessage, error)
}

// RawChain is the kernel chain behind the ChainKey with this name.
func RawChain(name string) cordis.Chain[json.RawMessage, json.RawMessage] {
	return cordis.NewChain[json.RawMessage, json.RawMessage]("wire:" + name)
}

// ChainInvoke asks a guest to run one of its middleware. Token identifies
// this run when the middleware calls next.
type ChainInvoke struct {
	ID    string          `json:"id"`
	Token string          `json:"token"`
	Input json.RawMessage `json:"input"`
}

// ChainStep continues a run: a guest's middleware calling next, or the host
// asking the guest that started an execution for its final step.
type ChainStep struct {
	Token string          `json:"token"`
	Input json.RawMessage `json:"input"`
}

// ChainDo starts an execution from a guest. Token names the final step the
// guest keeps for it.
type ChainDo struct {
	Name  string          `json:"name"`
	Token string          `json:"token"`
	Input json.RawMessage `json:"input"`
}

// ChainKey is a middleware chain whose input and output cross process
// boundaries as validated JSON, so that plugins in different processes can
// wrap one operation. Its operations mirror cordis.Chain.
//
// Each middleware in another process costs two round trips, and an
// execution is as long as its slowest step: it has no deadline of its own,
// so give Do a context that has one. Every hop through another process
// keeps a request of that session open until the execution returns; when a
// session has none left, the hop fails with a not-ready error rather than
// wait for requests that wait for it.
type ChainKey[In, Out any] struct{ name string }

func NewChainKey[In, Out any](name string) ChainKey[In, Out] { return ChainKey[In, Out]{name: name} }

func (k ChainKey[In, Out]) Name() string { return k.name }

// Use registers middleware for the lifetime of s. The middleware may change
// the input, return without calling next, or wrap what next returns; it may
// call next at most once, and not after it has returned.
func (k ChainKey[In, Out]) Use(ctx context.Context, s *cordis.Scope, middleware func(ctx context.Context, in In, next func(context.Context, In) (Out, error)) (Out, error), options ...cordis.ListenOption) (cancel func(context.Context) error, err error) {
	for _, t := range []reflect.Type{reflect.TypeFor[In](), reflect.TypeFor[Out]()} {
		if err := CheckDataType(t); err != nil {
			return nil, err
		}
	}
	if k.name == "" || middleware == nil {
		return nil, Fail(Invalid, "chain name and middleware are required")
	}
	raw := func(ctx context.Context, data json.RawMessage, next RawHandler) (json.RawMessage, error) {
		var in In
		if err := DecodeData(data, &in); err != nil {
			return nil, err
		}
		out, err := middleware(ctx, in, func(ctx context.Context, in In) (out Out, err error) {
			data, err := Encode(in)
			if err != nil {
				return out, err
			}
			result, err := next(ctx, data)
			if err != nil {
				return out, err
			}
			err = DecodeData(result, &out)
			return out, err
		})
		if err != nil {
			return nil, err
		}
		return Encode(out)
	}
	if up, remote := uplink(s); remote {
		chains, ok := up.(ChainUplink)
		if !ok {
			return nil, Fail(Incompatible, "this runtime does not carry middleware chains")
		}
		return chains.UseChain(ctx, s, k.name, subscriptionOptions(options), raw)
	}
	return local(RawChain(k.name).Use(s, raw, options...)), nil
}

// Do runs the chain: every middleware in registration order, then final.
func (k ChainKey[In, Out]) Do(ctx context.Context, s *cordis.Scope, in In, final func(context.Context, In) (Out, error)) (out Out, err error) {
	if k.name == "" || final == nil {
		return out, Fail(Invalid, "chain name and final step are required")
	}
	data, err := Encode(in)
	if err != nil {
		return out, err
	}
	last := func(ctx context.Context, data json.RawMessage) (json.RawMessage, error) {
		var in In
		if err := DecodeData(data, &in); err != nil {
			return nil, err
		}
		out, err := final(ctx, in)
		if err != nil {
			return nil, err
		}
		return Encode(out)
	}
	var result json.RawMessage
	if up, remote := uplink(s); remote {
		chains, ok := up.(ChainUplink)
		if !ok {
			return out, Fail(Incompatible, "this runtime does not carry middleware chains")
		}
		result, err = chains.DoChain(ctx, k.name, data, last)
	} else {
		result, err = RawChain(k.name).Do(ctx, s, data, last)
	}
	if err != nil {
		return out, err
	}
	err = DecodeData(result, &out)
	return out, err
}
