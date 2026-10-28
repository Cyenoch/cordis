package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

func TestTimeoutBurstReleasesIncomingAdmissionBeforeHandlersExit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const inflight = 4
		left, right := net.Pipe()
		limits := Limits{Inflight: inflight, Grace: time.Second}
		host, err := New(Options{
			Transport: left, FrameBytes: contract.MaxFrameBytes,
			Identity: contract.Identity{Activation: "burst"}, Limits: limits, Handler: auditHandler,
		})
		if err != nil {
			t.Fatal(err)
		}
		entered := make(chan struct{}, inflight)
		canceled := make(chan struct{}, inflight)
		release := make(chan struct{})
		guest, err := New(Options{
			Transport: right, Guest: true, Limits: limits,
			Handler: func(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
				if method != "call" {
					return auditHandler(ctx, method, input)
				}
				entered <- struct{}{}
				<-ctx.Done()
				canceled <- struct{}{}
				<-release
				return nil, ctx.Err()
			},
		})
		if err != nil {
			host.Close()
			host.Wait()
			t.Fatal(err)
		}
		defer func() { close(release); host.Close(); guest.Close(); host.Wait(); guest.Wait() }()
		if _, err := host.Call(t.Context(), "initialize", testInitialize("burst")); err != nil {
			t.Fatal(err)
		}
		results := make(chan error, inflight)
		for range inflight {
			go func() {
				ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
				defer cancel()
				_, err := host.Call(ctx, "call", nil)
				results <- err
			}()
		}
		for range inflight {
			<-entered
		}
		for range inflight {
			if err := <-results; !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timed call: %v", err)
			}
			<-canceled
		}
		synctest.Wait()
		if _, err := host.Call(t.Context(), "health", nil); err != nil {
			t.Fatalf("health after timeout burst: %v; guest: %v", err, guest.Err())
		}
	})
}

func TestIncomingAdmissionRejectsUncanceledOverflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		left, peer := net.Pipe()
		entered := make(chan struct{}, 2)
		endpoint, err := New(Options{
			Transport: left, FrameBytes: contract.MaxFrameBytes,
			Identity: contract.Identity{Activation: "overflow"}, Limits: Limits{Inflight: 2},
			Handler: func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
				entered <- struct{}{}
				<-ctx.Done()
				return nil, ctx.Err()
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer closeLifecycleEndpoint(t, endpoint, peer)
		for n := 1; n <= 3; n++ {
			auditWrite(t, peer, Frame{
				Schema: contract.SessionSchema, Kind: "request", Activation: "overflow",
				ID: fmt.Sprintf("g:%d", n), Method: "health", DeadlineMS: 30000, Payload: json.RawMessage(`{}`),
			})
			if n <= 2 {
				<-entered
			}
		}
		<-endpoint.Done()
		if coded, ok := errors.AsType[*contract.Error](endpoint.Err()); !ok || coded.Code != contract.Invalid {
			t.Fatalf("overflow error: %v", endpoint.Err())
		}
		select {
		case <-entered:
			t.Error("over-limit request reached handler")
		default:
		}
	})
}

func TestIncomingAdmissionReleasesNormallyCompletedRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		endpoint, peer := auditEndpoint(t, Limits{Inflight: 1})
		decoder := json.NewDecoder(peer)
		for n := 1; n <= 3; n++ {
			id := fmt.Sprintf("g:%d", n)
			auditWrite(t, peer, Frame{
				Schema: contract.SessionSchema, Kind: "request", Activation: "audit", ID: id,
				Method: "health", DeadlineMS: 1000, Payload: json.RawMessage(`{}`),
			})
			var response Frame
			if err := decoder.Decode(&response); err != nil {
				t.Fatal(err)
			}
			if response.ID != id || response.Error != nil {
				t.Fatalf("health reply: %+v", response)
			}
			synctest.Wait()
		}
		select {
		case <-endpoint.Done():
			t.Fatalf("completed requests exhausted admission: %v", endpoint.Err())
		default:
		}
	})
}

func TestCanceledHandlerCompletionDoesNotReleaseAnotherRequestsAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		left, peer := net.Pipe()
		entered := make(chan struct{})
		release := make(chan struct{})
		endpoint, err := New(Options{
			Transport: left, FrameBytes: contract.MaxFrameBytes,
			Identity: contract.Identity{Activation: "retiring"}, Limits: Limits{Inflight: 1},
			Handler: func(ctx context.Context, _ string, input json.RawMessage) (json.RawMessage, error) {
				entered <- struct{}{}
				<-ctx.Done()
				if string(input) == `"retiring"` {
					<-release
				}
				return nil, ctx.Err()
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer closeLifecycleEndpoint(t, endpoint, peer)
		request := Frame{
			Schema: contract.SessionSchema, Kind: "request", Activation: "retiring", ID: "g:1",
			Method: "health", DeadlineMS: 30000, Payload: json.RawMessage(`"retiring"`),
		}
		auditWrite(t, peer, request)
		<-entered
		for range 2 {
			auditWrite(t, peer, Frame{Schema: contract.SessionSchema, Kind: "cancel", Activation: "retiring", ID: "g:1"})
		}
		request.ID, request.Payload = "g:2", json.RawMessage(`{}`)
		auditWrite(t, peer, request)
		<-entered
		close(release)
		synctest.Wait()
		request.ID = "g:3"
		auditWrite(t, peer, request)
		<-endpoint.Done()
		if coded, ok := errors.AsType[*contract.Error](endpoint.Err()); !ok || coded.Code != contract.Invalid {
			t.Fatalf("overflow after canceled handler exit: %v", endpoint.Err())
		}
	})
}

func TestCanceledIncomingHandlersHaveSeparateBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		left, peer := net.Pipe()
		entered := make(chan struct{})
		release := make(chan struct{})
		endpoint, err := New(Options{
			Transport: left, FrameBytes: contract.MaxFrameBytes,
			Identity: contract.Identity{Activation: "cancel-flood"}, Limits: Limits{Inflight: 1},
			Handler: func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
				entered <- struct{}{}
				<-ctx.Done()
				<-release
				return nil, ctx.Err()
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { close(release); closeLifecycleEndpoint(t, endpoint, peer) }()
		for n := 1; n <= 5; n++ {
			id := fmt.Sprintf("g:%d", n)
			auditWrite(t, peer, Frame{
				Schema: contract.SessionSchema, Kind: "request", Activation: "cancel-flood", ID: id,
				Method: "health", DeadlineMS: 30000, Payload: json.RawMessage(`{}`),
			})
			<-entered
			auditWrite(t, peer, Frame{Schema: contract.SessionSchema, Kind: "cancel", Activation: "cancel-flood", ID: id})
		}
		synctest.Wait()
		select {
		case <-endpoint.Done():
			if !errors.Is(endpoint.Err(), contract.Fail(contract.Invalid, "")) {
				t.Fatalf("cancel flood reported wrong error: %v", endpoint.Err())
			}
		default:
			t.Fatal("raw peer retained more canceled handlers than the resource budget")
		}
	})
}
