package cordis_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis"
)

var ping = cordis.NewEvent[int]("ping")

// listener loads a plugin that registers one listener and waits for it.
func listener(t *testing.T, ctx context.Context, s *cordis.Scope, name string, fn func(context.Context, int) error, options ...cordis.ListenOption) *cordis.Instance[struct{}] {
	t.Helper()
	inst := load(s, plugin(name, nil, func(_ context.Context, s *cordis.Scope) error {
		ping.On(s, fn, options...)
		return nil
	}))
	mustWait(t, ctx, inst, cordis.Active)
	return inst
}

func TestEmitStopsAtFirstError(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		listener(t, ctx, app.Scope(), "one", func(context.Context, int) error { log.add("one"); return nil })
		listener(t, ctx, app.Scope(), "two", func(context.Context, int) error { log.add("two"); return errBoom })
		listener(t, ctx, app.Scope(), "three", func(context.Context, int) error { log.add("three"); return nil })
		err := ping.Emit(ctx, app.Scope(), 1)
		var located *cordis.HandlerError
		if !errors.Is(err, errBoom) || !errors.As(err, &located) || located.Name != "ping" || located.Instance.Name != "two" {
			t.Fatalf("emit = %v", err)
		}
		log.expect(t, "one", "two")
	})
}

func TestEmitParallelJoinsErrors(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var calls atomic.Int32
		first, second := errors.New("first"), errors.New("second")
		listener(t, ctx, app.Scope(), "a", func(context.Context, int) error { calls.Add(1); return first })
		listener(t, ctx, app.Scope(), "b", func(context.Context, int) error { calls.Add(1); return nil })
		listener(t, ctx, app.Scope(), "c", func(context.Context, int) error { calls.Add(1); return second })
		err := ping.EmitParallel(ctx, app.Scope(), 1)
		if calls.Load() != 3 || !errors.Is(err, first) || !errors.Is(err, second) {
			t.Fatalf("calls = %d, err = %v", calls.Load(), err)
		}
	})
}

func TestOnceUnderConcurrentEmits(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var calls atomic.Int32
		listener(t, ctx, app.Scope(), "once", func(context.Context, int) error { calls.Add(1); return errBoom }, cordis.Once)
		done := make(chan struct{})
		for range 2 {
			go func() { ping.Emit(ctx, app.Scope(), 1); done <- struct{}{} }()
		}
		<-done
		<-done
		ping.Emit(ctx, app.Scope(), 1)
		if calls.Load() != 1 {
			t.Fatalf("calls = %d, want 1", calls.Load())
		}
	})
}

func TestPrependAndReregister(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		s := app.Scope()
		ping.On(s, func(context.Context, int) error { log.add("A"); return nil })
		b := func(context.Context, int) error { log.add("B"); return nil }
		stop := ping.On(s, b, cordis.Prepend)
		ping.Emit(ctx, s, 1)
		stop()
		stop()
		ping.On(s, b)
		ping.Emit(ctx, s, 1)
		log.expect(t, "B", "A", "A", "B")
	})
}

func TestSameEventNameDifferentPayload(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		called := false
		ping.On(app.Scope(), func(context.Context, int) error { called = true; return nil })
		err := cordis.NewEvent[string]("ping").Emit(ctx, app.Scope(), "x")
		if !errors.Is(err, cordis.ErrTypeMismatch) || called {
			t.Fatalf("emit = %v, called = %v", err, called)
		}
	})
}

func TestQueryExplicitZeroAnswer(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		q := cordis.NewQuery[string, int]("count")
		s := app.Scope()
		q.Handle(s, func(context.Context, string) (int, bool, error) { log.add("one"); return 5, false, nil })
		q.Handle(s, func(context.Context, string) (int, bool, error) { log.add("two"); return 0, true, nil })
		q.Handle(s, func(context.Context, string) (int, bool, error) { log.add("three"); return 9, true, nil })
		out, ok, err := q.Ask(ctx, s, "x")
		if out != 0 || !ok || err != nil {
			t.Fatalf("ask = %d, %v, %v", out, ok, err)
		}
		log.expect(t, "one", "two")
	})
}

func TestQueryUnanswered(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		q := cordis.NewQuery[string, int]("count")
		q.Handle(app.Scope(), func(context.Context, string) (int, bool, error) { return 5, false, nil })
		out, ok, err := q.Ask(ctx, app.Scope(), "x")
		if out != 0 || ok || err != nil {
			t.Fatalf("ask = %d, %v, %v", out, ok, err)
		}
	})
}

var pipeline = cordis.NewChain[int, string]("pipeline")

