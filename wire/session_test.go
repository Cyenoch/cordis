package wire

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

func pair(t *testing.T, host Handler, guest Handler) (*Endpoint, *Endpoint) {
	t.Helper()
	a, b := net.Pipe()
	h, err := New(Options{FrameBytes: contract.MaxFrameBytes, Transport: a, Identity: contract.Identity{Activation: "sample"}, Handler: host})
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(Options{Transport: b, Guest: true, Handler: func(ctx context.Context, method string, data json.RawMessage) (json.RawMessage, error) {
		output, err := guest(ctx, method, data)
		if method == "initialize" && err == nil {
			return testRegistration(), nil
		}
		return output, err
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		h.Close()
		g.Close()
		h.Wait()
		g.Wait()
	})
	if _, err := h.Call(t.Context(), "initialize", testInitialize("sample")); err != nil {
		t.Fatal(err)
	}
	return h, g
}

func testInitialize(activation string) json.RawMessage {
	data, err := json.Marshal(contract.Initialize{FrameBytes: contract.MaxFrameBytes, Identity: contract.Identity{Activation: activation}, Protocol: contract.Protocol{Major: 1}, Definition: "test", Config: json.RawMessage(`{}`)})
	if err != nil {
		panic(err)
	}
	return data
}

func testRegistration() json.RawMessage {
	return json.RawMessage(`{"frameBytes":1048576,"minor":0,"features":[],"services":[],"subscriptions":[],"contributions":[]}`)
}

func TestEndpointBidirectionalCalls(t *testing.T) {
	var guest *Endpoint
	hostHandler := func(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
		if method != "host.invoke" {
			return nil, contract.Fail(contract.Invalid, "unknown method")
		}
		return json.RawMessage(`{"value":42}`), nil
	}
	guestHandler := func(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
		if method == "initialize" {
			return json.RawMessage(`{}`), nil
		}
		return guest.Call(ctx, "host.invoke", input)
	}
	h, g := pair(t, hostHandler, guestHandler)
	guest = g
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			result, err := h.Call(t.Context(), "call", json.RawMessage(`{}`))
			if err != nil {
				t.Error(err)
				return
			}
			if string(result) != `{"value":42}` {
				t.Errorf("result %s", result)
			}
		})
	}
	wg.Wait()
}

func TestEndpointCancellationDiscardsLateResult(t *testing.T) {
	entered := make(chan struct{})
	guest := func(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
		if method == "initialize" || method == "health" {
			return json.RawMessage(`{}`), nil
		}
		close(entered)
		<-ctx.Done()
		return json.RawMessage(`{"committed":true}`), nil
	}
	h, _ := pair(t, guest, guest)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { _, err := h.Call(ctx, "call", json.RawMessage(`{}`)); result <- err }()
	<-entered
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel result: %v", err)
	}
	if _, err := h.Call(t.Context(), "health", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("cooperative cancellation broke healthy session: %v", err)
	}
}

func TestFrameRejectsMalformedBoundaries(t *testing.T) {
	cases := []struct{ name, input string }{
		{name: "duplicate", input: `{"schema":"cordis-go.session/v1","kind":"request","id":"h:1","id":"h:2","activation":"a","method":"call","deadlineMs":1,"payload":{}}` + "\n"},
		{name: "unterminated", input: `{"schema":"cordis-go.session/v1"}`},
		{name: "unknown field", input: `{"schema":"cordis-go.session/v1","kind":"cancel","id":"h:1","activation":"a","secret":1}` + "\n"},
		{name: "oversize", input: strings.Repeat(" ", 1025) + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readFrame(bufio.NewReader(strings.NewReader(tc.input)), 1024); err == nil {
				t.Fatal("accepted invalid frame")
			}
		})
	}
}

func TestEndpointRejectsReplayedRequest(t *testing.T) {
	a, b := net.Pipe()
	e, err := New(Options{FrameBytes: contract.MaxFrameBytes, Transport: a, Identity: contract.Identity{Activation: "a"}, Handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
		e.Close()
		e.Wait()
	}()
	frame := Frame{Schema: contract.SessionSchema, Kind: "request", Activation: "a", ID: "g:1", Method: "health", Payload: json.RawMessage(`{}`), DeadlineMS: 1000}
	data, err := encodeFrame(frame, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write(data); err != nil {
		t.Fatal(err)
	}
	if _, err := readFrame(bufio.NewReader(b), 1024); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write(data); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.Done():
	case <-time.After(time.Second):
		t.Fatal("replayed request did not close session")
	}
}

func FuzzFrame(f *testing.F) {
	f.Add(`{"schema":"cordis-go.session/v1","kind":"cancel","id":"h:1","activation":"a"}` + "\n")
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			return
		}
		_, err := readFrame(bufio.NewReader(strings.NewReader(input)), 4096)
		if err == nil && !strings.HasSuffix(input, "\n") {
			t.Fatal("accepted unterminated frame")
		}
	})
}
