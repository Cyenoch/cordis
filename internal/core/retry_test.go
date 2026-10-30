package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func TestRetryKeepsCommittedConfigurationAndSurvivesCanceledWait(t *testing.T) {
	e := core.New()
	defer func() { _ = e.Close(t.Context()) }()
	var starts atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	i := add(t, e, "retry", contract.Definition{Start: func(_ context.Context, s contract.Scope) error {
		if starts.Add(1) == 1 {
			return errors.New("first start fails")
		}
		close(entered)
		<-release
		if string(s.Config()) != "{}" {
			return errors.New("retry changed configuration")
		}
		return nil
	}})
	if err := i.Wait(t.Context()); err == nil {
		t.Fatal("initial failure was hidden")
	}
	first := i.Status()
	wait, err := e.Retry(i, first, true)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("retry wait: %v", err)
	}
	close(release)
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if next := i.Status(); next.Desired != first.Desired+1 || next.Identity == first.Identity || starts.Load() != 2 {
		t.Fatalf("accepted retry was lost or duplicated: %+v starts=%d", next, starts.Load())
	}
	if _, err := e.Retry(i, first, false); err == nil {
		t.Fatal("stale retry restarted active instance")
	}
}

func TestRetryRejectsStaleRevisionAndRegistration(t *testing.T) {
	for _, operation := range []string{"update", "stop", "remove"} {
		t.Run(operation, func(t *testing.T) {
			e := core.New()
			defer func() { _ = e.Close(t.Context()) }()
			i := add(t, e, "entry", contract.Definition{Start: func(context.Context, contract.Scope) error {
				return errors.New("failed")
			}})
			_ = i.Wait(t.Context())
			expected := i.Status()
			switch operation {
			case "update":
				_, _ = i.Update(t.Context(), json.RawMessage(`{"new":true}`))
			case "stop":
				_ = i.Stop(t.Context())
			case "remove":
				_ = e.Remove(t.Context(), "entry")
				replacement := add(t, e, "entry", contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }})
				if err := replacement.Wait(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.Retry(i, expected, false); err == nil {
				t.Fatal("stale retry admitted after " + operation)
			}
		})
	}
}

func TestRetryRequiresCleanupButManualRetryMayAcceptCleanupFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer func() { _ = e.Close(t.Context()) }()
		cleaning, release := make(chan struct{}), make(chan struct{})
		var starts atomic.Int32
		i := add(t, e, "entry", contract.Definition{Start: func(_ context.Context, s contract.Scope) error {
			if starts.Add(1) > 1 {
				return nil
			}
			if err := s.Defer(func(context.Context) error {
				close(cleaning)
				<-release
				return errors.New("cleanup failed")
			}); err != nil {
				return err
			}
			return errors.New("initialization failed")
		}})
		<-cleaning
		if _, err := e.Retry(i, i.Status(), false); err == nil {
			t.Fatal("retry overlapped cleanup")
		}
		close(release)
		_ = i.Wait(t.Context())
		if _, err := e.Retry(i, i.Status(), true); err == nil {
			t.Fatal("automatic recovery ignored cleanup failure")
		}
		wait, err := e.Retry(i, i.Status(), false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAutomaticRetryDoesNotUseHistoricalActivationForGraphFailure(t *testing.T) {
	e := core.New()
	defer func() { _ = e.Close(t.Context()) }()
	aService := contract.Service{Name: "example.a", Major: 1}
	bService := contract.Service{Name: "example.b", Major: 1}
	provide := func(service contract.Service) contract.Definition {
		return contract.Definition{Provides: []contract.Service{service}, Start: func(_ context.Context, s contract.Scope) error {
			return s.Provide(service, func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`null`), nil })
		}}
	}
	helper := add(t, e, "helper", provide(bService))
	if err := helper.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	cleaning, release := make(chan struct{}), make(chan struct{})
	aDefinition := provide(aService)
	aDefinition.Requires = []contract.Requirement{{Name: bService.Name, Major: 1}}
	start := aDefinition.Start
	aDefinition.Start = func(ctx context.Context, scope contract.Scope) error {
		if err := scope.Defer(func(context.Context) error { close(cleaning); <-release; return nil }); err != nil {
			return err
		}
		return start(ctx, scope)
	}
	a := add(t, e, "a", aDefinition)
	if err := a.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	previous := a.Status().Identity
	stopped := make(chan error, 1)
	go func() { stopped <- helper.Stop(t.Context()) }()
	<-cleaning
	bDefinition := provide(bService)
	bDefinition.Requires = []contract.Requirement{{Name: aService.Name, Major: 1}}
	add(t, e, "b", bDefinition)
	close(release)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if err := a.Wait(t.Context()); err == nil {
		t.Fatal("graph cycle was not diagnosed")
	}
	status := a.Status()
	if status.Identity != previous || e.FailedActivation(a) {
		t.Fatalf("historical activation was treated as this graph failure: %+v", status)
	}
	if _, err := e.Retry(a, status, true); err == nil {
		t.Fatal("graph failure admitted an automatic retry")
	}
}

func TestRetryCannotPassAnAcceptedConcurrentConfigurationCommit(t *testing.T) {
	e := core.New()
	defer func() { _ = e.Close(t.Context()) }()
	i := add(t, e, "entry", contract.Definition{Start: func(_ context.Context, scope contract.Scope) error {
		if string(scope.Config()) == "{}" {
			return errors.New("initial failure")
		}
		return nil
	}})
	_ = i.Wait(t.Context())
	stale := i.Status()
	entered, release := make(chan struct{}), make(chan struct{})
	updated := make(chan error, 1)
	go func() {
		wait, err := e.Configure(i, json.RawMessage(`{"new":true}`), func() error { close(entered); <-release; return nil })
		if err == nil {
			_, err = wait(t.Context())
		}
		updated <- err
	}()
	<-entered
	retried := make(chan error, 1)
	go func() { _, err := e.Retry(i, stale, false); retried <- err }()
	close(release)
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	if err := <-retried; err == nil {
		t.Fatal("retry crossed a newer accepted configuration")
	}
	if status := i.Status(); status.Desired != stale.Desired+1 || status.State != contract.Active {
		t.Fatalf("retry replaced the new configuration: %+v", status)
	}
}
