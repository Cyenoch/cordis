package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func TestMissingEntryControlsReturnNotReady(t *testing.T) {
	e := core.New()
	defer closeEngine(t, e)
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{name: "stop", run: func() error { _, err := e.Stop(t.Context(), "missing"); return err }},
		{name: "disable", run: func() error { _, err := e.Disable(t.Context(), "missing"); return err }},
		{name: "resume", run: func() error { _, err := e.Resume("missing"); return err }},
		{name: "remove", run: func() error { return e.Remove(t.Context(), "missing") }},
		{name: "configure", run: func() error { _, err := e.Configure("missing", nil, nil); return err }},
		{name: "supervise", run: func() error { return e.Supervise(t.Context(), "missing", 0, 0) }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			var coded *contract.Error
			if err := operation.run(); !errors.As(err, &coded) || coded.Code != contract.NotReady {
				t.Fatalf("missing entry has wrong contract: %v", err)
			}
		})
	}
}

func TestStopAndResumeSubmitWithoutLifecycleWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		cleanup, release := make(chan struct{}), make(chan struct{})
		p := add(t, e, "provider", contract.Definition{Provides: []contract.Service{service}, Start: func(_ context.Context, s contract.Scope) error {
			if err := s.Defer(func(context.Context) error { close(cleanup); <-release; return nil }); err != nil {
				return err
			}
			return provider(`1`).Start(s.Context(), s)
		}})
		if err := p.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		wait, err := e.Stop(t.Context(), "provider")
		if err != nil {
			t.Fatal(err)
		}
		status := p.Status()
		if status.State != contract.Quiescing || !status.Enabled || !status.TemporaryStopped || e.IsActive(status.Identity) {
			t.Fatalf("stop intent was not submitted synchronously: %+v", status)
		}
		<-cleanup
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		if err := wait(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled cleanup wait: %v", err)
		}
		if _, err := e.Resume("provider"); err == nil {
			t.Fatal("resume crossed pending cleanup")
		}
		unrelated := add(t, e, "unrelated", definitionNoop())
		if err := unrelated.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		close(release)
		if err := wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		consumer := add(t, e, "consumer", contract.Definition{Requires: []contract.Requirement{requirement}, Start: definitionNoop().Start})
		if err := consumer.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		resumed, err := e.Resume("consumer")
		if err != nil || resumed != consumer {
			t.Fatalf("resume submission: %v %v", resumed, err)
		}
		synctest.Wait()
		if s := resumed.Status(); s.State != contract.Waiting || !s.Enabled || s.TemporaryStopped {
			t.Fatalf("resume waited or lost enable intent: %+v", s)
		}
		replacement := add(t, e, "replacement", provider(`2`))
		if err := replacement.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := resumed.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestStopWaiterRetainsOriginalCleanupResultAfterResume(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		failure := errors.New("first activation cleanup failed")
		first := true
		i := add(t, e, "entry", contract.Definition{Start: func(_ context.Context, s contract.Scope) error {
			if !first {
				return nil
			}
			first = false
			return s.Defer(func(context.Context) error { return failure })
		}})
		if err := i.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		wait, err := e.Stop(t.Context(), "entry")
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		resumed, err := e.Resume("entry")
		if err != nil {
			t.Fatal(err)
		}
		if err := resumed.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := wait(t.Context()); !errors.Is(err, failure) {
			t.Fatalf("old cleanup result was overwritten by resume: %v", err)
		}
		closeEngine(t, e)
	})
}

