package wire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

func TestGuestRejectsInvalidBootstrapBeforeHandler(t *testing.T) {
	for _, input := range []string{
		`{}`,
		`{"frameBytes":null}`,
		`{"frameBytes":"4096"}`,
		`{"frameBytes":4095}`,
		`{"frameBytes":1048577}`,
		`{"frameBytes":4096,"unknown":true}`,
		`{"frameBytes":4096,"frameBytes":4096}`,
		`{"frameBytes":4096,"identity":{"activation":"wrong"}}`,
		`{"frameBytes":4096,"identity":{"activation":"a"},"config":"` + strings.Repeat("x", 5000) + `"}`,
	} {
		t.Run(input[:min(len(input), 60)], func(t *testing.T) {
			left, right := net.Pipe()
			var handled atomic.Int32
			guest, err := New(Options{Transport: left, Guest: true, Handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				handled.Add(1)
				return nil, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { guest.Close(); right.Close(); guest.Wait() })
			frame := Frame{Schema: contract.SessionSchema, Kind: "request", Activation: "a", ID: "h:1", Method: "initialize", DeadlineMS: 1000, Payload: json.RawMessage(input)}
			_ = json.NewEncoder(right).Encode(frame)
			awaitEndpoint(t, guest)
			if handled.Load() != 0 || guest.Err() == nil {
				t.Fatalf("invalid bootstrap reached handler: %v", guest.Err())
			}
		})
	}
}

func awaitEndpoint(t *testing.T, endpoint *Endpoint) {
	t.Helper()
	select {
	case <-endpoint.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("endpoint did not close")
	}
}

func TestGuestUsesRawBootstrapLengthAndBoundNextFrame(t *testing.T) {
	for _, trailingFrame := range []bool{false, true} {
		t.Run(map[bool]string{false: "raw whitespace", true: "buffered next frame"}[trailingFrame], func(t *testing.T) {
			left, right := net.Pipe()
			guest, err := New(Options{Transport: left, Guest: true, Handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(`{"frameBytes":4096,"minor":0,"features":[],"services":[],"subscriptions":[],"contributions":[]}`), nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { guest.Close(); right.Close(); guest.Wait() })
			input := json.RawMessage(`{"frameBytes":4096,"identity":{"activation":"a"},"protocol":{"major":1},"definition":"test","config":{}}`)
			first, err := encodeFrame(Frame{Schema: contract.SessionSchema, Kind: "request", Activation: "a", ID: "h:1", Method: "initialize", DeadlineMS: 1000, Payload: input}, contract.MaxFrameBytes)
			if err != nil {
				t.Fatal(err)
			}
			data := append([]byte(strings.Repeat(" ", 5000)), first...)
			if trailingFrame {
				data = append(first, []byte(strings.Repeat(" ", 5000)+"\n")...)
			}
			go func() { _, _ = right.Write(data) }()
			awaitEndpoint(t, guest)
			if guest.Err() == nil {
				t.Fatal("accepted bytes beyond proposed budget")
			}
		})
	}
}

func TestBootstrapHardLimitWithoutNewline(t *testing.T) {
	left, right := net.Pipe()
	guest, err := New(Options{Transport: left, Guest: true, Handler: auditHandler})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { guest.Close(); right.Close(); guest.Wait() })
	go func() { _, _ = right.Write([]byte(strings.Repeat(" ", contract.MaxFrameBytes+1))) }()
	awaitEndpoint(t, guest)
}

func TestGuestAdmitsImmediateRequestAfterRegistration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		left, right := net.Pipe()
		host, err := New(Options{Transport: left, FrameBytes: contract.MaxFrameBytes, Identity: contract.Identity{Activation: "a"}, Handler: auditHandler})
		if err != nil {
			t.Fatal(err)
		}
		guest, err := New(Options{Transport: right, Guest: true, Handler: auditHandler})
		if err != nil {
			host.Close()
			host.Wait()
			t.Fatal(err)
		}
		defer func() { host.Close(); guest.Close(); host.Wait(); guest.Wait() }()
		if _, err := host.Call(t.Context(), "initialize", testInitialize("a")); err != nil {
			t.Fatal(err)
		}
		if _, err := host.Call(t.Context(), "call", nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestInitializationCommitBarrierReleasesOnCancelOrClose(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "close"}[closeSession], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				left, right := net.Pipe()
				endpoint := &Endpoint{
					transport: left, guest: true, frameBytes: contract.MaxFrameBytes,
					out: make(chan outbound), done: make(chan struct{}), incoming: map[string]incoming{},
				}
				endpoint.ctx, endpoint.cancel = context.WithCancel(context.Background())
				defer endpoint.Close()
				defer right.Close()
				request, cancelRequest := context.WithCancel(t.Context())
				defer cancelRequest()
				done := make(chan error, 1)
				go func() {
					done <- endpoint.sendResponse(request, t.Context(), Frame{
						Schema: contract.SessionSchema, Kind: "response", Activation: "a",
						ID: "h:1", Method: "initialize", Payload: testRegistration(),
					})
				}()
				// The unbuffered queue parks delivery before the initialization commit.
				synctest.Wait()
				if closeSession {
					endpoint.cancel()
				} else {
					cancelRequest()
				}
				message := <-endpoint.out
				if err := <-done; err == nil {
					t.Fatal("canceled registration committed")
				}
				endpoint.mu.Lock()
				ready := endpoint.ready
				endpoint.mu.Unlock()
				if ready {
					t.Fatal("failed initialization left ready state")
				}
				writerDone, readDone := make(chan struct{}), make(chan struct{})
				go func() { defer close(writerDone); endpoint.writeLoop() }()
				endpoint.out <- message
				var received atomic.Int32
				go func() {
					defer close(readDone)
					var data [1]byte
					n, _ := right.Read(data[:])
					received.Store(int32(n))
				}()
				// Both reader and writer have reached their waits before closing pipes.
				synctest.Wait()
				if received.Load() != 0 {
					t.Error("success bytes escaped the canceled commit")
				}
				endpoint.shutdown(context.Canceled)
				<-writerDone
				<-readDone
			})
		})
	}
}

