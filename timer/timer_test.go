package timer_test

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/timer"
)

// instance loads a plugin whose Start is start and waits until it is Active.
func instance(t *testing.T, app *cordis.App, start func(context.Context, *cordis.Scope) error) *cordis.Instance[struct{}] {
	t.Helper()
	inst := cordis.Plugin[struct{}]{Name: "timers", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		return start(ctx, s)
	}}.Load(app.Scope(), struct{}{})
	if err := inst.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	return inst
}

type log struct {
	mu    sync.Mutex
	items []int
}

func (l *log) add(n int) { l.mu.Lock(); l.items = append(l.items, n); l.mu.Unlock() }
func (l *log) get() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.items)
}

func TestAfterFuncFiresOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := cordis.New(cordis.Options{})
		defer app.Close(context.Background())
		var fired log
		instance(t, app, func(_ context.Context, s *cordis.Scope) error {
			timer.AfterFunc(s, time.Second, func(context.Context) { fired.add(1) })
			return nil
		})
		time.Sleep(999 * time.Millisecond)
		synctest.Wait()
		if len(fired.get()) != 0 {
			t.Fatal("fired early")
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := fired.get(); len(got) != 1 {
			t.Fatalf("fired %d times", len(got))
		}
	})
}

func TestEveryStopsWhenCanceledIncludingFromItsOwnCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := cordis.New(cordis.Options{})
		defer app.Close(context.Background())
		var outer, inner log
		var stopOuter, stopInner func()
		instance(t, app, func(_ context.Context, s *cordis.Scope) error {
			stopOuter = timer.Every(s, time.Second, func(context.Context) { outer.add(1) })
			stopInner = timer.Every(s, time.Second, func(context.Context) {
				inner.add(1)
				stopInner() // returns at once; this run completes
			})
			return nil
		})
		time.Sleep(2500 * time.Millisecond)
		synctest.Wait()
		stopOuter()
		stopOuter()
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := len(outer.get()); got != 2 {
			t.Fatalf("outer fired %d times, want 2", got)
		}
		if got := len(inner.get()); got != 1 {
			t.Fatalf("self-canceling timer fired %d times, want 1", got)
		}
	})
}

func TestTimersStopWithTheInstance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := cordis.New(cordis.Options{})
		defer app.Close(context.Background())
		var events []string
		var mu sync.Mutex
		note := func(s string) { mu.Lock(); events = append(events, s); mu.Unlock() }
		entered, release := make(chan struct{}), make(chan struct{})
		inst := instance(t, app, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(func(context.Context) error { note("cleanup"); return nil })
			timer.Every(s, time.Second, func(context.Context) {
				note("tick")
				close(entered)
				<-release
				note("tick done")
			})
			return nil
		})
		<-entered
		stopped := make(chan error, 1)
		go func() { stopped <- inst.Stop(context.Background()) }()
		synctest.Wait()
		close(release)
		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(events, []string{"tick", "tick done", "cleanup"}) {
			t.Fatalf("events = %v", events)
		}
	})
}

func TestOneShotTimersLeaveNothingBehind(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := cordis.New(cordis.Options{})
		defer app.Close(context.Background())
		var scope *cordis.Scope
		instance(t, app, func(_ context.Context, s *cordis.Scope) error { scope = s; return nil })
		synctest.Wait()
		before := runtime.NumGoroutine()
		var fired log
		for range 500 {
			timer.AfterFunc(scope, time.Second, func(context.Context) { fired.add(1) })
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := len(fired.get()); got != 500 {
			t.Fatalf("fired %d", got)
		}
		// The count is the whole process's and moves by one or two on its
		// own; timers that left a goroutine behind would add hundreds.
		if after := runtime.NumGoroutine(); after > before+50 {
			t.Fatalf("goroutines: %d before, %d after all timers fired", before, after)
		}
	})
}

func TestCallbackPanicFailsTheInstance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := cordis.New(cordis.Options{})
		defer app.Close(context.Background())
		inst := instance(t, app, func(_ context.Context, s *cordis.Scope) error {
			timer.AfterFunc(s, time.Second, func(context.Context) { panic("tick exploded") })
			return nil
		})
		time.Sleep(time.Minute)
		synctest.Wait()
		status := inst.Status()
		if status.State != cordis.Failed || !errors.Is(status.Err, cordis.ErrPanic) {
			t.Fatalf("status = %+v", status)
		}
	})
}

func TestSleepAndTicks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		if err := timer.Sleep(t.Context(), time.Second); err != nil {
			t.Fatal(err)
		}
		app := cordis.New(cordis.Options{})
		defer app.Close(context.Background())
		result := make(chan error, 1)
		inst := instance(t, app, func(_ context.Context, s *cordis.Scope) error {
			s.Go(func(ctx context.Context) error {
				result <- timer.Sleep(ctx, time.Hour)
				return nil
			})
			return nil
		})
		synctest.Wait()
		if err := inst.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("sleep = %v", err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		seen := 0
		for range timer.Ticks(ctx, time.Second) {
			seen++
			time.Sleep(3 * time.Second) // slower than the period
		}
		// Ticks at 1s, 4s and 7s; whether the one waiting at 10s is seen
		// depends on which of two timers of the same instant fires first.
		// None is delivered after ctx has ended.
		if seen < 3 || seen > 4 {
			t.Fatalf("slow consumer saw %d ticks in 10 periods", seen)
		}
	})
}

