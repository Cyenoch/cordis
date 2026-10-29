package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

type diagnosticSink struct {
	mu      sync.Mutex
	records []slog.Record
	query   func()
}

func (*diagnosticSink) Enabled(context.Context, slog.Level) bool { return true }
func (s *diagnosticSink) Handle(_ context.Context, r slog.Record) error {
	s.query()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r.Clone())
	return nil
}
func (s *diagnosticSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *diagnosticSink) WithGroup(string) slog.Handler      { return s }

func TestDiagnosticsKeepIdentityAndSeparateFailureFromCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &diagnosticSink{}
		e := core.NewWithHooks(core.Hooks{Logger: slog.New(sink)})
		sink.query = func() { e.IsActive(contract.Identity{}) }
		failure := contract.Fail("business_failure", "initialization failed")
		cleanup := errors.New("cleanup failed")
		i := add(t, e, "failed", contract.Definition{Start: func(_ context.Context, s contract.Scope) error {
			if err := s.Defer(func(context.Context) error { return cleanup }); err != nil {
				return err
			}
			return failure
		}})
		if err := i.Wait(t.Context()); !errors.Is(err, failure) || !errors.Is(err, cleanup) {
			t.Fatalf("failure chain: %v", err)
		}
		status := i.Status()
		if status.Identity.Activation == "" || status.Outcome.FailureCode != contract.PluginFailed || status.Outcome.CleanupCode != contract.PluginFailed {
			t.Fatalf("terminal identity/outcome: %+v", status)
		}
		if err := e.Close(t.Context()); !errors.Is(err, cleanup) {
			t.Fatal(err)
		}
		synctest.Wait()
		sink.mu.Lock()
		defer sink.mu.Unlock()
		var failed, active bool
		for _, record := range sink.records {
			record.Attrs(func(attr slog.Attr) bool {
				if attr.Key == "stage" {
					failed = failed || attr.Value.String() == "failed"
					active = active || attr.Value.String() == "active"
				}
				return true
			})
		}
		if !failed || active {
			t.Fatalf("startup failure logged wrong publication: failed=%v active=%v", failed, active)
		}
	})
}

func TestCallDiagnosticsAttributePanicsWithoutBusinessPayload(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "explicit-stack"}[capture], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sink := &diagnosticSink{}
				e := core.NewWithHooks(core.Hooks{Logger: slog.New(sink), CapturePanic: capture})
				sink.query = func() { e.IsActive(contract.Identity{}) }
				defer closeEngine(t, e)
				var instances []contract.Instance
				for _, name := range []string{"first", "second"} {
					provided := contract.Service{Name: name, Major: 1}
					i, err := e.Add(contract.Entry{ID: name, Package: "same-package", Enabled: true}, contract.Definition{
						Provides: []contract.Service{provided},
						Start: func(_ context.Context, scope contract.Scope) error {
							return scope.Provide(provided, func(context.Context, json.RawMessage) (json.RawMessage, error) {
								if name == "first" {
									panic("broken callback")
								}
								return json.RawMessage(`true`), nil
							})
						},
					}, contract.Identity{Package: "same-package"})
					if err != nil {
						t.Fatal(err)
					}
					if err := i.Wait(t.Context()); err != nil {
						t.Fatal(err)
					}
					instances = append(instances, i)
				}
				if _, err := e.Call(t.Context(), contract.Requirement{Name: "first", Major: 1}, json.RawMessage(`"private-payload"`)); err == nil {
					t.Fatal("panic returned success")
				}
				if _, err := e.Call(t.Context(), contract.Requirement{Name: "second", Major: 1}, nil); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				sink.mu.Lock()
				defer sink.mu.Unlock()
				found := false
				for _, r := range sink.records {
					if r.Message != "plugin call failed" {
						continue
					}
					values := map[string]string{}
					r.Attrs(func(a slog.Attr) bool { values[a.Key] = a.Value.String(); return true })
					found = true
					if values["activation"] != instances[0].Status().Identity.Activation || values["errorCode"] != string(contract.Panicked) {
						t.Fatalf("wrong failure owner or classification: %v", values)
					}
					if (values["stack"] != "") != capture {
						t.Fatalf("stack opt-in: %v", values)
					}
					for _, value := range values {
						if strings.Contains(value, "private-payload") {
							t.Fatal("business payload logged")
						}
					}
				}
				if !found || instances[1].Status().State != contract.Active {
					t.Fatal("missing failure log or damaged sibling")
				}
			})
		})
	}
}
