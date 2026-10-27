package access_test

import (
	"context"
	"encoding/json"
	"errors"
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
	public := sink.PublicError(contract.Fail(contract.Denied, secret))
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
