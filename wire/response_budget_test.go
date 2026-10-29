package wire

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestOversizedResponseIsolatesCall(t *testing.T) {
	for _, hostResponse := range []bool{false, true} {
		for _, errorResponse := range []bool{false, true} {
			t.Run(map[bool]string{false: "guest", true: "host"}[hostResponse]+map[bool]string{false: "/result", true: "/error"}[errorResponse], func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				large := strings.Repeat("sensitive-result", 100000)
				handler := func(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
					if string(input) == `"slow"` {
						close(entered)
						select {
						case <-release:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
					if string(input) == `"large"` {
						if errorResponse {
							return nil, contract.Fail(contract.Internal, large)
						}
						return json.RawMessage(`"` + large + `"`), nil
					}
					return json.RawMessage(`{}`), nil
				}
				host, guest := pair(t, handler, handler)
				caller, responder := host, guest
				if hostResponse {
					caller, responder = guest, host
				}
				sibling := make(chan error, 1)
				go func() { _, err := caller.Call(t.Context(), "call", json.RawMessage(`"slow"`)); sibling <- err }()
				<-entered
				_, err := caller.Call(t.Context(), "call", json.RawMessage(`"large"`))
				close(release)
				coded, ok := errors.AsType[*contract.Error](err)
				if !ok || coded.Code != contract.PluginFailed || strings.Contains(err.Error(), "sensitive-result") {
					t.Fatalf("oversized call: %v", err)
				}
				if err := <-sibling; err != nil {
					t.Fatalf("unrelated call: %v", err)
				}
				if _, err := caller.Call(t.Context(), "health", nil); err != nil {
					t.Fatalf("subsequent call: %v", err)
				}
				if responder.Err() != nil || responder.Identity().Activation != "sample" {
					t.Fatalf("session changed: %v", responder.Err())
				}
			})
		}
	}
}

type countedResponseTransport struct {
	net.Conn
	bytes   atomic.Int64
	written chan struct{}
}

func (c *countedResponseTransport) Write(data []byte) (int, error) {
	n, err := c.Conn.Write(data)
	c.bytes.Add(int64(n))
	c.written <- struct{}{}
	return n, err
}

func TestOversizedResponseWritesOnlyFixedError(t *testing.T) {
	left, right := net.Pipe()
	transport := &countedResponseTransport{Conn: right, written: make(chan struct{}, 4)}
	host, err := New(Options{Transport: left, FrameBytes: 4096, Identity: contract.Identity{Activation: "a"}, Handler: auditHandler})
	if err != nil {
		t.Fatal(err)
	}
	guest, err := New(Options{Transport: transport, Guest: true, Handler: func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == "initialize" {
			return json.RawMessage(`{"frameBytes":4096,"minor":0,"features":[],"services":[],"subscriptions":[]}`), nil
		}
		return json.RawMessage(`"` + strings.Repeat("x", 8192) + `"`), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close(); guest.Close(); host.Wait(); guest.Wait() })
	input := json.RawMessage(strings.Replace(string(testInitialize("a")), "1048576", "4096", 1))
	if _, err := host.Call(t.Context(), "initialize", input); err != nil {
		t.Fatal(err)
	}
	<-transport.written
	before := transport.bytes.Load()
	if _, err := host.Call(t.Context(), "call", nil); err == nil {
		t.Fatal("large result succeeded")
	}
	expected, err := encodeFrame(Frame{Schema: contract.SessionSchema, Kind: "response", Activation: "a", ID: "h:2", Method: "call", Error: &contract.Error{Code: contract.PluginFailed, Message: "response exceeds frame byte limit"}}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	<-transport.written
	if got := transport.bytes.Load() - before; got != int64(len(expected)) {
		t.Fatalf("wrote %d bytes, want only %d-byte error", got, len(expected))
	}
}

func TestCanceledOversizedResultDoesNotReplaceCancellation(t *testing.T) {
	entered := make(chan struct{})
	host, _ := pair(t, auditHandler, func(ctx context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == "call" {
			close(entered)
			<-ctx.Done()
			return json.RawMessage(`"` + strings.Repeat("x", contract.MaxFrameBytes) + `"`), nil
		}
		return json.RawMessage(`{}`), nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := host.Call(ctx, "call", nil); done <- err }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation overwritten: %v", err)
	}
	if _, err := host.Call(t.Context(), "health", nil); err != nil {
		t.Fatal(err)
	}
}

func TestUnencodableFixedErrorIsFatal(t *testing.T) {
	endpoint := &Endpoint{frameBytes: 128, ready: true, incoming: map[string]incoming{"h:1": {recoverable: true}}}
	err := endpoint.sendResponse(t.Context(), t.Context(), Frame{Schema: contract.SessionSchema, Kind: "response", Activation: "a", ID: "h:1", Method: "call", Payload: json.RawMessage(`"` + strings.Repeat("x", 1000) + `"`)})
	if _, ok := errors.AsType[*frameSizeError](err); !ok {
		t.Fatalf("unencodable error was hidden: %v", err)
	}
}

func TestOversizedControlResponsesRemainFatal(t *testing.T) {
	for _, method := range []string{"initialize", "health", "shutdown", "activation.publish", "activation.retire"} {
		t.Run(method, func(t *testing.T) {
			handler := func(_ context.Context, called string, _ json.RawMessage) (json.RawMessage, error) {
				if called == method {
					return nil, contract.Fail(contract.Internal, strings.Repeat("x", contract.MaxFrameBytes))
				}
				return json.RawMessage(`{}`), nil
			}
			if method == "initialize" {
				left, right := net.Pipe()
				host, err := New(Options{Transport: left, FrameBytes: contract.MaxFrameBytes, Identity: contract.Identity{Activation: "a"}, Handler: handler})
				if err != nil {
					t.Fatal(err)
				}
				guest, err := New(Options{Transport: right, Guest: true, Handler: handler})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { host.Close(); guest.Close(); host.Wait(); guest.Wait() })
				if _, err := host.Call(t.Context(), method, testInitialize("a")); err == nil {
					t.Fatal("control succeeded")
				}
				awaitEndpoint(t, guest)
				return
			}
			host, guest := pair(t, handler, handler)
			if _, err := host.Call(t.Context(), method, nil); err == nil {
				t.Fatal("control succeeded")
			}
			awaitEndpoint(t, guest)
		})
	}
}

type responseWriteFault struct {
	net.Conn
	partial bool
}

func (c *responseWriteFault) Write(data []byte) (int, error) {
	var frame Frame
	if json.Unmarshal(data, &frame) == nil && frame.Kind == "response" && frame.Method == "call" {
		if c.partial {
			_, _ = c.Conn.Write(data[:1])
			return 1, io.ErrUnexpectedEOF
		}
		return 0, nil
	}
	return c.Conn.Write(data)
}

func TestFallbackWriteFailuresRemainFatal(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "zero write", true: "partial error"}[partial], func(t *testing.T) {
			left, right := net.Pipe()
			host, err := New(Options{Transport: left, FrameBytes: contract.MaxFrameBytes, Identity: contract.Identity{Activation: "a"}, Handler: auditHandler})
			if err != nil {
				t.Fatal(err)
			}
			guest, err := New(Options{Transport: &responseWriteFault{Conn: right, partial: partial}, Guest: true, Handler: func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
				if method == "initialize" {
					return testRegistration(), nil
				}
				return json.RawMessage(`"` + strings.Repeat("x", contract.MaxFrameBytes) + `"`), nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { host.Close(); guest.Close(); host.Wait(); guest.Wait() })
			if _, err := host.Call(t.Context(), "initialize", testInitialize("a")); err != nil {
				t.Fatal(err)
			}
			if _, err := host.Call(t.Context(), "call", nil); err == nil {
				t.Fatal("failed writer returned success")
			}
			awaitEndpoint(t, guest)
		})
	}
}

func TestInvalidResponseJSONRemainsFatal(t *testing.T) {
	host, guest := pair(t, auditHandler, func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == "call" {
			return json.RawMessage(`{`), nil
		}
		return json.RawMessage(`{}`), nil
	})
	if _, err := host.Call(t.Context(), "call", nil); err == nil {
		t.Fatal("invalid JSON succeeded")
	}
	awaitEndpoint(t, guest)
}

func TestFrameBudgetIncludesEscapingAndLF(t *testing.T) {
	frame := Frame{Schema: contract.SessionSchema, Kind: "response", Activation: "a", ID: "h:1", Method: "call", Payload: json.RawMessage(`"<"`)}
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `\u003c`) {
		t.Fatal("fixture does not exercise escaping")
	}
	if _, err := encodeFrame(frame, len(data)+1); err != nil {
		t.Fatal(err)
	}
	if data, err := encodeFrame(frame, len(data)); err == nil || len(data) != 0 {
		t.Fatalf("over budget wrote bytes: %q, %v", data, err)
	}
}
