package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func TestManagedWorkWaitsForFinalPublication(t *testing.T) {
	for _, failure := range []string{"start", "missing-service", "duplicate"} {
		t.Run(failure, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				publishErr := errors.New("publication rejected")
				e := core.New()
				defer func() { _ = e.Close(t.Context()) }()
				if failure == "duplicate" {
					p := add(t, e, "provider", provider(`1`))
					if err := p.Wait(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				entry := contract.Entry{ID: "work", Enabled: true}
				var ran, published, cleaned atomic.Bool
				def := contract.Definition{Provides: []contract.Service{service}, Start: func(_ context.Context, s contract.Scope) error {
					if err := s.Defer(func(context.Context) error { cleaned.Store(true); return nil }); err != nil {
						return err
					}
					if err := contract.Go(s, func(context.Context) error { ran.Store(true); return nil }); err != nil {
						return err
					}
					if err := s.(contract.ActivationScope).OnPublish(func(context.Context) error { published.Store(true); return nil }); err != nil {
						return err
					}
					if failure == "start" {
						return publishErr
					}
					if failure == "missing-service" {
						return nil
					}
					return s.Provide(service, func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil })
				}}
				i, err := e.Add(entry, def, contract.Identity{})
				if err != nil {
					t.Fatal(err)
				}
				if err := i.Wait(t.Context()); err == nil {
					t.Fatal("final publication succeeded")
				}
				if ran.Load() || published.Load() || !cleaned.Load() {
					t.Fatalf("failed publication: work=%v published=%v cleanup=%v", ran.Load(), published.Load(), cleaned.Load())
				}
			})
		})
	}
}

func TestPublicationAcknowledgmentGatesAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		entered, ack := make(chan struct{}), make(chan struct{})
		var work, consumers, events atomic.Int32
		var startCtx, activationCtx context.Context
		var scope contract.Scope
		p := add(t, e, "provider", contract.Definition{Provides: []contract.Service{service}, Start: func(ctx context.Context, s contract.Scope) error {
			startCtx, activationCtx, scope = ctx, s.Context(), s
			if err := contract.Go(s, func(ctx context.Context) error {
				if ctx != activationCtx || ctx.Err() != nil {
					return errors.New("invalid managed context")
				}
				work.Add(1)
				return nil
			}); err != nil {
				return err
			}
			if _, err := s.On(ctx, "ready", contract.SubscriptionOptions{}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
				events.Add(1)
				return contract.EventResult{}, nil
			}); err != nil {
				return err
			}
			if err := s.Provide(service, func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`1`), nil }); err != nil {
				return err
			}
			return s.(contract.ActivationScope).OnPublish(func(ctx context.Context) error {
				if ctx.Err() != nil || e.IsActive(s.Identity()) {
					return errors.New("publication was already active or canceled")
				}
				close(entered)
				<-ack
				return nil
			})
		}})
		<-entered
		c := add(t, e, "consumer", contract.Definition{Requires: []contract.Requirement{requirement}, Start: func(context.Context, contract.Scope) error { consumers.Add(1); return nil }})
		waited := make(chan error, 1)
		go func() { waited <- p.Wait(t.Context()) }()
		unrelated := add(t, e, "unrelated", definitionNoop())
		if err := unrelated.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if p.Status().State != contract.Starting || e.IsActive(scope.Identity()) || work.Load() != 0 || consumers.Load() != 0 {
			t.Fatal("publication acknowledgment did not gate readiness")
		}
		select {
		case err := <-waited:
			t.Fatalf("wait completed before acknowledgment: %v", err)
		default:
		}
		if _, err := e.Call(t.Context(), requirement, nil); err == nil {
			t.Fatal("service called before publication acknowledgment")
		}
		if _, err := e.Emit(t.Context(), "ready", nil, contract.Sequential); err != nil || events.Load() != 0 {
			t.Fatalf("event admitted before acknowledgment: %v", err)
		}
		if !errors.Is(startCtx.Err(), context.Canceled) || activationCtx.Err() != nil {
			t.Fatal("Start and activation context lifetimes are not separate")
		}
		if err := contract.Go(scope, func(context.Context) error { return nil }); err == nil {
			t.Fatal("work registration remained open after Start")
		}
		close(ack)
		if err := <-waited; err != nil {
			t.Fatal(err)
		}
		if err := c.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if work.Load() != 1 || consumers.Load() != 1 || !e.IsActive(scope.Identity()) {
			t.Fatal("acknowledged activation did not become ready exactly once")
		}
	})
}

func TestLatePublicationCannotReviveRetiredWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		publishing, ack, retired := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var runs, notifications atomic.Int32
		var scope contract.Scope
		var cleaned atomic.Bool
		i := add(t, e, "late", contract.Definition{Start: func(_ context.Context, s contract.Scope) error {
			scope = s
			if err := s.Defer(func(context.Context) error { cleaned.Store(true); return nil }); err != nil {
				return err
			}
			if err := contract.Go(s, func(context.Context) error { runs.Add(1); return nil }); err != nil {
				return err
			}
			lifecycle := s.(contract.ActivationScope)
			if err := lifecycle.OnRetire(func(ctx context.Context) error {
				if ctx.Err() != nil {
					return errors.New("retirement notification inherited cancellation")
				}
				notifications.Add(1)
				close(retired)
				return nil
			}); err != nil {
				return err
			}
			return lifecycle.OnPublish(func(context.Context) error { close(publishing); <-ack; return nil })
		}})
		<-publishing
		wait, err := e.Stop(t.Context(), i)
		if err != nil {
			t.Fatal(err)
		}
		<-retired
		synctest.Wait()
		if cleaned.Load() || e.IsActive(scope.Identity()) || scope.Context().Err() == nil {
			t.Fatal("retirement released resources or retained ordinary admission")
		}
		if err := scope.(contract.ActivationScope).OnPublish(func(context.Context) error { runs.Add(1); return nil }); err == nil {
			t.Fatal("late publish registration accepted")
		}
		close(ack)
		if err := wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := i.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if runs.Load() != 0 || notifications.Load() != 1 || !cleaned.Load() || i.Status().Applied != 0 {
			t.Fatal("late publication or repeated stop revived activation")
		}
	})
}

func TestRetireNotificationPrecedesConsumerAndGlobalCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		notifying, ack := make(chan struct{}), make(chan struct{})
		workStarted, workCanceled, workExit := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var cleaned, consumerCleaned, globalRetired atomic.Bool
		var child contract.Instance
		e := core.NewWithHooks(core.Hooks{OnRetire: func(id contract.Identity) {
			if id.Entry == "provider" {
				globalRetired.Store(true)
				// Models Host.removeChildren waiting for child cleanup.
				if err := child.Stop(t.Context()); err != nil {
					t.Error(err)
				}
			}
		}})
		defer closeEngine(t, e)
		p := add(t, e, "provider", contract.Definition{Provides: []contract.Service{service}, Start: func(_ context.Context, s contract.Scope) error {
			if err := s.Defer(func(context.Context) error {
				if !consumerCleaned.Load() {
					return errors.New("provider released before consumer")
				}
				cleaned.Store(true)
				return nil
			}); err != nil {
				return err
			}
			if err := contract.Go(s, func(ctx context.Context) error {
				close(workStarted)
				<-ctx.Done()
				close(workCanceled)
				<-workExit
				return nil
			}); err != nil {
				return err
			}
			if err := s.(contract.ActivationScope).OnRetire(func(ctx context.Context) error {
				if ctx.Err() != nil || s.Context().Err() == nil {
					return errors.New("retirement context lifetime violated")
				}
				close(notifying)
				<-ack
				return nil
			}); err != nil {
				return err
			}
			return s.Provide(service, func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`1`), nil })
		}})
		if err := p.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		<-workStarted
		if _, err := e.Call(t.Context(), requirement, nil); err != nil {
			t.Fatalf("managed work occupied service gate: %v", err)
		}
		var err error
		child, err = e.Add(contract.Entry{ID: "consumer", Parent: "provider", Enabled: true}, contract.Definition{
			Requires: []contract.Requirement{requirement}, Start: func(_ context.Context, s contract.Scope) error {
				return s.Defer(func(ctx context.Context) error {
					select {
					case <-workCanceled:
					default:
						return errors.New("consumer cleanup preceded work cancellation")
					}
					if _, err := s.Call(ctx, requirement, nil); err != nil {
						return err
					}
					consumerCleaned.Store(true)
					return nil
				})
			},
		}, contract.Identity{})
		if err != nil {
			t.Fatal(err)
		}
		if err := child.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		wait, err := e.Stop(t.Context(), p)
		if err != nil {
			t.Fatal(err)
		}
		<-notifying
		<-workCanceled
		synctest.Wait()
		if consumerCleaned.Load() || cleaned.Load() || globalRetired.Load() {
			t.Fatal("cleanup or global hook overtook per-scope retirement acknowledgment")
		}
		close(ack)
		synctest.Wait()
		if !consumerCleaned.Load() || !globalRetired.Load() || cleaned.Load() {
			t.Fatal("consumer cleanup waited for provider work exit, or provider released too early")
		}
		close(workExit)
		if err := wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !cleaned.Load() {
			t.Fatal("provider resources were not released after managed work exited")
		}
	})
}

func TestRetireDuringStartNotifiesLateRegistrations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		started, early, late := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var count atomic.Int32
		i := add(t, e, "initializing", contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
			lifecycle := s.(contract.ActivationScope)
			if err := lifecycle.OnRetire(func(context.Context) error { count.Add(1); close(early); return nil }); err != nil {
				return err
			}
			close(started)
			<-ctx.Done()
			<-early
			if err := lifecycle.OnRetire(func(ctx context.Context) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				count.Add(1)
				close(late)
				return nil
			}); err != nil {
				return err
			}
			// The notification must not wait for Start to return.
			<-late
			return s.Defer(func(context.Context) error {
				if count.Load() != 2 {
					return errors.New("retirement registration lost")
				}
				return nil
			})
		}})
		<-started
		if err := i.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if count.Load() != 2 {
			t.Fatalf("retirement notifications: %d", count.Load())
		}
	})
}