func TestChainModifyShortCircuitWrap(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		s := app.Scope()
		pipeline.Use(s, func(ctx context.Context, in int, next cordis.Handler[int, string]) (string, error) {
			out, err := next(ctx, in+1)
			return "A(" + out + ")", err
		})
		pipeline.Use(s, func(_ context.Context, in int, _ cordis.Handler[int, string]) (string, error) {
			return fmt.Sprintf("B%d", in), nil
		})
		out, err := pipeline.Do(ctx, s, 1, func(context.Context, int) (string, error) {
			t.Error("final must not run")
			return "", nil
		})
		if out != "A(B2)" || err != nil {
			t.Fatalf("do = %q, %v", out, err)
		}
	})
}

func TestChainNextOnlyOnce(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var second error
		finals := 0
		pipeline.Use(app.Scope(), func(ctx context.Context, in int, next cordis.Handler[int, string]) (string, error) {
			out, err := next(ctx, in)
			_, second = next(ctx, in)
			return out, err
		})
		out, err := pipeline.Do(ctx, app.Scope(), 1, func(context.Context, int) (string, error) { finals++; return "ok", nil })
		if out != "ok" || err != nil || second == nil || finals != 1 {
			t.Fatalf("do = %q, %v; second next = %v; finals = %d", out, err, second, finals)
		}
	})
}

func TestChainWithoutMiddleware(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		out, err := pipeline.Do(ctx, app.Scope(), 3, func(_ context.Context, in int) (string, error) { return fmt.Sprint(in), nil })
		if out != "3" || err != nil {
			t.Fatalf("do = %q, %v", out, err)
		}
	})
}

func TestChainCancellation(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		callCtx, cancel := context.WithCancel(ctx)
		observed := false
		pipeline.Use(app.Scope(), func(ctx context.Context, in int, next cordis.Handler[int, string]) (string, error) {
			cancel()
			return next(ctx, in)
		})
		pipeline.Use(app.Scope(), func(ctx context.Context, in int, next cordis.Handler[int, string]) (string, error) {
			observed = true
			return next(ctx, in)
		})
		_, err := pipeline.Do(callCtx, app.Scope(), 1, func(context.Context, int) (string, error) { return "final", nil })
		if !errors.Is(err, context.Canceled) || observed {
			t.Fatalf("do = %v, downstream ran = %v", err, observed)
		}
	})
}

func TestFilterToSubtree(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		var treeScope *cordis.Scope
		tree := load(app.Scope(), plugin("tree", nil, func(_ context.Context, s *cordis.Scope) error {
			treeScope = s
			load(s, plugin("inside", nil, func(_ context.Context, s *cordis.Scope) error {
				ping.On(s, func(context.Context, int) error { log.add("inside"); return nil })
				return nil
			}))
			return nil
		}))
		mustWait(t, ctx, tree, cordis.Active)
		synctest.Wait()
		listener(t, ctx, app.Scope(), "outside", func(context.Context, int) error { log.add("outside"); return nil })
		listener(t, ctx, app.Scope(), "global", func(context.Context, int) error { log.add("global"); return nil }, cordis.Global)
		scoped := app.Scope().Filter(func(l *cordis.Scope) bool { return l.Within(treeScope) })
		if err := ping.Emit(ctx, scoped, 1); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "inside", "global")
	})
}

func TestFilterToSameRealmAndIsolationDoesNotScopeEvents(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		isolated := app.Scope().Isolate(dbKey)
		listener(t, ctx, isolated, "inside", func(context.Context, int) error { log.add("inside"); return nil })
		listener(t, ctx, app.Scope(), "outside", func(context.Context, int) error { log.add("outside"); return nil })
		if err := ping.Emit(ctx, app.Scope(), 1); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "inside", "outside")
		same := isolated.Filter(func(l *cordis.Scope) bool { return l.Shares(isolated, dbKey) })
		if err := ping.Emit(ctx, same, 1); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "inside", "outside", "inside")
	})
}

func TestProviderWorksWhileConsumersCleanUp(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		requests := make(chan string)
		queue := cordis.NewService[chan string]("queue")
		p := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			ping.On(s, func(context.Context, int) error { log.add("provider listener"); return nil })
			s.Go(func(ctx context.Context) error {
				for {
					select {
					case r := <-requests:
						log.add("worker handled " + r)
					case <-ctx.Done():
						log.add("provider canceled")
						return ctx.Err()
					}
				}
			})
			return queue.Provide(s, requests)
		}))
		c := load(app.Scope(), plugin("c", []cordis.Requirement{queue}, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(func(ctx context.Context) error {
				q, err := queue.Get(s)
				if err != nil {
					return err
				}
				q <- "unsubscribe"
				return ping.Emit(ctx, s, 1)
			})
			return nil
		}))
		synctest.Wait()
		mustWait(t, ctx, c, cordis.Active)
		if err := p.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		// The worker and the listener both served the consumer's cleanup, in
		// either order, before the provider was canceled.
		got := log.list()
		slices.Sort(got[:len(got)-1])
		if !slices.Equal(got, []string{"provider listener", "worker handled unsubscribe", "provider canceled"}) {
			t.Fatalf("journal = %v", got)
		}
	})
}

