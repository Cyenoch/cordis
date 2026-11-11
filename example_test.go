package cordis_test

import (
	"context"
	"fmt"

	"github.com/Cyenoch/cordis"
)

type clock interface{ Now() string }

type fixedClock string

func (c fixedClock) Now() string { return string(c) }

var clockService = cordis.NewService[clock]("clock")

// A plugin declares what it needs, reads it with Get, and registers its
// cleanup on the scope it was given.
func Example() {
	ctx := context.Background()
	app := cordis.New(cordis.Options{})

	report := cordis.Plugin[string]{
		Name:     "report",
		Requires: []cordis.Requirement{clockService},
		Start: func(_ context.Context, s *cordis.Scope, title string) error {
			c, err := clockService.Get(s)
			if err != nil {
				return err
			}
			fmt.Println(title, "at", c.Now())
			s.Defer(func(context.Context) error {
				fmt.Println(title, "stopped")
				return nil
			})
			return nil
		},
	}

	inst := report.Load(app.Scope(), "daily")
	inst.Wait(ctx)
	fmt.Println("report is", inst.Status().State)

	clockService.Provide(app.Scope(), fixedClock("09:00"))
	inst.Wait(ctx)

	app.Close(ctx)
	// Output:
	// report is waiting
	// daily at 09:00
	// daily stopped
}

// A chain runs its middleware in registration order around a final step.
func ExampleChain() {
	ctx := context.Background()
	app := cordis.New(cordis.Options{})
	defer app.Close(ctx)

	price := cordis.NewChain[int, int]("price")
	price.Use(app.Scope(), func(ctx context.Context, cents int, next cordis.Handler[int, int]) (int, error) {
		return next(ctx, cents*2) // double the input
	})
	price.Use(app.Scope(), func(ctx context.Context, cents int, next cordis.Handler[int, int]) (int, error) {
		out, err := next(ctx, cents)
		return out + 1, err // add one to the result
	})

	out, _ := price.Do(ctx, app.Scope(), 10, func(_ context.Context, cents int) (int, error) {
		return cents, nil
	})
	fmt.Println(out)
	// Output: 21
}

// A query is answered by the first handler that claims it.
func ExampleQuery() {
	ctx := context.Background()
	app := cordis.New(cordis.Options{})
	defer app.Close(ctx)

	lookup := cordis.NewQuery[string, int]("lookup")
	lookup.Handle(app.Scope(), func(_ context.Context, name string) (int, bool, error) {
		if name == "zero" {
			return 0, true, nil // the zero value is a valid answer
		}
		return 0, false, nil
	})

	_, ok, _ := lookup.Ask(ctx, app.Scope(), "one")
	fmt.Println(ok)
	n, ok, _ := lookup.Ask(ctx, app.Scope(), "zero")
	fmt.Println(n, ok)
	// Output:
	// false
	// 0 true
}
