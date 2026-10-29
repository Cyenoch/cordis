package cordis

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

type receiptSession struct {
	fixtureSession
	deliver func(context.Context, func(bool)) (json.RawMessage, error)
}

func (s *receiptSession) CallWithReceipt(ctx context.Context, _ string, _ json.RawMessage, receipt func(bool)) (json.RawMessage, error) {
	return s.deliver(ctx, receipt)
}

func TestNativeCancelWaitsForSendReceiptOnly(t *testing.T) {
	for _, once := range []bool{false, true} {
		t.Run(map[bool]string{false: "regular", true: "once"}[once], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := core.New()
				defer func() {
					if err := e.Close(t.Context()); err != nil {
						t.Error(err)
					}
				}()
				entered, send, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
				sendNow, finishNow := sync.OnceFunc(func() { close(send) }), sync.OnceFunc(func() { close(finish) })
				defer finishNow()
				defer sendNow()
				session := &receiptSession{deliver: func(ctx context.Context, receipt func(bool)) (json.RawMessage, error) {
					close(entered)
					<-send
					receipt(true)
					<-finish
					return json.RawMessage(`{"handled":true,"value":false}`), nil
				}}
				var registry *remoteSubscriptions
				instance, err := e.Add(contract.Entry{ID: "native", Enabled: true}, contract.Definition{Start: func(_ context.Context, scope contract.Scope) error {
					registry = &remoteSubscriptions{scope: scope.(*core.Scope), owner: scope.Identity(), plugin: genericPlugin{session: session}, entries: map[string]*remoteSubscription{}}
					return nil
				}}, contract.Identity{})
				if err != nil {
					t.Fatal(err)
				}
				if err := instance.Wait(t.Context()); err != nil {
					t.Fatal(err)
				}
				id := registry.owner.Activation + "/1"
				spec := contract.SubscriptionSpec{ID: id, Name: "event", Options: contract.SubscriptionOptions{Once: once}}
				if err := registry.subscribe(t.Context(), spec); err != nil {
					t.Fatal(err)
				}
				for _, invalid := range []string{"", "other/1", registry.owner.Activation + "/01", id} {
					spec.ID = invalid
					if err := registry.subscribe(t.Context(), spec); contract.ErrorCode(err) != contract.Invalid {
						t.Fatalf("invalid or duplicate identity %q: %v", invalid, err)
					}
				}
				emitted := make(chan error, 1)
				go func() { _, err := e.Emit(t.Context(), "event", nil, contract.Sequential); emitted <- err }()
				<-entered
				canceled := make(chan error, 1)
				go func() { canceled <- registry.unsubscribe(t.Context(), id) }()
				synctest.Wait()
				select {
				case <-canceled:
					t.Fatal("cancellation confirmed before the admitted send settled")
				default:
				}
				sendNow()
				if err := <-canceled; err != nil {
					t.Fatal(err)
				}
				select {
				case <-emitted:
					t.Fatal("cancellation waited for callback completion")
				default:
				}
				if err := registry.unsubscribe(t.Context(), id); err != nil {
					t.Fatalf("repeat cancel: %v", err)
				}
				if results, err := e.Emit(t.Context(), "event", nil, contract.Sequential); err != nil || len(results) != 0 {
					t.Fatalf("canceled listener admitted again: %v %v", results, err)
				}
				finishNow()
				if err := <-emitted; err != nil {
					t.Fatal(err)
				}
				if err := instance.Stop(t.Context()); err != nil {
					t.Fatal(err)
				}
				spec.ID = registry.owner.Activation + "/2"
				if err := registry.subscribe(t.Context(), spec); contract.ErrorCode(err) != contract.NotReady {
					t.Fatalf("retired activation accepted registration: %v", err)
				}
			})
		})
	}
}