func TestThrottle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := cordis.New(cordis.Options{})
		defer app.Close(context.Background())
		var both, leadingOnly log
		var call, callLeading func(int)
		inst := instance(t, app, func(_ context.Context, s *cordis.Scope) error {
			call, _ = timer.Throttle(s, time.Second, func(_ context.Context, n int) { both.add(n) })
			callLeading, _ = timer.Throttle(s, time.Second, func(_ context.Context, n int) { leadingOnly.add(n) }, timer.NoTrailing)
			return nil
		})
		for _, n := range []int{1, 2, 3} {
			call(n)
			callLeading(n)
		}
		synctest.Wait()
		if got := both.get(); !slices.Equal(got, []int{1}) {
			t.Fatalf("during the window: %v", got)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := both.get(); !slices.Equal(got, []int{1, 3}) {
			t.Fatalf("throttled calls ran as %v, want [1 3]", got)
		}
		if got := leadingOnly.get(); !slices.Equal(got, []int{1}) {
			t.Fatalf("without trailing: %v, want [1]", got)
		}
		if err := inst.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		call(9)
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := both.get(); !slices.Equal(got, []int{1, 3}) {
			t.Fatalf("call after stop ran: %v", got)
		}
	})
}

func TestDebounce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := cordis.New(cordis.Options{})
		defer app.Close(context.Background())
		var ran log
		var call func(int)
		inst := instance(t, app, func(_ context.Context, s *cordis.Scope) error {
			call, _ = timer.Debounce(s, time.Second, func(_ context.Context, n int) { ran.add(n) })
			return nil
		})
		for _, n := range []int{1, 2, 3} {
			call(n)
			time.Sleep(500 * time.Millisecond)
			synctest.Wait()
		}
		if got := ran.get(); len(got) != 0 {
			t.Fatalf("ran before the quiet period: %v", got)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if got := ran.get(); !slices.Equal(got, []int{3}) {
			t.Fatalf("debounced calls ran as %v, want [3]", got)
		}
		if err := inst.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		call(9)
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := ran.get(); !slices.Equal(got, []int{3}) {
			t.Fatalf("call after stop ran: %v", got)
		}
	})
}

// Regressions for defects found by review: a timer and a stop can become
// ready together, and the stop must win.

func TestNoCallbackAfterTheInstanceContextIsCanceled(t *testing.T) {
	for range 100 {
		synctest.Test(t, func(t *testing.T) {
			app := cordis.New(cordis.Options{})
			defer app.Close(context.Background())
			var late log
			inst := instance(t, app, func(_ context.Context, s *cordis.Scope) error {
				timer.Every(s, time.Second, func(ctx context.Context) {
					if ctx.Err() != nil {
						late.add(1)
					}
					time.Sleep(2500 * time.Millisecond) // a tick is pending when it returns
				})
				return nil
			})
			time.Sleep(1500 * time.Millisecond)
			if err := inst.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(late.get()) != 0 {
				t.Fatal("callback entered after the instance's context was canceled")
			}
		})
	}
}

func TestStopBeforeAnImmediateTimerFires(t *testing.T) {
	for range 100 {
		synctest.Test(t, func(t *testing.T) {
			app := cordis.New(cordis.Options{})
			defer app.Close(context.Background())
			var fired log
			instance(t, app, func(_ context.Context, s *cordis.Scope) error {
				stop := timer.AfterFunc(s, 0, func(context.Context) { fired.add(1) })
				stop()
				return nil
			})
			time.Sleep(time.Second)
			synctest.Wait()
			if len(fired.get()) != 0 {
				t.Fatal("timer fired after stop")
			}
		})
	}
}

func TestDebounceCallAtExpiryStartsANewQuietPeriod(t *testing.T) {
	for range 100 {
		synctest.Test(t, func(t *testing.T) {
			app := cordis.New(cordis.Options{})
			defer app.Close(context.Background())
			var ran log
			var call func(int)
			instance(t, app, func(_ context.Context, s *cordis.Scope) error {
				call, _ = timer.Debounce(s, time.Second, func(_ context.Context, n int) { ran.add(n) })
				return nil
			})
			call(1)
			time.Sleep(time.Second) // exactly the quiet period
			call(2)
			synctest.Wait()
			for _, n := range ran.get() {
				if n == 2 {
					t.Fatal("the second call ran without a quiet period")
				}
			}
			time.Sleep(time.Minute)
			synctest.Wait()
			if got := ran.get(); got[len(got)-1] != 2 {
				t.Fatalf("ran %v, want the latest value last", got)
			}
		})
	}
}
