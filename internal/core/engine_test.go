package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

var service = contract.Service{Name: "store", Major: 1, Minor: 2}
var requirement = contract.Requirement{Name: "store", Major: 1, MinMinor: 1, MaxMinor: 3}

func add(t *testing.T, e *core.Engine, id string, def contract.Definition) contract.Instance {
	t.Helper()
	i, err := e.Add(contract.Entry{ID: id, Enabled: true, Config: json.RawMessage(`{}`)}, def, contract.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func provider(value string) contract.Definition {
	return contract.Definition{Provides: []contract.Service{service}, Start: func(ctx context.Context, s contract.Scope) error {
		return s.Provide(service, func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(value), nil })
	}}
}
func closeEngine(t *testing.T, e *core.Engine) {
	t.Helper()
	if err := e.Close(t.Context()); err != nil {
		t.Error(err)
	}
}
func TestDependencyReadinessAndCanceledWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		var starts atomic.Int32
		consumer := contract.Definition{Requires: []contract.Requirement{requirement}, Start: func(context.Context, contract.Scope) error { starts.Add(1); return nil }}
		i := add(t, e, "consumer", consumer)
		canceled := add(t, e, "canceled", consumer)
		synctest.Wait()
		if starts.Load() != 0 || len(i.Status().WaitingFor) != 1 {
			t.Fatal("consumer initialized before provider readiness")
		}
		if err := canceled.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		p := add(t, e, "provider", provider(`1`))
		if err := p.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := i.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if starts.Load() != 1 || canceled.Status().State != contract.Stopped {
			t.Fatal("canceled wait was revived")
		}
	})
}
func TestProviderRetirementCancelsBeforeConsumerCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		entered := make(chan struct{})
		var cleanupCalled atomic.Bool
		var providerClosed atomic.Bool
		p := add(t, e, "provider", contract.Definition{Provides: []contract.Service{service}, Start: func(ctx context.Context, s contract.Scope) error {
			if err := s.Defer(func(context.Context) error {
				if !cleanupCalled.Load() {
					return errors.New("provider released before consumer")
				}
				providerClosed.Store(true)
				return nil
			}); err != nil {
				return err
			}
			return s.Provide(service, func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
				if string(input) == `"work"` {
					close(entered)
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if ctx.Err() != nil {
					return nil, errors.New("cleanup context was canceled")
				}
				cleanupCalled.Store(true)
				return json.RawMessage(`0`), nil
			})
		}})
		if err := p.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		c := add(t, e, "consumer", contract.Definition{Requires: []contract.Requirement{requirement}, Start: func(ctx context.Context, s contract.Scope) error {
			return s.Defer(func(ctx context.Context) error {
				_, err := s.Call(ctx, requirement, json.RawMessage(`"cleanup"`))
				return err
			})
		}})
		if err := c.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		finished := make(chan error, 1)
		go func() { _, err := e.Call(t.Context(), "", requirement, json.RawMessage(`"work"`)); finished <- err }()
		<-entered
		if err := p.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := <-finished; !errors.Is(err, context.Canceled) {
			t.Fatalf("work cancellation: %v", err)
		}
		if !cleanupCalled.Load() || !providerClosed.Load() || c.Status().State != contract.Waiting {
			t.Fatal("retirement order or cleanup binding violated")
		}
	})
}
func TestFailureAggregatesCleanupAndExplicitRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		startErr, cleanupErr := errors.New("start failed"), errors.New("cleanup failed")
		var attempts atomic.Int32
		var order []int
		i := add(t, e, "broken", contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
			if attempts.Add(1) > 1 {
				return nil
			}
			if err := s.Defer(func(context.Context) error { order = append(order, 1); return nil }); err != nil {
				return err
			}
			if err := s.Defer(func(context.Context) error { order = append(order, 2); return cleanupErr }); err != nil {
				return err
			}
			return startErr
		}})
		err := i.Wait(t.Context())
		if !errors.Is(err, startErr) || !errors.Is(err, cleanupErr) {
			t.Fatalf("lost errors: %v", err)
		}
		if fmt.Sprint(order) != "[2 1]" {
			t.Fatalf("cleanup order: %v", order)
		}
		add(t, e, "unrelated", provider(`1`))
		synctest.Wait()
		if attempts.Load() != 1 {
			t.Fatal("failed instance automatically retried")
		}
		if err := i.Retry(t.Context()); err != nil {
			t.Fatal(err)
		}
		closeEngine(t, e)
	})
}
func TestConfigurationSupersedesLateStartAndRejectsInvalid(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		entered, release := make(chan struct{}), make(chan struct{})
		i, err := e.Add(contract.Entry{ID: "config", Enabled: true, Config: json.RawMessage(`1`)}, contract.Definition{Validate: func(config json.RawMessage) error {
			if string(config) == `0` {
				return errors.New("invalid config")
			}
			return nil
		}, Start: func(ctx context.Context, s contract.Scope) error {
			if string(s.Config()) == `1` {
				close(entered)
				<-release
			}
			return nil
		}}, contract.Identity{})
		if err != nil {
			t.Fatal(err)
		}
		<-entered
		result2 := make(chan contract.UpdateResult, 1)
		err2 := make(chan error, 1)
		go func() { r, err := i.Update(t.Context(), json.RawMessage(`2`)); result2 <- r; err2 <- err }()
		synctest.Wait()
		result3 := make(chan contract.UpdateResult, 1)
		err3 := make(chan error, 1)
		go func() { r, err := i.Update(t.Context(), json.RawMessage(`3`)); result3 <- r; err3 <- err }()
		synctest.Wait()
		if r := <-result2; !r.Superseded || r.Applied {
			t.Fatalf("revision 2: %+v", r)
		}
		if err := <-err2; err != nil {
			t.Fatal(err)
		}
		if i.Status().Applied != 0 {
			t.Fatal("old initialization committed")
		}
		close(release)
		if r := <-result3; !r.Applied || r.Revision != 3 {
			t.Fatalf("revision 3: %+v", r)
		}
		if err := <-err3; err != nil {
			t.Fatal(err)
		}
		before := i.Status()
		if _, err := i.Update(t.Context(), json.RawMessage(`0`)); err == nil {
			t.Fatal("invalid config accepted")
		}
		after := i.Status()
		if before.Desired != after.Desired || before.Applied != after.Applied || before.Identity != after.Identity {
			t.Fatal("invalid update changed activation")
		}
	})
}
func TestRealmIsolationAndParentOwnership(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		for _, realm := range []string{"tenant-a", "tenant-b"} {
			if err := e.ConfigureRealm(realm, "", []string{service.Key()}); err != nil {
				t.Fatal(err)
			}
		}
		root := add(t, e, "root", provider(`0`))
		if err := root.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Call(t.Context(), "tenant-a", requirement, nil); err == nil {
			t.Fatal("isolated realm inherited root")
		}
		for n, realm := range []string{"tenant-a", "tenant-b"} {
			i, err := e.Add(contract.Entry{ID: realm, Realm: realm, Enabled: true}, provider(fmt.Sprint(n+1)), contract.Identity{})
			if err != nil {
				t.Fatal(err)
			}
			if err := i.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		raw, err := e.Call(t.Context(), "tenant-b", requirement, nil)
		if err != nil || string(raw) != `2` {
			t.Fatalf("realm call: %s %v", raw, err)
		}
		parent := add(t, e, "parent", contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }})
		if err := parent.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		child, err := e.AddChild("parent", contract.Entry{ID: "optional", Enabled: true}, contract.Definition{Requires: []contract.Requirement{{Name: "missing"}}, Start: func(context.Context, contract.Scope) error { return errors.New("must not start") }}, contract.Identity{})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if parent.Status().State != contract.Active || child.Status().State != contract.Waiting {
			t.Fatal("optional child stopped parent")
		}
		if err := parent.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if child.Status().State != contract.Stopped {
			t.Fatal("waiting child survived parent")
		}
	})
}
func TestBlockedBuiltinRemainsQuiescing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		entered, release := make(chan struct{}), make(chan struct{})
		i := add(t, e, "builtin", contract.Definition{Start: func(context.Context, contract.Scope) error { close(entered); <-release; return nil }})
		<-entered
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := i.Stop(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked cleanup: %v", err)
		}
		if i.Status().State != contract.Quiescing {
			t.Fatal("uncooperative code falsely released")
		}
		close(release)
		if err := i.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		closeEngine(t, e)
	})
}
func TestDuplicateProviderAndDependencyCycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		p := add(t, e, "first", provider(`1`))
		if err := p.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		duplicate := add(t, e, "duplicate", provider(`2`))
		if err := duplicate.Wait(t.Context()); err == nil {
			t.Fatal("duplicate provider accepted")
		}
		aService := contract.Service{Name: "a", Major: 1}
		bService := contract.Service{Name: "b", Major: 1}
		a := add(t, e, "a", contract.Definition{Requires: []contract.Requirement{{Name: "b", Major: 1}}, Provides: []contract.Service{aService}, Start: func(context.Context, contract.Scope) error { t.Error("cycle started"); return nil }})
		b := add(t, e, "b", contract.Definition{Requires: []contract.Requirement{{Name: "a", Major: 1}}, Provides: []contract.Service{bService}, Start: func(context.Context, contract.Scope) error { t.Error("cycle started"); return nil }})
		for _, i := range []contract.Instance{a, b} {
			err := i.Wait(t.Context())
			var coded *contract.Error
			if !errors.As(err, &coded) || coded.Code != contract.Reentrant {
				t.Fatalf("dependency cycle: %v", err)
			}
		}
		if err := e.Close(t.Context()); err == nil {
			t.Fatal("close hid failed activation")
		}
	})
}
func TestBindingReplacementCannotCommitOldInitialization(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		p := add(t, e, "provider", provider(`1`))
		if err := p.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		var starts atomic.Int32
		c := add(t, e, "consumer", contract.Definition{Requires: []contract.Requirement{requirement}, Start: func(ctx context.Context, s contract.Scope) error {
			if starts.Add(1) == 1 {
				close(entered)
				<-release
			}
			raw, err := s.Call(ctx, requirement, nil)
			if err != nil {
				return err
			}
			if string(raw) != `2` {
				return errors.New("stale binding")
			}
			return nil
		}})
		<-entered
		stopped := make(chan error, 1)
		go func() { stopped <- p.Stop(t.Context()) }()
		synctest.Wait()
		close(release)
		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
		replacement := add(t, e, "replacement", provider(`2`))
		if err := replacement.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := c.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if starts.Load() != 2 {
			t.Fatalf("activation starts: %d", starts.Load())
		}
	})
}
func TestPublicationHooksShareActiveGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var e *core.Engine
		var mu sync.Mutex
		var records []contract.Identity
		e = core.NewWithHooks(core.Hooks{BeforePublish: func(identity contract.Identity) error {
			if e.IsActive(identity) {
				return errors.New("premature active")
			}
			mu.Lock()
			records = append(records, identity)
			mu.Unlock()
			return nil
		}, OnRetire: func(identity contract.Identity) {
			if e.IsActive(identity) {
				t.Error("retiring identity active")
			}
		}})
		i := add(t, e, "hook", contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }})
		if err := i.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		identity := records[0]
		mu.Unlock()
		if !e.IsActive(identity) {
			t.Fatal("published record hidden")
		}
		if err := i.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if e.IsActive(identity) {
			t.Fatal("retired record visible")
		}
		closeEngine(t, e)
	})
}

