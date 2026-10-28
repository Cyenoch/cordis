package wire

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

func TestUnansweredCancellationDoesNotKeepWaitingGoroutines(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, peer := auditEndpoint(t, Limits{Inflight: 4, Grace: time.Hour})
		requests := make(chan Frame)
		go func() {
			decoder := json.NewDecoder(peer)
			for {
				var frame Frame
				if decoder.Decode(&frame) != nil {
					return
				}
				if frame.Kind == "request" {
					requests <- frame
				}
			}
		}()
		synctest.Wait()
		before := runtime.NumGoroutine()
		for range 4 {
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() { defer close(done); _, _ = e.Call(ctx, "health", nil) }()
			<-requests
			cancel()
			<-done
			synctest.Wait()
		}
		if growth := runtime.NumGoroutine() - before; growth != 0 {
			t.Errorf("unanswered cancellations kept %d waiting goroutines", growth)
		}
		e.Close()
		e.Wait()
	})
}

func TestAcknowledgedCancellationReleasesSupervision(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, peer := auditEndpoint(t, Limits{Inflight: 1, Grace: time.Hour})
		requests := make(chan Frame)
		go func() {
			dec := json.NewDecoder(peer)
			for {
				var f Frame
				if dec.Decode(&f) != nil {
					return
				}
				if f.Kind == "request" {
					requests <- f
					continue
				}
				if f.Kind == "cancel" {
					if json.NewEncoder(peer).Encode(Frame{Schema: contract.SessionSchema, Kind: "response", Activation: "audit", ID: f.ID, Method: "health", Payload: json.RawMessage(`{}`)}) != nil {
						return
					}
				}
			}
		}()
		for range 500 {
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { _, err := e.Call(ctx, "health", nil); done <- err }()
			<-requests
			cancel()
			<-done
			synctest.Wait()
		}
		time.Sleep(2 * time.Hour)
		done := make(chan error, 1)
		go func() { _, err := e.Call(t.Context(), "health", nil); done <- err }()
		select {
		case request := <-requests:
			auditWrite(t, peer, Frame{
				Schema: contract.SessionSchema, Kind: "response", Activation: "audit",
				ID: request.ID, Method: request.Method, Payload: json.RawMessage(`{}`),
			})
		case <-e.Done():
			t.Fatalf("acknowledged cancellation expired endpoint: %v", e.Err())
		}
		if err := <-done; err != nil {
			t.Fatalf("health after acknowledged cancellations: %v", err)
		}
	})
}
