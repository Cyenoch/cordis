// Package timer provides timers owned by a cordis instance: they stop when
// the instance stops, and their callbacks never race with its cleanups.
//
// The functions take the caller's scope. A callback runs as managed work of
// that scope's instance, so it starts counting once the instance is Active,
// it is not invoked after the instance's context is canceled, the instance
// waits for a running callback before cleaning up, and a panic in it fails
// the instance.
package timer

import (
	"context"
	"iter"
	"sync"
	"time"

	"github.com/Cyenoch/cordis"
)

// AfterFunc calls fn once, d after the instance is Active or after the call
// if it already is. stop cancels it; it does not wait and may be called from
// fn itself.
func AfterFunc(s *cordis.Scope, d time.Duration, fn func(context.Context)) (stop func()) {
	done, stop := signal()
	s.Go(func(ctx context.Context) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
			if live(ctx, done) {
				fn(ctx)
			}
		case <-done:
		case <-ctx.Done():
		}
		return nil
	})
	return stop
}

// Every calls fn every d until stop is called or the instance stops. A slow
// fn delays the next call; ticks do not pile up. It panics if d is not
// positive, like time.NewTicker.
func Every(s *cordis.Scope, d time.Duration, fn func(context.Context)) (stop func()) {
	if d <= 0 {
		panic("timer: non-positive interval for Every")
	}
	done, stop := signal()
	s.Go(func(ctx context.Context) error {
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				// A tick can be pending together with a stop; the stop wins.
				if !live(ctx, done) {
					return nil
				}
				fn(ctx)
			case <-done:
				return nil
			case <-ctx.Done():
				return nil
			}
		}
	})
	return stop
}

// Sleep waits for d. It returns ctx's error if ctx ends first, or has
// already ended.
func Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Ticks yields the time every d until ctx ends or the loop breaks. When the
// loop body is slower than d, the ticks in between are dropped. It panics
// if d is not positive, like time.NewTicker.
func Ticks(ctx context.Context, d time.Duration) iter.Seq[time.Time] {
	if d <= 0 {
		panic("timer: non-positive interval for Ticks")
	}
	return func(yield func(time.Time) bool) {
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case now := <-t.C:
				if !yield(now) {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}
}

// ThrottleOption adjusts Throttle.
type ThrottleOption uint8

// NoTrailing drops calls made during a window instead of running the latest
// one when the window ends.
const NoTrailing ThrottleOption = 1

// Throttle returns call, which limits how often fn runs. The first call runs
// fn at once and opens a window of length d, counted from when fn returns.
// When the window ends, the latest call made during it runs and opens the
// next window. Calls after stop or after the instance has stopped do nothing.
func Throttle[T any](s *cordis.Scope, d time.Duration, fn func(context.Context, T), options ...ThrottleOption) (call func(T), stop func()) {
	trailing := true
	for _, option := range options {
		if option == NoTrailing {
			trailing = false
		}
	}
	w := &window[T]{trailing: trailing, wake: make(chan struct{}, 1)}
	done, stop := signal()
	s.Go(func(ctx context.Context) error {
		t := time.NewTimer(d)
		t.Stop()
		defer t.Stop()
		for {
			select {
			case <-w.wake:
			case <-done:
				return nil
			case <-ctx.Done():
				return nil
			}
			for {
				value, ok := w.next()
				if !ok {
					break
				}
				if !live(ctx, done) {
					return nil
				}
				fn(ctx, value)
				t.Reset(d)
				select {
				case <-t.C:
				case <-done:
					return nil
				case <-ctx.Done():
					return nil
				}
			}
		}
	})
	return w.put, stop
}

// window is the state of a throttled function: the call that opened the
// current window and the latest call made while it is open.
type window[T any] struct {
	mu       sync.Mutex
	wake     chan struct{}
	trailing bool
	open     bool
	leading  T
	value    T
	pending  bool

	hasLeading bool
}

func (w *window[T]) put(value T) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.open && !w.trailing {
		return
	}
	w.value, w.pending = value, true
	if !w.open {
		// The first call opens the window; later ones only replace the value
		// that runs when it ends.
		w.open = true
		w.leading, w.hasLeading = value, true
		w.pending = false
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
}

// next returns the value to run now: the call that opened the window, then
// the latest call made during each window. It closes the window when there
// is none.
func (w *window[T]) next() (T, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var zero T
	switch {
	case w.hasLeading:
		value := w.leading
		w.leading, w.hasLeading = zero, false
		return value, true
	case w.pending:
		value := w.value
		w.value, w.pending = zero, false
		return value, true
	}
	w.open = false
	return zero, false
}

// Debounce returns call, which runs fn with the latest value once no call
// has been made for d. Calls after stop or after the instance has stopped do
// nothing.
func Debounce[T any](s *cordis.Scope, d time.Duration, fn func(context.Context, T)) (call func(T), stop func()) {
	box := &latest[T]{wake: make(chan struct{}, 1)}
	done, stop := signal()
	s.Go(func(ctx context.Context) error {
		t := time.NewTimer(d)
		t.Stop()
		defer t.Stop()
		for {
			select {
			case <-box.wake:
				t.Reset(d)
			case <-t.C:
				// A call that arrived as the timer expired starts a new
				// quiet period instead of running without one.
				value, rest := box.take(d)
				if rest > 0 {
					t.Reset(rest)
					continue
				}
				if live(ctx, done) {
					fn(ctx, value)
				}
			case <-done:
				return nil
			case <-ctx.Done():
				return nil
			}
		}
	})
	return box.put, stop
}

// latest holds the most recent value passed to a debounced function and
// wakes its worker.
type latest[T any] struct {
	mu    sync.Mutex
	wake  chan struct{}
	value T
	at    time.Time // when value was put
}

func (l *latest[T]) put(value T) {
	l.mu.Lock()
	l.value, l.at = value, time.Now()
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// take returns the value once it has been quiet for d, and otherwise how
// much of the quiet period remains.
func (l *latest[T]) take(d time.Duration) (value T, rest time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rest = d - time.Since(l.at); rest > 0 {
		return value, rest
	}
	value = l.value
	var zero T
	l.value = zero
	return value, 0
}

// signal returns a channel closed by stop. stop is idempotent.
func signal() (done chan struct{}, stop func()) {
	done = make(chan struct{})
	var once sync.Once
	return done, func() { once.Do(func() { close(done) }) }
}

// live reports that neither the instance nor the timer has been stopped.
// Callbacks check it right before running, because a timer and a stop can
// become ready together.
func live(ctx context.Context, done chan struct{}) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}
