package access_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/access"
	"github.com/Cyenoch/cordis/contract"
)

func TestDiagnosticDefaultsNeverEchoSecretsOrPayload(t *testing.T) {
	t.Parallel()
	var record access.Diagnostic
	sink, err := access.NewSink(access.SinkOptions{Write: func(_ context.Context, value access.Diagnostic) error {
		record = value
		return nil
	}, MaxBytes: 512})
	if err != nil {
		t.Fatal(err)
	}
	secret := "bearer-super-secret"
	owner := identity("first")
	owner.Entry = "/Users/private/name"
	if err := sink.Emit(t.Context(), access.DiagnosticInput{
		Owner: owner, Stage: "load", Code: contract.PluginFailed,
		Err: errors.New(secret), Payload: json.RawMessage(`{"token":"` + secret + `"}`), Summary: secret,
	}); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "/Users") {
		t.Fatalf("secret diagnostic: %s", encoded)
	}
	if len(encoded) > 512 {
		t.Fatalf("diagnostic bytes = %d", len(encoded))
	}
	public := access.PublicError(contract.Fail(contract.Denied, secret))
	if strings.Contains(public.Error(), secret) {
		t.Fatal("public error leaked secret")
	}
	var projected *contract.Error
	if !errors.As(public, &projected) || projected.Code != contract.Denied {
		t.Fatal("error code lost")
	}
}

func TestDiagnosticRedactionIsBoundedAndFailuresPropagate(t *testing.T) {
	t.Parallel()
	written := false
	redactFailure := errors.New("redactor unavailable")
	sink, err := access.NewSink(access.SinkOptions{
		Write:  func(context.Context, access.Diagnostic) error { written = true; return nil },
		Redact: func(context.Context, access.DiagnosticInput) (string, error) { return "", redactFailure },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Emit(t.Context(), access.DiagnosticInput{}); !errors.Is(err, redactFailure) {
		t.Fatalf("redaction failure lost: %v", err)
	}
	if written {
		t.Fatal("failed redaction emitted record")
	}
	writeFailure := errors.New("sink unavailable")
	sink, err = access.NewSink(access.SinkOptions{
		MaxBytes: 512,
		Write: func(_ context.Context, record access.Diagnostic) error {
			encoded, err := json.Marshal(record)
			if err != nil {
				return err
			}
			if len(encoded) > 512 {
				t.Fatalf("redacted record exceeds limit: %d", len(encoded))
			}
			return writeFailure
		},
		Redact: func(context.Context, access.DiagnosticInput) (string, error) {
			return strings.Repeat("界\n", 10000), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Emit(t.Context(), access.DiagnosticInput{}); !errors.Is(err, writeFailure) {
		t.Fatalf("writer failure lost: %v", err)
	}
}

func TestDiagnosticMaximumLabelsFitEncodedBudget(t *testing.T) {
	t.Parallel()
	for _, budget := range []int{512, 768} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			label := strings.Repeat("a", 64)
			owner := contract.Identity{Package: label, Entry: label, Activation: label, Digest: label}
			var record access.Diagnostic
			sink, err := access.NewSink(access.SinkOptions{
				MaxBytes: budget,
				Write: func(_ context.Context, output access.Diagnostic) error {
					record = output
					return nil
				},
				Redact: func(context.Context, access.DiagnosticInput) (string, error) {
					return strings.Repeat("\x00", budget), nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := sink.Emit(t.Context(), access.DiagnosticInput{
				Owner: owner, Call: label, Stage: label, Code: contract.Invalid,
			}); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if len(encoded) > budget || record.Owner.Activation != label || record.Stage != label {
				t.Fatalf("diagnostic exceeds budget or lost correlation: %d bytes, %#v", len(encoded), record)
			}
			if budget == 512 && (record.Owner.Package != "" || record.Call != "") {
				t.Fatal("maximum labels did not trigger compact fallback")
			}
		})
	}
}

func TestPublicErrorProjectsWithoutSink(t *testing.T) {
	t.Parallel()
	secret := errors.New("host-only-secret")
	for _, test := range []struct {
		name string
		err  error
		code contract.Code
	}{
		{name: "host", err: secret, code: contract.Internal},
		{name: "denied", err: fmt.Errorf("%w: %w", contract.Fail(contract.Denied, "private"), secret), code: contract.Denied},
		{name: "unknown", err: contract.Fail("private-code", "private"), code: contract.Internal},
		{name: "canceled", err: fmt.Errorf("%w: %w", context.Canceled, secret), code: contract.Canceled},
		{name: "timeout", err: fmt.Errorf("%w: %w", context.DeadlineExceeded, secret), code: contract.Timeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			public := access.PublicError(test.err)
			requireAccessCode(t, public, test.code)
			if strings.Contains(public.Error(), "private") || strings.Contains(public.Error(), secret.Error()) || errors.Is(public, secret) {
				t.Fatalf("public error exposed host details: %v", public)
			}
		})
	}
	if access.PublicError(nil) != nil {
		t.Fatal("successful result projected as failure")
	}
}
