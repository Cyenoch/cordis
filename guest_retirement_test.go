package cordis

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func TestGuestRetirementNotifiesBeforeReturningReverseCall(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		inflight      bool
		cancelRequest bool
	}{
		{name: "new-call"},
		{name: "inflight-call", inflight: true},
		{name: "canceled-request", inflight: true, cancelRequest: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				engine := core.New()
				defer func() { _ = engine.Close(t.Context()) }()
				service := contract.Service{Name: "message", Major: 1}
				required := contract.Requirement{Name: "message", Major: 1}
				entered := make(chan struct{})
				provider, err := engine.Add(contract.Entry{ID: "provider", Enabled: true},
					contract.Definition{Provides: []contract.Service{service}, Start: func(_ context.Context, scope contract.Scope) error {
						return scope.Provide(service, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
							close(entered)
							<-ctx.Done()
							return nil, ctx.Err()
						})
					}}, contract.Identity{})
				if err != nil {
					t.Fatal(err)
				}
				if err := provider.Wait(t.Context()); err != nil {
					t.Fatal(err)
				}
				retiring, notify := make(chan struct{}), make(chan struct{})
				guestCtx, guestCancel := context.WithCancel(t.Context())
				defer guestCancel()
				requestCtx, requestCancel := context.WithCancel(t.Context())
				defer requestCancel()
				var scope *core.Scope
				consumer, err := engine.Add(contract.Entry{ID: "consumer", Enabled: true},
					contract.Definition{Requires: []contract.Requirement{required}, Start: func(_ context.Context, s contract.Scope) error {
						scope = s.(*core.Scope)
						return scope.OnRetire(func(context.Context) error {
							close(retiring)
							<-notify
							guestCancel()
							return nil
						})
					}}, contract.Identity{})
				if err != nil {
					t.Fatal(err)
				}
				if err := consumer.Wait(t.Context()); err != nil {
					t.Fatal(err)
				}
				replied := make(chan error, 1)
				call := func() {
					input, _ := json.Marshal(contract.ServiceCall{Required: required, Input: json.RawMessage(`{}`)})
					_, err := (&Host{}).handleGuest(requestCtx, scope, &remoteSubscriptions{scope: scope}, "call", input)
					if guestCtx.Err() == nil && requestCtx.Err() == nil {
						err = errors.New("reverse call replied before the guest received retirement")
					}
					replied <- err
				}
				if scenario.inflight {
					go call()
					<-entered
				}
				stopped := make(chan error, 1)
				go func() { stopped <- provider.Stop(t.Context()) }()
				<-retiring
				if !scenario.inflight {
					go call()
				}
				synctest.Wait()
				earlyReply := false
				select {
				case err := <-replied:
					earlyReply = true
					t.Error(err)
				default:
				}
				if scenario.cancelRequest {
					requestCancel()
					synctest.Wait()
					select {
					case err := <-replied:
						earlyReply = true
						if err == nil {
							t.Error("canceled reverse call succeeded")
						}
					default:
						t.Error("request cancellation waited for guest retirement")
					}
				}
				close(notify)
				if err := <-stopped; err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if !earlyReply {
					select {
					case err := <-replied:
						if err == nil {
							t.Fatal("retired call succeeded")
						}
					default:
						t.Fatal("reverse call did not finish after notification")
					}
				}
			})
		})
	}
}
