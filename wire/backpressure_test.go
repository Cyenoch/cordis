package wire_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/wire"
)

func TestUnreadResponsesBoundRequestWorkAndDoNotPoisonOtherEndpoints(t *testing.T) {
	for _, mode := range []string{"inflight overflow", "response delivery timeout"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const inflight = 3
				const grace = time.Second
				// One response may be in Write, inflight+4 may be queued, and
				// at most inflight handlers may wait for response delivery.
				const maxHandlers = 2*inflight + 5
				healthy := newBackpressureHealthyEndpoint(t)
				assertBackpressureHealth(t, healthy)
				left, peer := net.Pipe()
				var handled atomic.Int32
				receiver, err := wire.New(wire.Options{
					Transport: left, Guest: true,
					Limits: wire.Limits{Inflight: inflight, Grace: grace},
					Handler: func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
						if method == "initialize" {
							return json.RawMessage(`{"frameBytes":1048576,"minor":0,"features":[],"services":[],"subscriptions":[]}`), nil
						}
						if method != "initialize" {
							handled.Add(1)
						}
						return json.RawMessage(`{}`), nil
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					_ = receiver.Close()
					_ = peer.Close()
					receiver.Wait()
				}()
				write := func(sequence int, method string) error {
					payload := json.RawMessage(`{}`)
					if method == "initialize" {
						payload = json.RawMessage(`{"frameBytes":1048576,"identity":{"activation":"blocked-reader"},"protocol":{"major":1},"definition":"test","config":{}}`)
					}
					return json.NewEncoder(peer).Encode(wire.Frame{
						Schema: contract.SessionSchema, Kind: "request", Activation: "blocked-reader",
						ID: fmt.Sprintf("h:%d", sequence), Method: method,
						Payload: payload, DeadlineMS: 30000,
					})
				}
				if err := write(1, "initialize"); err != nil {
					t.Fatal(err)
				}
				var initialized wire.Frame
				if err := json.NewDecoder(peer).Decode(&initialized); err != nil {
					t.Fatal(err)
				}
				if initialized.ID != "h:1" || initialized.Error != nil {
					t.Fatalf("initialize reply: %+v", initialized)
				}
				// The peer now never reads again. A single sender issues strictly
				// increasing IDs, with a scheduling barrier after each real write.
				requests := make(chan int)
				written := make(chan error, 1)
				senderDone := make(chan struct{})
				go func() {
					defer close(senderDone)
					for sequence := range requests {
						written <- write(sequence, "call")
					}
				}()
				defer func() { close(requests); <-senderDone }()
				count := maxHandlers + 2
				if mode == "response delivery timeout" {
					count = inflight + 6
				}
				for sequence := 2; sequence < count+2; sequence++ {
					requests <- sequence
					if err := <-written; err != nil {
						break
					}
					synctest.Wait()
					select {
					case <-receiver.Done():
						sequence = count + 2
					default:
					}
				}
				if mode == "response delivery timeout" {
					select {
					case <-receiver.Done():
						t.Fatalf("closed before delivery grace elapsed: %v", receiver.Err())
					default:
					}
					time.Sleep(2 * grace)
					synctest.Wait()
				}
				select {
				case <-receiver.Done():
				default:
					t.Fatal("peer stopped reading replies but endpoint kept admitting requests")
				}
				if receiver.Err() == nil {
					t.Error("response flood closed without an error")
				}
				if got := handled.Load(); got == 0 || got > maxHandlers {
					t.Errorf("receiver ran %d handlers, want 1..%d", got, maxHandlers)
				}
				// Wait must drain every reader, writer and response-waiting handler;
				// no goroutine per unanswered request may survive this closure.
				receiver.Wait()
				assertBackpressureHealth(t, healthy)
			})
		})
	}
}

func newBackpressureHealthyEndpoint(t *testing.T) *wire.Endpoint {
	t.Helper()
	left, right := net.Pipe()
	handler := func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == "initialize" {
			return json.RawMessage(`{"frameBytes":1048576,"minor":0,"features":[],"services":[],"subscriptions":[]}`), nil
		}
		if method == "health" {
			return json.RawMessage(`{"ready":true}`), nil
		}
		return json.RawMessage(`{}`), nil
	}
	host, err := wire.New(wire.Options{FrameBytes: contract.MaxFrameBytes, Transport: left, Identity: contract.Identity{Activation: "healthy-neighbor"}, Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	guest, err := wire.New(wire.Options{Transport: right, Guest: true, Handler: handler})
	if err != nil {
		_ = host.Close()
		host.Wait()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = host.Close()
		_ = guest.Close()
		host.Wait()
		guest.Wait()
	})
	if _, err := host.Call(t.Context(), "initialize", json.RawMessage(`{"frameBytes":1048576,"identity":{"activation":"healthy-neighbor"},"protocol":{"major":1},"definition":"test","config":{}}`)); err != nil {
		t.Fatal(err)
	}
	return host
}

func assertBackpressureHealth(t *testing.T, endpoint *wire.Endpoint) {
	t.Helper()
	output, err := endpoint.Call(t.Context(), "health", json.RawMessage(`{}`))
	if err != nil || string(output) != `{"ready":true}` {
		t.Fatalf("independent endpoint health = %s, %v", output, err)
	}
}
