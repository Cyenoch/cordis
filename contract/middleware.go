package contract

import (
	"context"
	"encoding/json"
	"sync/atomic"
)

// Middleware wraps a handler; next may be called at most once per invocation.
type Middleware func(context.Context, json.RawMessage, Handler) (json.RawMessage, error)

func Chain(final Handler, middlewares ...Middleware) Handler {
	next := final
	for n := len(middlewares) - 1; n >= 0; n-- {
		downstream, middleware := next, middlewares[n]
		next = func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var used atomic.Bool
			once := func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
				if !used.CompareAndSwap(false, true) {
					return nil, Fail(Reentrant, "middleware next already called")
				}
				return downstream(ctx, input)
			}
			return middleware(ctx, input, once)
		}
	}
	return next
}
