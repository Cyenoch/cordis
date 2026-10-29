package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

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
	endpoint.Close()
	if err := peer.Close(); err != nil {
		t.Error(err)
	}
	endpoint.Wait()
}

func TestAnsweredCancellationDoesNotExpireHealthyEndpoint(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		left, right := net.Pipe()
		const grace = time.Second
		endpoint, err := New(Options{FrameBytes: contract.MaxFrameBytes,
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
		base, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		peerDone := make(chan error, 1)
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
				payload := json.RawMessage(`{}`)
				if request.Method == "initialize" {
					payload = testRegistration()
				}
				if err := writeLifecycleFrame(right, Frame{
					Schema: contract.SessionSchema, Kind: "response", Activation: request.Activation,
					ID: request.ID, Method: request.Method, Payload: payload,
				}); err != nil {
					peerDone <- err
					return
				}
				if request.ID == "h:1" {
					cancel()
				}
			}
		}()

		callDone := make(chan error, 1)
		go func() {
			_, err := endpoint.Call(base, "initialize", testInitialize("answered"))
			callDone <- err
		}()
		if err := <-callDone; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("answered canceled call: %v", err)
		}
		time.Sleep(2 * grace)
		synctest.Wait()
		if _, err := endpoint.Call(t.Context(), "health", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("already answered cancellation killed the endpoint: %v", err)
		}
		endpoint.Close()
		<-peerDone
	})
}

func TestInitializeReplyAdmitsImmediateGuestRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		left, right := net.Pipe()
		handled := make(chan string, 1)
		endpoint, err := New(Options{FrameBytes: contract.MaxFrameBytes,
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
		callDone := make(chan error, 1)
		go func() {
			_, err := endpoint.Call(base, "initialize", testInitialize("initialize"))
			callDone <- err
		}()
		decoder := json.NewDecoder(right)
		var initialize Frame
		if err := decoder.Decode(&initialize); err != nil {
			t.Fatal(err)
		}
		peerDone := make(chan error, 1)
		go func() {
			var frames bytes.Buffer
			if err := writeLifecycleFrame(&frames, Frame{
				Schema: contract.SessionSchema, Kind: "response", Activation: "initialize",
				ID: initialize.ID, Method: "initialize", Payload: testRegistration(),
			}); err != nil {
				peerDone <- err
				return
			}
			if err := writeLifecycleFrame(&frames, Frame{
				Schema: contract.SessionSchema, Kind: "request", Activation: "initialize",
				ID: "g:1", Method: "call", Payload: json.RawMessage(`{}`), DeadlineMS: 1000,
			}); err != nil {
				peerDone <- err
				return
			}
			_, err := right.Write(frames.Bytes())
			peerDone <- err
		}()
		synctest.Wait()
		select {
		case method := <-handled:
			if method != "call" {
				t.Errorf("handled method = %q", method)
			}
		default:
			t.Errorf("immediate guest request was not admitted: %v", endpoint.Err())
		}
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
