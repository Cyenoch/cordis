package timer_test

import (
	"context"
	"fmt"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/timer"
)

// A timer belongs to the instance whose scope it was given: stopping the
// instance cancels it.
func ExampleEvery() {
	ctx := context.Background()
	app := cordis.New(cordis.Options{})
	defer app.Close(ctx)

	ticks := make(chan int)
	poller := cordis.Plugin[struct{}]{Name: "poller", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		n := 0
		timer.Every(s, 10*time.Millisecond, func(ctx context.Context) {
			n++
			select {
			case ticks <- n:
			case <-ctx.Done():
			}
		})
		return nil
	}}
	inst := poller.Load(app.Scope(), struct{}{})

	fmt.Println(<-ticks, <-ticks)
	inst.Stop(ctx)
	select {
	case <-ticks:
		fmt.Println("still ticking")
	case <-time.After(50 * time.Millisecond):
		fmt.Println("stopped with the instance")
	}
	// Output:
	// 1 2
	// stopped with the instance
}

// Debounce runs once the calls have gone quiet, with the latest value.
func ExampleDebounce() {
	ctx := context.Background()
	app := cordis.New(cordis.Options{})
	defer app.Close(ctx)

	saved := make(chan string)
	save, _ := timer.Debounce(app.Scope(), 20*time.Millisecond, func(_ context.Context, text string) {
		saved <- text
	})
	for _, text := range []string{"h", "he", "hello"} {
		save(text)
	}
	fmt.Println(<-saved)
	// Output: hello
}
