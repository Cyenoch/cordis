package contract_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

type fixedEndpoint contract.Service

func (e fixedEndpoint) Service() contract.Service { return contract.Service(e) }
func (e fixedEndpoint) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (e fixedEndpoint) Stream(context.Context, string, json.RawMessage) iter.Seq2[json.RawMessage, error] {
	return contract.FailedStream(contract.Fail(contract.Invalid, "no streams"))
}

func TestRequireWaitsForCompatibleMinor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		app := cordis.New(cordis.Options{})
		defer app.Close(context.Background())
		key := contract.EndpointKey("example.counter", 1)
		provide := func(minor uint32) *cordis.Instance[struct{}] {
			return cordis.Plugin[struct{}]{Name: "provider", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
				return key.Provide(s, fixedEndpoint{Name: "example.counter", Major: 1, Minor: minor})
			}}.Load(app.Scope(), struct{}{})
		}
		old := provide(1)
		consumer := cordis.Plugin[struct{}]{Name: "consumer", Requires: []cordis.Requirement{contract.Require(key, 2, 5)}}.Load(app.Scope(), struct{}{})
		synctest.Wait()
		status := consumer.Status()
		if status.State != cordis.Waiting || len(status.Missing) != 1 {
			t.Fatalf("status = %+v", status)
		}
		var version *contract.VersionError
		if !errors.As(status.Missing[0].Reason, &version) || version.Visible.Minor != 1 || version.Required.MinMinor != 2 {
			t.Fatalf("reason = %v", status.Missing[0].Reason)
		}
		if !errors.Is(status.Missing[0].Reason, contract.Fail(contract.Incompatible, "")) {
			t.Fatalf("reason %v is not classified as incompatible", status.Missing[0].Reason)
		}
		if err := old.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		provide(3)
		synctest.Wait()
		if got := consumer.Status().State; got != cordis.Active {
			t.Fatalf("consumer = %v after a compatible provider arrived", got)
		}
	})
}