func TestTransitiveCleanupAndIndependentInstances(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		var mu sync.Mutex
		var order []string
		record := func(name string) contract.Cleanup {
			return func(context.Context) error { mu.Lock(); order = append(order, name); mu.Unlock(); return nil }
		}
		a := add(t, e, "a", contract.Definition{Provides: []contract.Service{service}, Start: func(ctx context.Context, s contract.Scope) error {
			if err := s.Defer(record("a")); err != nil {
				return err
			}
			return s.Provide(service, func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`1`), nil })
		}})
		if err := a.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		middle := contract.Service{Name: "middle", Major: 1}
		middleReq := contract.Requirement{Name: "middle", Major: 1}
		b := add(t, e, "b", contract.Definition{Requires: []contract.Requirement{requirement}, Provides: []contract.Service{middle}, Start: func(ctx context.Context, s contract.Scope) error {
			if err := s.Defer(record("b")); err != nil {
				return err
			}
			return s.Provide(middle, func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil })
		}})
		if err := b.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		c := add(t, e, "c", contract.Definition{Requires: []contract.Requirement{middleReq}, Start: func(ctx context.Context, s contract.Scope) error { return s.Defer(record("c")) }})
		if err := c.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		independent := add(t, e, "independent", contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }})
		if err := independent.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := a.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		got := fmt.Sprint(order)
		mu.Unlock()
		if got != "[c b a]" {
			t.Fatalf("transitive order: %s", got)
		}
		if independent.Status().State != contract.Active {
			t.Fatal("unrelated instance retired")
		}
	})
}

func TestRemoveEnableAndStaleSessionFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		definition := contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }}
		i := add(t, e, "entry", definition)
		if err := i.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		old := i.Status().Identity
		if err := i.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := e.Enable(t.Context(), "entry"); err != nil {
			t.Fatal(err)
		}
		if i.Status().Identity.Activation == old.Activation {
			t.Fatal("activation identity reused")
		}
		if err := e.FailActivation(old, errors.New("late worker exit")); err == nil {
			t.Fatal("late session failure accepted")
		}
		if i.Status().State != contract.Active {
			t.Fatal("late session failed new activation")
		}
		if err := e.Remove(t.Context(), "entry"); err != nil {
			t.Fatal(err)
		}
		next := add(t, e, "entry", definition)
		if err := next.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := i.Stop(t.Context()); err == nil {
			t.Fatal("old entry handle affected replacement")
		}
		if next.Status().State != contract.Active {
			t.Fatal("replacement was stopped")
		}
	})
}

func TestFailedPublicationRollsBackPreparedOwnership(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("publish failed")
		var retired atomic.Int32
		e := core.NewWithHooks(core.Hooks{BeforePublish: func(contract.Identity) error { return failure }, OnRetire: func(contract.Identity) { retired.Add(1) }})
		i := add(t, e, "failed-publish", definitionNoop())
		if err := i.Wait(t.Context()); !errors.Is(err, failure) {
			t.Fatalf("publication error: %v", err)
		}
		if retired.Load() != 1 {
			t.Fatal("prepared external ownership not revoked")
		}
		if err := e.Close(t.Context()); !errors.Is(err, failure) {
			t.Fatalf("close error: %v", err)
		}
	})
}