func TestHostRejectsForgedRegistration(t *testing.T) {
	for _, registration := range []string{`{}`, `{"frameBytes":4096}`, `{"frameBytes":1048576,"unknown":true}`} {
		t.Run(registration, func(t *testing.T) {
			left, right := net.Pipe()
			host, err := New(Options{Transport: left, FrameBytes: contract.MaxFrameBytes, Identity: contract.Identity{Activation: "a"}, Handler: auditHandler})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { host.Close(); right.Close(); host.Wait() })
			go func() {
				var request Frame
				if json.NewDecoder(right).Decode(&request) == nil {
					_ = json.NewEncoder(right).Encode(Frame{Schema: contract.SessionSchema, Kind: "response", Activation: "a", ID: request.ID, Method: "initialize", Payload: json.RawMessage(registration)})
				}
			}()
			if _, err := host.Call(t.Context(), "initialize", testInitialize("a")); err == nil {
				t.Fatal("forged registration succeeded")
			}
			awaitEndpoint(t, host)
		})
	}
}

func TestLateRegistrationDoesNotAdmitRequestsAfterCanceledInitialize(t *testing.T) {
	left, right := net.Pipe()
	var handled atomic.Int32
	host, err := New(Options{
		Transport: left, FrameBytes: contract.MaxFrameBytes, Identity: contract.Identity{Activation: "a"},
		Handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			handled.Add(1)
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close(); right.Close(); host.Wait() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := host.Call(ctx, "initialize", testInitialize("a")); done <- err }()
	decoder := json.NewDecoder(right)
	var request Frame
	if err := decoder.Decode(&request); err != nil {
		t.Fatal(err)
	}
	cancel()
	var canceled Frame
	if err := decoder.Decode(&canceled); err != nil || canceled.Kind != "cancel" {
		t.Fatalf("initialize cancellation: %+v, %v", canceled, err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("initialize: %v", err)
	}
	encoder := json.NewEncoder(right)
	if err := encoder.Encode(Frame{Schema: contract.SessionSchema, Kind: "response", Activation: "a", ID: request.ID, Method: "initialize", Payload: testRegistration()}); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(Frame{Schema: contract.SessionSchema, Kind: "request", Activation: "a", ID: "g:1", Method: "host.invoke", DeadlineMS: 1000, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	awaitEndpoint(t, host)
	if handled.Load() != 0 || host.Err() == nil {
		t.Fatalf("late registration reopened admission: handled=%d, error=%v", handled.Load(), host.Err())
	}
}

func TestBootstrapAndRepeatedInitializeAreNotFallbacks(t *testing.T) {
	left, right := net.Pipe()
	guest, err := New(Options{Transport: right, Guest: true, Handler: auditHandler})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { guest.Close(); left.Close(); guest.Wait() })
	if _, err := guest.Call(t.Context(), "health", nil); err == nil {
		t.Fatal("guest sent before a budget was bound")
	}
	if _, err := New(Options{Transport: left, Identity: contract.Identity{Activation: "a"}, Handler: auditHandler}); err == nil {
		t.Fatal("host used an implicit frame budget")
	}
	host, _ := pair(t, auditHandler, auditHandler)
	if _, err := host.Call(t.Context(), "initialize", testInitialize("sample")); err == nil {
		t.Fatal("repeated initialization succeeded")
	}
	if _, err := host.Call(t.Context(), "health", nil); err != nil {
		t.Fatal(err)
	}
}

func TestSmallestBudgetFitsWorstEscapedFixedResponses(t *testing.T) {
	for _, code := range []contract.Code{contract.PluginFailed, contract.Canceled, contract.Timeout} {
		frame := Frame{Schema: contract.SessionSchema, Kind: "response", Activation: strings.Repeat("\x00", 128), ID: "h:18446744073709551615", Method: strings.Repeat("\x00", 256), Error: &contract.Error{Code: code, Message: "response exceeds frame byte limit"}}
		if _, err := encodeFrame(frame, contract.MinFrameBytes); err != nil {
			t.Fatal(err)
		}
		frame.Activation += "a"
		if _, err := encodeFrame(frame, contract.MaxFrameBytes); err == nil {
			t.Fatal("oversized activation accepted")
		}
	}
}

func TestIdentifiersUseUTF8BytesInBothDirections(t *testing.T) {
	for _, activation := range []string{strings.Repeat("界", 42), strings.Repeat("界", 43)} {
		frame := Frame{Schema: contract.SessionSchema, Kind: "response", Activation: activation, ID: "h:1", Method: "call", Payload: json.RawMessage(`{}`)}
		data, err := encodeFrame(frame, contract.MinFrameBytes)
		valid := len(activation) <= 128
		if (err == nil) != valid {
			t.Fatalf("send UTF-8 bytes=%d: %v", len(activation), err)
		}
		if data == nil {
			data, err = json.Marshal(frame)
			if err != nil {
				t.Fatal(err)
			}
			data = append(data, '\n')
		}
		_, err = readFrame(bufio.NewReader(bytes.NewReader(data)), contract.MinFrameBytes)
		if (err == nil) != valid {
			t.Fatalf("receive UTF-8 bytes=%d: %v", len(activation), err)
		}
	}
}

func TestLocalOversizedRequestDoesNotInvokePeer(t *testing.T) {
	var calls atomic.Int32
	handler := func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == "call" {
			calls.Add(1)
		}
		return json.RawMessage(`{}`), nil
	}
	host, _ := pair(t, handler, handler)
	_, err := host.Call(t.Context(), "call", json.RawMessage(`"`+strings.Repeat("x", contract.MaxFrameBytes)+`"`))
	if _, ok := errors.AsType[*frameSizeError](err); !ok || calls.Load() != 0 {
		t.Fatalf("local size rejection: %v, handled=%d", err, calls.Load())
	}
	if _, err := host.Call(t.Context(), "health", nil); err != nil {
		t.Fatal(err)
	}
}