func TestWaitingDiagnosticsRespectRealmVisibility(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		for _, name := range []string{"visible", "isolated", "other-major"} {
			var isolated []string
			if name == "isolated" {
				isolated = []string{service.Key()}
			}
			if err := e.ConfigureRealm(name, "", isolated); err != nil {
				t.Fatal(err)
			}
		}
		p := add(t, e, "provider", provider(`1`))
		if err := p.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		req := requirement
		req.MinMinor, req.MaxMinor = 3, 5
		for _, name := range []string{"visible", "isolated", "other-major"} {
			requested := req
			if name == "other-major" {
				requested.Major = 2
			}
			i, err := e.Add(contract.Entry{ID: name, Realm: name, Enabled: true}, contract.Definition{
				Requires: []contract.Requirement{requested}, Start: definitionNoop().Start,
			}, contract.Identity{})
			if err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			s := i.Status()
			if len(s.Waiting) != 1 || s.Waiting[0].Requirement == nil || *s.Waiting[0].Requirement != requested || s.Waiting[0].Kind != "service" {
				t.Fatalf("requirement diagnostic: %+v", s)
			}
			w := s.Waiting[0]
			if name == "isolated" {
				if w.Reason != "missing" || len(w.Visible) != 0 {
					t.Fatalf("isolated ancestor leaked: %+v", w)
				}
			} else if w.Reason != "incompatible" || len(w.Visible) != 1 || w.Visible[0] != service {
				t.Fatalf("visible incompatible provider omitted: %+v", w)
			}
		}
		parent := add(t, e, "parent", contract.Definition{Requires: []contract.Requirement{{Name: "missing"}}, Start: definitionNoop().Start})
		child, err := e.Add(contract.Entry{ID: "child", Parent: "parent", Enabled: true}, definitionNoop(), contract.Identity{})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if s := child.Status(); len(s.Waiting) != 1 || s.Waiting[0].Kind != "parent" || s.Waiting[0].Parent != parent.Status().Identity.Entry {
			t.Fatalf("parent readiness diagnostic: %+v", s)
		}
		// A returned diagnostic must not be able to mutate the definition.
		status := parent.Status()
		status.Waiting[0].Requirement.Name = "changed"
		if parent.Status().Waiting[0].Requirement.Name != "missing" {
			t.Fatal("status exposed mutable requirement")
		}
	})
}

func TestStopSubmissionRejectsSelfControlBeforeRetirement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		defer closeEngine(t, e)
		i := add(t, e, "self", contract.Definition{Provides: []contract.Service{service}, Start: func(_ context.Context, s contract.Scope) error {
			return s.Provide(service, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
				_, err := e.Stop(ctx, "self")
				return nil, err
			})
		}})
		if err := i.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		_, err := e.Call(t.Context(), "", requirement, nil)
		var coded *contract.Error
		if !errors.As(err, &coded) || coded.Code != contract.Reentrant || i.Status().State != contract.Active {
			t.Fatalf("self stop mutated before rejection: %v %+v", err, i.Status())
		}
	})
}

func TestStopWaiterParticipatesInServiceWaitGraph(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := core.New()
		failure := make(chan error, 1)
		cleanupEntered, allowCleanup := make(chan struct{}), make(chan struct{})
		a := add(t, e, "caller", contract.Definition{Provides: []contract.Service{service}, Start: func(_ context.Context, s contract.Scope) error {
			return s.Provide(service, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
				wait, err := e.Stop(ctx, "target")
				if err != nil {
					return nil, err
				}
				return nil, wait(ctx)
			})
		}})
		if err := a.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		b := add(t, e, "target", contract.Definition{Requires: []contract.Requirement{requirement}, Start: func(_ context.Context, s contract.Scope) error {
			return s.Defer(func(ctx context.Context) error {
				close(cleanupEntered)
				<-allowCleanup
				_, err := s.Call(ctx, requirement, nil)
				return err
			})
		}})
		if err := b.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		go func() { _, err := e.Call(t.Context(), "", requirement, nil); failure <- err }()
		<-cleanupEntered
		synctest.Wait()
		close(allowCleanup)
		var coded *contract.Error
		if err := <-failure; !errors.As(err, &coded) || coded.Code != contract.Reentrant {
			t.Fatalf("service/stop cleanup wait cycle was not diagnosed: %v", err)
		}
		if err := e.Close(t.Context()); !errors.As(err, &coded) || coded.Code != contract.Reentrant {
			t.Fatalf("cleanup failure was not retained: %v", err)
		}
	})
}
