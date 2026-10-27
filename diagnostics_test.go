package cordis

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/access"
	"github.com/Cyenoch/cordis/contract"
)

func TestSinkFailurePreservesLocalCauseWithoutLeakingDefaultSummary(t *testing.T) {
	cause, broken := errors.New("secret-token:/private/path"), errors.New("writer unavailable")
	var observed error
	var summary string
	sink, err := access.NewSink(access.SinkOptions{Write: func(_ context.Context, d access.Diagnostic) error { summary = d.Summary; return broken }, Redact: func(_ context.Context, in access.DiagnosticInput) (string, error) {
		observed = in.Err
		return "safe summary", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(Options{Directory: t.TempDir(), Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	i, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "failure"}, contract.Definition{Start: func(context.Context, contract.Scope) error { return cause }})
	if err != nil {
		t.Fatal(err)
	}
	err = i.Wait(t.Context())
	if !errors.Is(err, cause) || !errors.Is(err, broken) || !errors.Is(observed, cause) || summary != "safe summary" {
		t.Fatal(err, observed, summary)
	}
	records := h.DiagnosticErrors()
	if len(records) != 1 || !errors.Is(records[0], cause) || !errors.Is(records[0], broken) {
		t.Fatal(records)
	}
	records[0] = nil
	if h.DiagnosticErrors()[0] == nil {
		t.Fatal("mutable diagnostic failures")
	}
	if err := h.Disable(t.Context(), "failure"); !errors.Is(err, cause) || !errors.Is(err, broken) {
		t.Fatal(err)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	defaultSink, err := access.NewSink(access.SinkOptions{Write: func(_ context.Context, d access.Diagnostic) error {
		if strings.Contains(d.Summary, "secret") {
			t.Fatal("default secret leaked")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := defaultSink.Emit(t.Context(), access.DiagnosticInput{Err: cause, Summary: cause.Error(), Stage: "stderr", Code: contract.PluginFailed}); err != nil {
		t.Fatal(err)
	}
}

type stderrFixtureRuntime struct {
	callback bool
	chunk    string
}

func (r *stderrFixtureRuntime) Start(_ context.Context, launch contract.Launch) (contract.Session, error) {
	r.callback = launch.Stderr != nil
	if launch.Stderr != nil {
		if err := launch.Stderr([]byte(r.chunk)); err != nil {
			return nil, err
		}
	}
	return &fixtureSession{done: make(chan struct{})}, nil
}
func TestHostStderrIsExplicitOptInAndAlwaysUsesSink(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "enabled"}[enabled], func(t *testing.T) {
			h, pkg, _ := lifecycleHost(t)
			runtime := &stderrFixtureRuntime{chunk: "private-token"}
			h.options.Runtimes["wasm"] = runtime
			var summaries []string
			sink, err := access.NewSink(access.SinkOptions{Write: func(_ context.Context, d access.Diagnostic) error {
				summaries = append(summaries, d.Summary)
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			h.options.Sink = sink
			h.options.DiagnosticStderr = enabled
			i, err := h.Enable(t.Context(), contract.Entry{ID: "stderr", Package: pkg.ID, Config: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			if err := i.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
			if runtime.callback != enabled {
				t.Fatal("stderr callback selected without opt-in")
			}
			for _, summary := range summaries {
				if strings.Contains(summary, "private-token") {
					t.Fatal("raw stderr escaped sink default")
				}
			}
		})
	}
}
