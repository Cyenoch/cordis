package wire

import (
	"context"
	"encoding/json"
	"github.com/Cyenoch/cordis/contract"
	"runtime"
	"testing"
	"testing/synctest"
	"time"
)

func TestAcknowledgedCancellationReleasesSupervision(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, peer := auditEndpoint(t, Limits{Inflight: 1, Grace: time.Hour})
		requests := make(chan struct{})
		go func() {
			dec := json.NewDecoder(peer)
			for {
				var f Frame
				if dec.Decode(&f) != nil {
					return
				}
				if f.Kind == "request" {
					requests <- struct{}{}
					continue
				}
				if f.Kind == "cancel" {
					if json.NewEncoder(peer).Encode(Frame{Schema: contract.SessionSchema, Kind: "response", Activation: "audit", ID: f.ID, Method: "health", Payload: json.RawMessage(`{}`)}) != nil {
						return
					}
				}
			}
		}()
		before := runtime.NumGoroutine()
		for range 500 {
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { _, err := e.Call(ctx, "health", nil); done <- err }()
			<-requests
			cancel()
			<-done
			synctest.Wait()
		}
		e.mu.Lock()
		history := len(e.cancelOrder)
		awaiting := 0
		for _, c := range e.canceled {
			if c.awaiting {
				awaiting++
			}
		}
		e.mu.Unlock()
		growth := runtime.NumGoroutine() - before
		if growth > 20 {
			t.Errorf("500 fully acknowledged sequential cancellations: history=%d, awaiting=%d, extra live goroutines=%d", history, awaiting, growth)
		}
	})
}
