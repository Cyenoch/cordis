package wire

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

// Context gates park Call before and after it captures its response channel.
// Only channel scheduling is white-box; frames and health use real transport.
type lifecycleGate struct {
	entered chan struct{}
	release chan struct{}
}

type lifecycleContext struct {
	context.Context
	doneCalls atomic.Int32
	gates     []*lifecycleGate
}

func (c *lifecycleContext) Done() <-chan struct{} {
	n := int(c.doneCalls.Add(1)) - 1
	if n < len(c.gates) && c.gates[n] != nil {
		close(c.gates[n].entered)
		<-c.gates[n].release
	}
	return c.Context.Done()
}

func writeLifecycleFrame(writer io.Writer, frame Frame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	_, err = writer.Write(append(data, '\n'))
	return err
}

func closeLifecycleEndpoint(t *testing.T, endpoint *Endpoint, peer net.Conn) {
	t.Helper()
	if err := endpoint.Close(); err != nil {
		t.Error(err)
	}
	if err := peer.Close(); err != nil {
		t.Error(err)
	}
	endpoint.Wait()
}

func TestAnsweredCancellationDoesNotExpireHealthyEndpoint(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		left, right := net.Pipe()
		const grace = time.Second
		endpoint, err := New(Options{
			Transport: left,
			Identity:  contract.Identity{Activation: "answered"},
			Limits:    Limits{Grace: grace},
			Handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(`{}`), nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer closeLifecycleEndpoint(t, endpoint, right)
		peerDone := make(chan error, 1)
		responseReady := make(chan struct{})
		go func() {
			decoder := json.NewDecoder(right)
			for {
				var request Frame
				if err := decoder.Decode(&request); err != nil {
					peerDone <- err
					return
				}
				if request.Kind == "cancel" {
					// An already answered request has no further cancellation reply.
					continue
				}
				if request.ID == "h:1" {
					<-responseReady
				}
				if err := writeLifecycleFrame(right, Frame{
					Schema: contract.SessionSchema, Kind: "response", Activation: request.Activation,
					ID: request.ID, Method: request.Method, Payload: json.RawMessage(`{}`),
				}); err != nil {
					peerDone <- err
					return
				}
			}
		}()

		base, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		before := &lifecycleGate{entered: make(chan struct{}), release: make(chan struct{})}
		after := &lifecycleGate{entered: make(chan struct{}), release: make(chan struct{})}
		ctx := &lifecycleContext{Context: base, gates: []*lifecycleGate{before, after}}
		callDone := make(chan error, 1)
		go func() {
			_, err := endpoint.Call(ctx, "initialize", json.RawMessage(`{}`))
			callDone <- err
		}()
		<-before.entered
		endpoint.mu.Lock()
		request := endpoint.pending["h:1"]
		request.result = nil
		endpoint.mu.Unlock()
		close(before.release)
		<-after.entered
		// Call captured a nil result channel before reaching its second Done
		// check. Deliver the real reply to a separate buffered channel, so only
		// cancellation can wake the caller's select after receive removes it.
		endpoint.mu.Lock()
		request.result = make(chan reply, 1)
		endpoint.mu.Unlock()
		close(responseReady)
		synctest.Wait()
		cancel()
		close(after.release)
		if err := <-callDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("answered canceled call: %v", err)
		}
		time.Sleep(2 * grace)
		synctest.Wait()
		if _, err := endpoint.Call(t.Context(), "health", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("already answered cancellation killed the endpoint: %v", err)
		}
		if err := endpoint.Close(); err != nil {
			t.Fatal(err)
		}
		<-peerDone
	})
}

func TestInitializeReplyAdmitsImmediateGuestRequestBeforeCallerResumes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		left, right := net.Pipe()
		handled := make(chan string, 1)
		endpoint, err := New(Options{
			Transport: left,
			Identity:  contract.Identity{Activation: "initialize"},
			Handler: func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
				handled <- method
				return json.RawMessage(`{"accepted":true}`), nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer closeLifecycleEndpoint(t, endpoint, right)
		base, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		gate := &lifecycleGate{entered: make(chan struct{}), release: make(chan struct{})}
		ctx := &lifecycleContext{Context: base, gates: []*lifecycleGate{nil, gate}}
		callDone := make(chan error, 1)
		go func() {
			_, err := endpoint.Call(ctx, "initialize", json.RawMessage(`{}`))
			callDone <- err
		}()
		decoder := json.NewDecoder(right)
		var initialize Frame
		if err := decoder.Decode(&initialize); err != nil {
			t.Fatal(err)
		}
		<-gate.entered
		peerDone := make(chan error, 1)
		go func() {
			if err := writeLifecycleFrame(right, Frame{
				Schema: contract.SessionSchema, Kind: "response", Activation: "initialize",
				ID: initialize.ID, Method: "initialize", Payload: json.RawMessage(`{}`),
			}); err != nil {
				peerDone <- err
				return
			}
			peerDone <- writeLifecycleFrame(right, Frame{
				Schema: contract.SessionSchema, Kind: "request", Activation: "initialize",
				ID: "g:1", Method: "host.invoke", Payload: json.RawMessage(`{}`), DeadlineMS: 1000,
			})
		}()
		synctest.Wait()
		select {
		case method := <-handled:
			if method != "host.invoke" {
				t.Errorf("handled method = %q", method)
			}
		default:
			t.Errorf("immediate guest request was not admitted: %v", endpoint.Err())
		}
		close(gate.release)
		if err := <-callDone; err != nil {
			t.Fatalf("initialize: %v", err)
		}
		if err := <-peerDone; err != nil {
			t.Fatalf("guest request: %v", err)
		}
		var response Frame
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.ID != "g:1" || response.Error != nil || string(response.Payload) != `{"accepted":true}` {
			t.Fatalf("guest response: %+v", response)
		}
	})
}
