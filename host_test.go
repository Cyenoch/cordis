package cordis_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

func TestCloseDrainsFailedBuiltinCleanupAndReleasesStore(t *testing.T) {
	directory := t.TempDir()
	host, err := cordis.Open(cordis.Options{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	cleanupErr := errors.New("builtin cleanup failed")
	cleaned := make(chan struct{})
	defer func() {
		if err := host.Close(t.Context()); err != nil && !errors.Is(err, cleanupErr) {
			t.Error(err)
		}
	}()
	instance, err := host.AddBuiltin(t.Context(), contract.Entry{ID: "cleanup", Config: json.RawMessage(`{}`)}, contract.Definition{
		Start: func(_ context.Context, scope contract.Scope) error {
			return scope.Defer(func(context.Context) error {
				close(cleaned)
				return cleanupErr
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := host.Close(t.Context()); !errors.Is(err, cleanupErr) {
		t.Fatalf("Close lost cleanup failure: %v", err)
	}
	select {
	case <-host.Done():
	default:
		t.Fatal("cleanup error prevented reporting completed close")
	}
	if status := instance.Status(); status.Identity.Activation == "" || status.Outcome.Reason != contract.ExitStopped || status.Outcome.CleanupCode == "" || status.Outcome.FailureCode != "" {
		t.Fatalf("cleanup failure conflated with stop cause: %+v", status)
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("Close returned before builtin cleanup drained")
	}
	// Reopening the same directory proves that a drained cleanup failure did
	// not retain the package catalog's exclusive writer lock.
	reopened, err := packagehost.Open(directory, packagehost.TrustPolicy{})
	if err != nil {
		t.Fatalf("drained Host.Close retained store lock: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltinCallRejectsSuccessReturnedAfterCancellation(t *testing.T) {
	directory := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		host, err := cordis.Open(cordis.Options{Directory: directory})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := host.Close(t.Context()); err != nil {
				t.Error(err)
			}
		}()
		service := contract.Service{Name: "builtin.late", Major: 1}
		entered := make(chan struct{})
		release := make(chan struct{})
		instance, err := host.AddBuiltin(t.Context(), contract.Entry{ID: "late", Config: json.RawMessage(`{}`)}, contract.Definition{
			Provides: []contract.Service{service},
			Start: func(_ context.Context, scope contract.Scope) error {
				return scope.Provide(service, func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
					if string(input) == `"late"` {
						close(entered)
						<-ctx.Done()
						<-release
					}
					return json.RawMessage(`{"success":true}`), nil
				})
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := instance.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		required := contract.Requirement{Name: service.Name, Major: service.Major}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		type result struct {
			output json.RawMessage
			err    error
		}
		done := make(chan result, 1)
		go func() {
			output, err := host.Call(ctx, required, json.RawMessage(`"late"`))
			done <- result{output: output, err: err}
		}()
		<-entered
		cancel()
		synctest.Wait()
		close(release)
		got := <-done
		if !errors.Is(got.err, context.Canceled) || len(got.output) != 0 {
			t.Fatalf("canceled builtin call committed late success: output=%s err=%v", got.output, got.err)
		}
		output, err := host.Call(t.Context(), required, json.RawMessage(`"healthy"`))
		if err != nil || string(output) != `{"success":true}` {
			t.Fatalf("canceled call poisoned live builtin: output=%s err=%v", output, err)
		}
	})
}
