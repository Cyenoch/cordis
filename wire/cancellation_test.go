package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

func auditHandler(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
	if method == "initialize" {
		return testRegistration(), nil
	}
	return json.RawMessage(`{}`), nil
}
func auditWrite(t *testing.T, c net.Conn, f Frame) {
	t.Helper()
	if err := json.NewEncoder(c).Encode(f); err != nil {
		t.Fatal(err)
	}
}
func auditEndpoint(t *testing.T, limits Limits) (*Endpoint, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	e, err := New(Options{FrameBytes: contract.MaxFrameBytes, Transport: a, Identity: contract.Identity{Activation: "audit"}, Limits: limits, Handler: auditHandler})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close(); b.Close(); e.Wait() })
	return e, b
}

func TestCancellationHistoryRetainsUnansweredRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, peer := auditEndpoint(t, Limits{Inflight: 1, Grace: time.Second})
		requests := make(chan Frame, 1)
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
				if f.Kind == "cancel" && f.ID != "h:1" {
					if json.NewEncoder(peer).Encode(Frame{Schema: contract.SessionSchema, Kind: "response", Activation: "audit", ID: f.ID, Method: "health", Payload: json.RawMessage(`{}`)}) != nil {
						return
					}
				}
			}
		}()
		for i := 0; i < 5; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { _, err := e.Call(ctx, "health", nil); done <- err }()
			<-requests
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			synctest.Wait()
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		select {
		case <-e.Done():
			t.Logf("closed as expected: %v", e.Err())
		default:
			t.Error("unanswered h:1 survived two cancellation grace intervals after four later answered cancellations")
		}
	})
}

type auditGateTransport struct {
	net.Conn
	entered, release chan struct{}
	once             sync.Once
}

func (g *auditGateTransport) Write(p []byte) (int, error) {
	var f Frame
	_ = json.Unmarshal(p, &f)
	if f.Kind == "request" {
		g.once.Do(func() { close(g.entered); <-g.release })
	}
	return g.Conn.Write(p)
}
func TestCanceledParentChildInTransitKeepsSessionHealthy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := net.Pipe()
		gate := &auditGateTransport{Conn: b, entered: make(chan struct{}), release: make(chan struct{})}
		host, err := New(Options{FrameBytes: contract.MaxFrameBytes, Transport: a, Identity: contract.Identity{Activation: "audit"}, Handler: auditHandler})
		if err != nil {
			t.Fatal(err)
		}
		var guest *Endpoint
		guest, err = New(Options{Transport: gate, Guest: true, Handler: func(ctx context.Context, m string, p json.RawMessage) (json.RawMessage, error) {
			if m == "call" {
				return guest.Call(ctx, "host.invoke", p)
			}
			return auditHandler(ctx, m, p)
		}})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { host.Close(); guest.Close(); host.Wait(); guest.Wait() }()
		if _, err := host.Call(context.Background(), "initialize", testInitialize("audit")); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := host.Call(ctx, "call", nil); done <- err }()
		<-gate.entered
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		synctest.Wait()
		close(gate.release)
		synctest.Wait()
		if _, err := host.Call(context.Background(), "health", nil); err != nil {
			t.Fatalf("cooperative bidirectional call killed session after parent cancellation: %v", err)
		}
	})
}

func TestSendAdmissionObservesEachDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, peer := auditEndpoint(t, Limits{Inflight: 1, Grace: time.Second})
		// Fill the response queue while the peer never reads from its pipe.
		for i := 1; i <= 6; i++ {
			auditWrite(t, peer, Frame{Schema: contract.SessionSchema, Kind: "request", Activation: "audit", ID: fmt.Sprintf("g:%d", i), Method: "health", Payload: json.RawMessage(`{}`), DeadlineMS: 30000})
			synctest.Wait()
		}
		longCtx, longCancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer longCancel()
		longDone := make(chan error, 1)
		go func() { _, err := e.Call(longCtx, "health", nil); longDone <- err }()
		synctest.Wait()
		const timeout = 100 * time.Millisecond
		shortCtx, shortCancel := context.WithTimeout(t.Context(), timeout)
		defer shortCancel()
		start := time.Now()
		if _, err := e.Call(shortCtx, "health", nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("short call: %v", err)
		}
		if elapsed := time.Since(start); elapsed != timeout {
			t.Errorf("short call returned after %v, want %v", elapsed, timeout)
		}
		e.Close()
		<-longDone
	})
}