func TestRetireDoesNotWaitForBlockedStartBeforeGlobalHook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		notified, global, started := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var cleaned atomic.Bool
		e := core.NewWithHooks(core.Hooks{OnRetire: func(contract.Identity) {
			select {
			case <-notified:
			default:
				t.Error("global retirement preceded session cancellation")
			}
			close(global)
		}})
		defer closeEngine(t, e)
		i := add(t, e, "blocked-start", contract.Definition{Start: func(_ context.Context, s contract.Scope) error {
			if err := s.Defer(func(context.Context) error { cleaned.Store(true); return nil }); err != nil {
				return err
			}
			if err := s.(contract.ActivationScope).OnRetire(func(context.Context) error { close(notified); return nil }); err != nil {
				return err
			}
			close(started)
			// A Host global hook can unblock initialization by removing children.
			<-global
			return nil
		}})
		<-started
		if err := i.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !cleaned.Load() {
			t.Fatal("initialization resources were not cleaned")
		}
	})
}

type ownedResource struct {
	closes atomic.Int32
	err    error
}

func (r *ownedResource) Close() error { r.closes.Add(1); return r.err }

func TestResourceOwnershipFailureClosesResource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		var scope contract.Scope
		i := add(t, e, "resource-owner", contract.Definition{Start: func(_ context.Context, s contract.Scope) error { scope = s; return nil }})
		if err := i.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := i.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		closeErr := errors.New("resource close failed")
		resource := &ownedResource{err: closeErr}
		owned, err := contract.Own(scope, resource)
		var coded *contract.Error
		if owned != nil || resource.closes.Load() != 1 || !errors.Is(err, closeErr) || !errors.As(err, &coded) || coded.Code != contract.NotReady {
			t.Fatalf("failed ownership leaked resource or cause: owned=%v closes=%d err=%v", owned, resource.closes.Load(), err)
		}
	})
}

func TestLifecycleFailuresRemainObservable(t *testing.T) {
	for _, stage := range []string{"managed work", "publication", "retirement"} {
		for _, panics := range []bool{false, true} {
			name := stage + "/error"
			if panics {
				name = stage + "/panic"
			}
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					e := core.New()
					cause, cleanupErr := errors.New("lifecycle cause"), errors.New("cleanup cause")
					release := make(chan struct{})
					var cleaned, laterRetire atomic.Bool
					fail := func(context.Context) error {
						<-release
						if panics {
							panic(cause)
						}
						return cause
					}
					i := add(t, e, "failure", contract.Definition{Start: func(_ context.Context, s contract.Scope) error {
						if err := s.Defer(func(context.Context) error { cleaned.Store(true); return cleanupErr }); err != nil {
							return err
						}
						lifecycle := s.(contract.ActivationScope)
						switch stage {
						case "managed work":
							return contract.Go(s, fail)
						case "publication":
							if err := contract.Go(s, func(context.Context) error { return errors.New("work ran despite failed acknowledgment") }); err != nil {
								return err
							}
							return lifecycle.OnPublish(fail)
						default:
							if err := lifecycle.OnRetire(fail); err != nil {
								return err
							}
							return lifecycle.OnRetire(func(context.Context) error { laterRetire.Store(true); return nil })
						}
					}})
					if stage != "publication" {
						if err := i.Wait(t.Context()); err != nil {
							t.Fatal(err)
						}
						close(release)
						if stage == "retirement" {
							if err := i.Stop(t.Context()); !errors.Is(err, cause) || !errors.Is(err, cleanupErr) {
								t.Fatalf("retirement failure lost: %v", err)
							}
						} else {
							synctest.Wait()
						}
					} else {
						close(release)
					}
					if stage != "retirement" {
						if err := i.Wait(t.Context()); !errors.Is(err, cause) || !errors.Is(err, cleanupErr) {
							t.Fatalf("activation failure lost: %v", err)
						}
						if i.Status().State != contract.Failed {
							t.Fatal("failed work did not fail activation")
						}
					} else if !laterRetire.Load() {
						t.Fatal("retirement failure skipped later notifications")
					}
					if !cleaned.Load() || !strings.Contains(i.Status().Error, stage) {
						t.Fatalf("cleanup or failure attribution lost: %+v", i.Status())
					}
					if err := e.Close(t.Context()); !errors.Is(err, cause) || !errors.Is(err, cleanupErr) {
						t.Fatalf("close failure lost: %v", err)
					}
				})
			})
		}
	}
}

func TestManagedWorkCancellationIsNormalRetirement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		started := make(chan struct{})
		i := add(t, e, "worker", contract.Definition{Start: func(_ context.Context, s contract.Scope) error {
			return contract.Go(s, func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			})
		}})
		if err := i.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		<-started
		if err := i.Stop(t.Context()); err != nil {
			t.Fatalf("ordinary cancellation became activation failure: %v", err)
		}
		if status := i.Status(); status.State != contract.Stopped || status.Error != "" {
			t.Fatalf("normal retirement: %+v", status)
		}
		if err := e.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}
