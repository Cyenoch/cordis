package cordis_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis"
)

type settings struct {
	Port   int
	Secret string
}

func validPort(c settings) error {
	if c.Port <= 0 {
		return errors.New("port must be positive")
	}
	return nil
}

func TestInvalidUpdateKeepsInstance(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		p := cordis.Plugin[settings]{Name: "p", Validate: validPort, Start: func(_ context.Context, s *cordis.Scope, c settings) error {
			log.add("start")
			s.Defer(note(&log, "cleanup"))
			return nil
		}}
		inst := p.Load(app.Scope(), settings{Port: 80})
		mustWait(t, ctx, inst, cordis.Active)
		if err := inst.Update(ctx, settings{Port: -1}); err == nil || !contains(err.Error(), "port must be positive") {
			t.Fatalf("update = %v", err)
		}
		if got := inst.Status(); got.State != cordis.Active || got.Activations != 1 {
			t.Fatalf("status = %+v", got)
		}
		log.expect(t, "start")
	})
}

func TestInPlaceUpdate(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		var port atomic.Int64
		p := cordis.Plugin[settings]{Name: "p", Start: func(_ context.Context, s *cordis.Scope, c settings) error {
			log.add("start")
			port.Store(int64(c.Port))
			s.Defer(note(&log, "cleanup"))
			return cordis.OnUpdate(s, func(_ context.Context, c settings) (bool, error) {
				if c.Port == 0 {
					return false, errBoom
				}
				port.Store(int64(c.Port))
				return true, nil
			})
		}}
		inst := p.Load(app.Scope(), settings{Port: 80})
		mustWait(t, ctx, inst, cordis.Active)
		if err := inst.Update(ctx, settings{Port: 81}); err != nil {
			t.Fatal(err)
		}
		if port.Load() != 81 {
			t.Fatalf("port = %d", port.Load())
		}
		log.expect(t, "start")

		if err := inst.Update(ctx, settings{}); !errors.Is(err, errBoom) {
			t.Fatalf("failing handler: update = %v", err)
		}
		if got := inst.Status().State; got != cordis.Failed {
			t.Fatalf("state = %v", got)
		}
		log.expect(t, "start", "cleanup")
	})
}

func TestOnUpdateConfigTypeMismatch(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		p := cordis.Plugin[settings]{Name: "p", Start: func(_ context.Context, s *cordis.Scope, _ settings) error {
			return cordis.OnUpdate(s, func(context.Context, int) (bool, error) { return true, nil })
		}}
		if err := p.Load(app.Scope(), settings{}).Wait(ctx); !errors.Is(err, cordis.ErrTypeMismatch) {
			t.Fatalf("wait = %v", err)
		}
	})
}

func TestConsecutiveUpdatesConverge(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		gate := make(chan struct{})
		var last atomic.Int64
		p := cordis.Plugin[settings]{Name: "p", Start: func(ctx context.Context, _ *cordis.Scope, c settings) error {
			if c.Port == 2 {
				select {
				case <-gate:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			last.Store(int64(c.Port))
			return nil
		}}
		inst := p.Load(app.Scope(), settings{Port: 1})
		mustWait(t, ctx, inst, cordis.Active)
		first := make(chan error, 1)
		go func() { first <- inst.Update(ctx, settings{Port: 2}) }()
		synctest.Wait() // the restart for port 2 is blocked in Start
		if err := inst.Update(ctx, settings{Port: 3}); err != nil {
			t.Fatal(err)
		}
		if err := <-first; !errors.Is(err, cordis.ErrSuperseded) {
			t.Fatalf("first update = %v, want ErrSuperseded", err)
		}
		if got := inst.Status().State; got != cordis.Active || last.Load() != 3 {
			t.Fatalf("state = %v, port = %d", got, last.Load())
		}
	})
}

func TestCanceledUpdateWaitStillApplies(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		gate := make(chan struct{})
		var last atomic.Int64
		p := cordis.Plugin[settings]{Name: "p", Start: func(_ context.Context, _ *cordis.Scope, c settings) error {
			if c.Port == 2 {
				<-gate
			}
			last.Store(int64(c.Port))
			return nil
		}}
		inst := p.Load(app.Scope(), settings{Port: 1})
		mustWait(t, ctx, inst, cordis.Active)
		waitCtx, cancel := context.WithCancel(ctx)
		result := make(chan error, 1)
		go func() { result <- inst.Update(waitCtx, settings{Port: 2}) }()
		synctest.Wait()
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("update = %v", err)
		}
		close(gate)
		mustWait(t, ctx, inst, cordis.Active)
		if last.Load() != 2 {
			t.Fatalf("port = %d", last.Load())
		}
	})
}

