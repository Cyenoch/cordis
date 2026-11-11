// Package clock declares a service another package requires.
package clock

import (
	"context"
	"time"
)

// Clock tells the time.
//
//cordis:service example.clock 1.2
type Clock interface {
	Now(ctx context.Context) (time.Time, error)
}