func TestParentWorksWhileChildrenCleanUp(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		requests := make(chan string)
		queue := cordis.NewService[chan string]("queue")
		parent := load(app.Scope(), plugin("parent", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Go(func(ctx context.Context) error {
				for {
					select {
					case r := <-requests:
						log.add("worker handled " + r)
					case <-ctx.Done():
						log.add("parent canceled")
						return ctx.Err()
					}
				}
			})
			if err := queue.Provide(s, requests); err != nil {
				return err
			}
			load(s, plugin("child", nil, func(_ context.Context, s *cordis.Scope) error {
				s.Defer(func(context.Context) error {
					q, err := queue.Get(s)
					if err != nil {
						return err
					}
					q <- "goodbye"
					return nil
				})
				return nil
			}))
			return nil
		}))
		mustWait(t, ctx, parent, cordis.Active)
		synctest.Wait()
		if err := parent.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "worker handled goodbye", "parent canceled")
	})
}

func TestCleanupWaitsForInflightCallback(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		entered := make(chan struct{})
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			s.Defer(note(&log, "cleanup"))
			ping.On(s, func(ctx context.Context, _ int) error {
				close(entered)
				<-ctx.Done()
				log.add("listener saw cancel")
				return nil
			})
			return nil
		}))
		mustWait(t, ctx, inst, cordis.Active)
		go ping.Emit(ctx, app.Scope(), 1)
		<-entered
		if err := inst.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "listener saw cancel", "cleanup")
	})
}

func TestWaitingForOwnStopIsReentrant(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var inst *cordis.Instance[struct{}]
		var got error
		inst = load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			ping.On(s, func(ctx context.Context, _ int) error {
				got = inst.Stop(ctx)
				return nil
			})
			return nil
		}))
		mustWait(t, ctx, inst, cordis.Active)
		if err := ping.Emit(ctx, app.Scope(), 1); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(got, cordis.ErrReentrant) {
			t.Fatalf("stop from own listener = %v", got)
		}
		<-inst.Done()
	})
}

func TestNestedCallbackWaitingForUpstreamStop(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		pong := cordis.NewEvent[int]("pong")
		var a *cordis.Instance[struct{}]
		var got error
		a = load(app.Scope(), plugin("a", nil, func(_ context.Context, s *cordis.Scope) error {
			ping.On(s, func(ctx context.Context, n int) error { return pong.Emit(ctx, s, n) })
			return nil
		}))
		b := load(app.Scope(), plugin("b", nil, func(_ context.Context, s *cordis.Scope) error {
			pong.On(s, func(ctx context.Context, _ int) error {
				got = a.Stop(ctx)
				return nil
			})
			return nil
		}))
		mustWait(t, ctx, a, cordis.Active)
		mustWait(t, ctx, b, cordis.Active)
		if err := ping.Emit(ctx, app.Scope(), 1); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(got, cordis.ErrReentrant) {
			t.Fatalf("stop from nested listener = %v", got)
		}
		<-a.Done()
	})
}

func TestReleaseFromOwnCallback(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var got error
		calls := 0
		sub, release := app.Scope().Sub()
		ping.On(sub, func(ctx context.Context, _ int) error {
			calls++
			got = release(ctx)
			return nil
		})
		ping.Emit(ctx, app.Scope(), 1)
		synctest.Wait()
		ping.Emit(ctx, app.Scope(), 1)
		if !errors.Is(got, cordis.ErrReentrant) || calls != 1 {
			t.Fatalf("release = %v, calls = %d", got, calls)
		}
	})
}

func TestUncooperativeCallbackStaysVisible(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		entered, release := make(chan struct{}), make(chan struct{})
		inst := load(app.Scope(), plugin("p", nil, func(_ context.Context, s *cordis.Scope) error {
			ping.On(s, func(context.Context, int) error {
				close(entered)
				<-release
				return nil
			})
			return nil
		}))
		mustWait(t, ctx, inst, cordis.Active)
		go ping.Emit(ctx, app.Scope(), 1)
		<-entered
		short, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := inst.Stop(short); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stop = %v", err)
		}
		if got := inst.Status().State; got != cordis.Quiescing {
			t.Fatalf("state = %v, want quiescing", got)
		}
		close(release)
		if err := inst.Stop(ctx); err != nil {
			t.Fatal(err)
		}
	})
}