func TestRestartAndStoppedHandle(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		p := cordis.Plugin[settings]{Name: "p", Start: func(_ context.Context, s *cordis.Scope, c settings) error {
			log.add("start")
			s.Defer(note(&log, "cleanup"))
			return nil
		}}
		inst := p.Load(app.Scope(), settings{Port: 1})
		mustWait(t, ctx, inst, cordis.Active)
		if err := inst.Restart(ctx); err != nil {
			t.Fatal(err)
		}
		log.expect(t, "start", "cleanup", "start")
		if err := inst.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		if err := inst.Update(ctx, settings{Port: 2}); !errors.Is(err, cordis.ErrInactive) {
			t.Fatalf("update after stop = %v", err)
		}
		if got := inst.Status().Activations; got != 2 {
			t.Fatalf("activations = %d", got)
		}
	})
}

func TestLifecycleEvents(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		var failure error
		cordis.InstanceChanged.On(app.Scope(), func(_ context.Context, c cordis.InstanceChange) error {
			if c.Instance.Name == "broken" {
				log.add(c.From.String() + " -> " + c.To.String())
				if c.To == cordis.Failed {
					failure = c.Err
				}
			}
			return nil
		})
		broken := load(app.Scope(), plugin("broken", nil, func(context.Context, *cordis.Scope) error { return errBoom }))
		broken.Wait(ctx)
		synctest.Wait()
		log.expect(t, "waiting -> starting", "starting -> quiescing", "quiescing -> failed")
		if !errors.Is(failure, errBoom) {
			t.Fatalf("failure = %v", failure)
		}
	})
}

func TestLifecycleListenerMayRestart(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		attempts := 0
		ready := make(chan *cordis.Instance[struct{}], 1)
		cordis.InstanceChanged.On(app.Scope(), func(ctx context.Context, c cordis.InstanceChange) error {
			if c.Instance.Name != "flaky" {
				return nil
			}
			log.add(c.To.String())
			if c.To == cordis.Failed {
				return (<-ready).Restart(ctx)
			}
			return nil
		})
		inst := load(app.Scope(), plugin("flaky", nil, func(context.Context, *cordis.Scope) error {
			attempts++
			if attempts == 1 {
				return errBoom
			}
			return nil
		}))
		ready <- inst
		synctest.Wait()
		mustWait(t, ctx, inst, cordis.Active)
		log.expect(t, "starting", "quiescing", "failed", "starting", "active")
	})
}

func TestServiceChangeStaysInItsRealm(t *testing.T) {
	kernel(t, func(ctx context.Context, app *cordis.App) {
		var log journal
		isolated := app.Scope().Isolate(dbKey)
		watch := func(name string) func(context.Context, cordis.ServiceChange) error {
			return func(_ context.Context, c cordis.ServiceChange) error {
				if c.Service == "db" && c.Available {
					log.add(name + " saw " + c.Provider.Name)
				}
				return nil
			}
		}
		cordis.ServiceChanged.On(isolated, watch("inside"))
		cordis.ServiceChanged.On(app.Scope(), watch("outside"))
		load(isolated, provider("db", dbKey, database(sqlite("one"))))
		synctest.Wait()
		log.expect(t, "inside saw db")
	})
}

func TestLoggingOmitsConfigAndKeepsStackOutOfStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out bytes.Buffer
		app := cordis.New(cordis.Options{Logger: slog.New(slog.NewTextHandler(&out, nil)), CapturePanic: true})
		p := cordis.Plugin[settings]{Name: "secretive", Start: func(context.Context, *cordis.Scope, settings) error {
			panic("exploded")
		}}
		inst := p.Load(app.Scope(), settings{Port: 1, Secret: "hunter2"})
		err := inst.Wait(t.Context())
		app.Close(t.Context())
		logged := out.String()
		for _, want := range []string{"secretive", "starting", "failed", "exploded", "update_test.go", "stack="} {
			if !contains(logged, want) {
				t.Errorf("log does not contain %q:\n%s", want, logged)
			}
		}
		if contains(logged, "hunter2") {
			t.Errorf("log leaks configuration:\n%s", logged)
		}
		if contains(err.Error(), "goroutine ") || contains(inst.Status().Err.Error(), "update_test.go") {
			t.Errorf("status carries the stack: %v", err)
		}
	})
}