func definitionNoop() contract.Definition {
	return contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }}
}

func TestSameDefinitionInstancesOwnIndependentConfiguration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		var stopped atomic.Int32
		definition := contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
			if string(s.Config()) != `1` && string(s.Config()) != `2` {
				return errors.New("unexpected instance config")
			}
			return s.Defer(func(context.Context) error { stopped.Add(1); return nil })
		}}
		first, err := e.Add(contract.Entry{ID: "first", Enabled: true, Config: json.RawMessage(`1`)}, definition, contract.Identity{})
		if err != nil {
			t.Fatal(err)
		}
		second, err := e.Add(contract.Entry{ID: "second", Enabled: true, Config: json.RawMessage(`2`)}, definition, contract.Identity{})
		if err != nil {
			t.Fatal(err)
		}
		if err := first.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := second.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := first.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if stopped.Load() != 1 || second.Status().State != contract.Active {
			t.Fatal("instance resource ownership leaked")
		}
	})
}

func TestIncompatibleMinorWaitAndCanceledUpdateWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		incompatible := requirement
		incompatible.MinMinor = 3
		incompatible.MaxMinor = 4
		var started atomic.Int32
		consumer := add(t, e, "consumer", contract.Definition{Requires: []contract.Requirement{incompatible}, Start: func(context.Context, contract.Scope) error { started.Add(1); return nil }})
		p := add(t, e, "provider", provider(`1`))
		if err := p.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if started.Load() != 0 || consumer.Status().State != contract.Waiting {
			t.Fatal("incompatible minor initialized consumer")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		result, err := consumer.Update(ctx, json.RawMessage(`2`))
		if !errors.Is(err, context.Canceled) || result.Applied || result.Superseded {
			t.Fatalf("canceled update wait: %+v %v", result, err)
		}
		if consumer.Status().Desired != 2 || consumer.Status().Applied != 0 {
			t.Fatal("canceled wait incorrectly rolled back desired or applied revision")
		}
	})
}
