package core

import (
	"context"

	"github.com/Cyenoch/cordis/contract"
)

// IsCleanup recognizes authority created only by the core for a bound cleanup
// call. Wire payloads cannot manufacture the private context key or activation.
func IsCleanup(ctx context.Context, owner contract.Identity) bool {
	a, _ := ctx.Value(cleanupKey{}).(*activation)
	return a != nil && a.identity == owner
}
