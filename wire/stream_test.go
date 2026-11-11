package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

// streamPair is a session whose guest serves streams with serve. limits
// apply to both ends.
func streamPair(t *testing.T, limits Limits, frameBytes int, serve StreamHandler) (*Endpoint, *Endpoint) {
	t.Helper()
	a, b := net.Pipe()
	plain := func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == "initialize" {
			data, _ := json.Marshal(contract.Registration{FrameBytes: frameBytes, Features: []string{}, Services: []contract.Service{}})
			return data, nil
		}
		return json.RawMessage(`"plain"`), nil
	}
	h, err := New(Options{FrameBytes: frameBytes, Transport: a, Identity: contract.Identity{Activation: "sample"}, Handler: plain, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(Options{Transport: b, Guest: true, Handler: plain, Stream: serve, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		h.Close()
		g.Close()
		h.Wait()
		g.Wait()
	})
	input, _ := json.Marshal(contract.Initialize{FrameBytes: frameBytes, Identity: contract.Identity{Activation: "sample"},
		Protocol: contract.Protocol{Major: 1}, Definition: "test", Config: json.RawMessage(`{}`),
		Services: []contract.Service{}, Features: []string{}, Provides: []contract.Service{}})
	if _, err := h.Call(t.Context(), "initialize", input); err != nil {
		t.Fatal(err)
	}
	return h, g
}

// numbers yields 0..n-1 and records how many it produced.
func numbers(n int, produced *atomic.Int64, ended *atomic.Value) StreamHandler {
	return func(ctx context.Context, _ string, _ json.RawMessage) iter.Seq2[json.RawMessage, error] {
		return func(yield func(json.RawMessage, error) bool) {
			defer func() { ended.Store(fmt.Sprint(ctx.Err())) }()
			for i := range n {
				produced.Add(1)
				if !yield(json.RawMessage(fmt.Sprint(i)), nil) {
					return
				}
			}
		}
	}
}

func pendingCount(e *Endpoint) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pending)
}

func TestStreamDeliversItemsUnderBackpressure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var produced atomic.Int64
		var ended atomic.Value
		host, _ := streamPair(t, Limits{}, contract.MaxFrameBytes, numbers(100, &produced, &ended))
		next, stop := iter.Pull2(host.Stream(t.Context(), "numbers", nil))
		defer stop()
		if item, err, ok := next(); !ok || err != nil || string(item) != "0" {
			t.Fatalf("first item = %s, %v, %v", item, err, ok)
		}
		// The consumer pauses, for longer than the request deadline: the
		// producer stops once the window is full, and nothing times out.
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if got := produced.Load(); got > int64(DefaultWindow)+2 || got < int64(DefaultWindow) {
			t.Fatalf("the producer ran %d items ahead of a window of %d", got, DefaultWindow)
		}
		for want := 1; want < 100; want++ {
			item, err, ok := next()
			if !ok || err != nil || string(item) != fmt.Sprint(want) {
				t.Fatalf("item %d = %s, %v, %v", want, item, err, ok)
			}
		}
		if _, err, ok := next(); ok {
			t.Fatalf("the stream did not end: %v", err)
		}
		synctest.Wait()
		if pendingCount(host) != 0 {
			t.Fatal("a finished stream still holds its request slot")
		}
		// Plain calls still work on the session.
		if out, err := host.Call(t.Context(), "plain", nil); err != nil || string(out) != `"plain"` {
			t.Fatalf("call after the stream = %s, %v", out, err)
		}
	})
}

func TestStreamEndsEarlyOrWithAnError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var produced atomic.Int64
		var ended atomic.Value
		host, _ := streamPair(t, Limits{}, contract.MaxFrameBytes, func(ctx context.Context, method string, input json.RawMessage) iter.Seq2[json.RawMessage, error] {
			if method == "failing" {
				return func(yield func(json.RawMessage, error) bool) {
					if yield(json.RawMessage(`1`), nil) && yield(json.RawMessage(`2`), nil) {
						yield(nil, contract.Fail(contract.Denied, "no more"))
					}
				}
			}
			return numbers(1000, &produced, &ended)(ctx, method, input)
		})
		// Stopping early cancels the producer and frees the slot.
		for item, err := range host.Stream(t.Context(), "numbers", nil) {
			if err != nil {
				t.Fatal(err)
			}
			if string(item) == "3" {
				break
			}
		}
		synctest.Wait()
		if ended.Load() != "context canceled" || pendingCount(host) != 0 {
			t.Fatalf("producer ended with %v, %d requests pending", ended.Load(), pendingCount(host))
		}
		// Items precede the error that ends a stream.
		var got []string
		var failure error
		for item, err := range host.Stream(t.Context(), "failing", nil) {
			if err != nil {
				failure = err
				break
			}
			got = append(got, string(item))
		}
		if strings.Join(got, ",") != "1,2" || !errors.Is(failure, contract.Fail(contract.Denied, "")) {
			t.Fatalf("items %v, error %v", got, failure)
		}
		// A canceled consumer.
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		for item, err := range host.Stream(ctx, "numbers", nil) {
			if err != nil {
				failure = err
				break
			}
			if string(item) == "2" {
				cancel()
			}
		}
		if !errors.Is(failure, context.Canceled) {
			t.Fatalf("a canceled stream ended with %v", failure)
		}
	})
}

func TestStreamTimesOutOnlyWhenTheProducerIsSilent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var ended atomic.Value
		host, _ := streamPair(t, Limits{Deadline: 10 * time.Second}, contract.MaxFrameBytes, func(ctx context.Context, _ string, _ json.RawMessage) iter.Seq2[json.RawMessage, error] {
			return func(yield func(json.RawMessage, error) bool) {
				defer func() { ended.Store(fmt.Sprint(ctx.Err())) }()
				// Slow but alive: one item every 8 seconds, then silence.
				for i := range 3 {
					time.Sleep(8 * time.Second)
					if !yield(json.RawMessage(fmt.Sprint(i)), nil) {
						return
					}
				}
				<-ctx.Done()
			}
		})
		var got []string
		var failure error
		for item, err := range host.Stream(t.Context(), "slow", nil) {
			if err != nil {
				failure = err
				break
			}
			got = append(got, string(item))
		}
		if strings.Join(got, ",") != "0,1,2" || !errors.Is(failure, contract.Fail(contract.Timeout, "")) {
			t.Fatalf("items %v, error %v", got, failure)
		}
		synctest.Wait()
		if ended.Load() != "context canceled" {
			t.Fatalf("the silent producer ended with %v", ended.Load())
		}
	})
}

func TestOversizedItemFailsOnlyItsStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host, guest := streamPair(t, Limits{}, contract.MinFrameBytes, func(context.Context, string, json.RawMessage) iter.Seq2[json.RawMessage, error] {
			return func(yield func(json.RawMessage, error) bool) {
				if yield(json.RawMessage(`"small"`), nil) {
					yield(json.RawMessage(`"`+strings.Repeat("x", contract.MinFrameBytes)+`"`), nil)
				}
			}
		})
		var got []string
		var failure error
		for item, err := range host.Stream(t.Context(), "big", nil) {
			if err != nil {
				failure = err
				break
			}
			got = append(got, string(item))
		}
		if len(got) != 1 || !errors.Is(failure, contract.Fail(contract.PluginFailed, "")) {
			t.Fatalf("items %v, error %v", got, failure)
		}
		if _, err := host.Call(t.Context(), "plain", nil); err != nil {
			t.Fatalf("the session did not survive: %v", err)
		}
		select {
		case <-guest.Done():
			t.Fatal("the session ended")
		default:
		}
	})
}

func TestStreamProtocolViolationsEndTheSession(t *testing.T) {
	item := func(id string) Frame {
		return Frame{Schema: contract.SessionSchema, Kind: "item", Activation: "sample", ID: id, Payload: json.RawMessage(`1`)}
	}
	for name, test := range map[string]struct {
		frames func(id string) []Frame
		ends   bool
	}{
		"an item of an unknown request": {func(string) []Frame { return []Frame{item("g:999")} }, true},
		"items beyond the credit": {func(id string) []Frame {
			frames := make([]Frame, DefaultWindow+1)
			for n := range frames {
				frames[n] = item(id)
			}
			return frames
		}, true},
		"credit for a stream that ended": {func(string) []Frame {
			return []Frame{{Schema: contract.SessionSchema, Kind: "credit", Activation: "sample", ID: "h:999", Credit: 1}}
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			left, right := net.Pipe()
			endpoint, err := New(Options{Transport: left, Guest: true, Handler: func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
				data, _ := json.Marshal(contract.Registration{FrameBytes: contract.MaxFrameBytes, Features: []string{}, Services: []contract.Service{}})
				return data, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { endpoint.Close(); right.Close(); endpoint.Wait() })
			peer := json.NewEncoder(right)
			decoder := json.NewDecoder(right)
			init := Frame{Schema: contract.SessionSchema, Kind: "request", Activation: "sample", ID: "h:1", Method: "initialize", DeadlineMS: 1000,
				Payload: testInitialize("sample")}
			if err := peer.Encode(init); err != nil {
				t.Fatal(err)
			}
			var response Frame
			if err := decoder.Decode(&response); err != nil || response.Error != nil {
				t.Fatalf("initialize: %v %+v", err, response)
			}
			// The endpoint starts a stream, so that there is a request to violate.
			// It consumes one item and then pauses, so it grants nothing more.
			go func() {
				next, stop := iter.Pull2(endpoint.Stream(context.Background(), "numbers", nil))
				defer stop()
				next()
				<-endpoint.Done()
			}()
			var request Frame
			if err := decoder.Decode(&request); err != nil || !request.Stream || request.Window != DefaultWindow {
				t.Fatalf("stream request: %v %+v", err, request)
			}
			go func() {
				for _, frame := range test.frames(request.ID) {
					if peer.Encode(frame) != nil {
						return
					}
				}
			}()
			select {
			case <-endpoint.Done():
				if !test.ends {
					t.Fatalf("the session ended: %v", endpoint.Err())
				}
			case <-time.After(200 * time.Millisecond):
				if test.ends {
					t.Fatal("the session survived a protocol violation")
				}
			}
		})
	}
}

func TestChainRequestsHaveNoDeadlineOfTheirOwn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		slow := func(ctx context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
			if method == "initialize" {
				data, _ := json.Marshal(contract.Registration{FrameBytes: contract.MaxFrameBytes, Features: []string{}, Services: []contract.Service{}})
				return data, nil
			}
			select {
			case <-time.After(10 * time.Minute):
				return json.RawMessage(`"done"`), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		a, b := net.Pipe()
		limits := Limits{Deadline: time.Second}
		host, err := New(Options{FrameBytes: contract.MaxFrameBytes, Transport: a, Identity: contract.Identity{Activation: "sample"}, Handler: slow, Limits: limits})
		if err != nil {
			t.Fatal(err)
		}
		guest, err := New(Options{Transport: b, Guest: true, Handler: slow, Limits: limits})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { host.Close(); guest.Close(); host.Wait(); guest.Wait() }()
		if _, err := host.Call(t.Context(), "initialize", testInitialize("sample")); err != nil {
			t.Fatal(err)
		}
		if _, err := host.Call(t.Context(), "call", nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("a plain request that takes too long = %v", err)
		}
		for _, method := range []string{"chain.invoke", "chain.next", "chain.do", "chain.final"} {
			if out, err := host.Call(t.Context(), method, nil); err != nil || string(out) != `"done"` {
				t.Fatalf("%s = %s, %v", method, out, err)
			}
		}
		// The starter's own deadline still applies.
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		if _, err := host.Call(ctx, "chain.do", nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("a chain request past its starter's deadline = %v", err)
		}
	})
}

// Regressions for defects found by review.

func TestStreamThatFinishedSurvivesTheEndOfItsSession(t *testing.T) {
	for range 30 {
		host, guest := streamPair(t, Limits{}, contract.MaxFrameBytes, func(context.Context, string, json.RawMessage) iter.Seq2[json.RawMessage, error] {
			return func(yield func(json.RawMessage, error) bool) {
				for i := range 5 {
					if !yield(json.RawMessage(fmt.Sprint(i)), nil) {
						return
					}
				}
			}
		})
		var got []string
		var failure error
		for item, err := range host.Stream(t.Context(), "five", nil) {
			if err != nil {
				failure = err
				break
			}
			if len(got) == 0 {
				// The whole stream is on this side by now; the session goes.
				time.Sleep(5 * time.Millisecond)
				guest.Close()
				time.Sleep(5 * time.Millisecond)
			}
			got = append(got, string(item))
		}
		if len(got) != 5 || failure != nil {
			t.Fatalf("a stream that had completed delivered %v and ended with %v", got, failure)
		}
	}
}

func TestStoppingAStreamDoesNotWaitForItsProducer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host, guest := streamPair(t, Limits{}, contract.MaxFrameBytes, func(context.Context, string, json.RawMessage) iter.Seq2[json.RawMessage, error] {
			return func(yield func(json.RawMessage, error) bool) {
				for i := range 100 {
					if !yield(json.RawMessage(fmt.Sprint(i)), nil) {
						return
					}
					time.Sleep(time.Minute) // does not watch its context
				}
			}
		})
		for range host.Stream(t.Context(), "stubborn", nil) {
			break
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		select {
		case <-host.Done():
			t.Fatalf("the session ended: %v", host.Err())
		case <-guest.Done():
			t.Fatalf("the session ended: %v", guest.Err())
		default:
		}
		if _, err := host.Call(t.Context(), "plain", nil); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Hour) // let the producer run out
	})
}

func TestCancelIsQueuedBeforeTheSlotIsReused(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	a, b := net.Pipe()
	limits := Limits{Inflight: 1}
	handler := func(ctx context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		switch method {
		case "initialize":
			data, _ := json.Marshal(contract.Registration{FrameBytes: contract.MaxFrameBytes, Features: []string{}, Services: []contract.Service{}})
			return data, nil
		case "block":
			select {
			case <-block:
			case <-ctx.Done():
			}
			return nil, ctx.Err()
		}
		return json.RawMessage(`{}`), nil
	}
	host, err := New(Options{FrameBytes: contract.MaxFrameBytes, Transport: a, Identity: contract.Identity{Activation: "sample"}, Handler: handler, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	guest, err := New(Options{Transport: b, Guest: true, Handler: handler, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { host.Close(); guest.Close(); host.Wait(); guest.Wait() }()
	if _, err := host.Call(t.Context(), "initialize", testInitialize("sample")); err != nil {
		t.Fatal(err)
	}
	for round := range 300 {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			host.Call(ctx, "block", nil)
		}()
		time.Sleep(time.Duration(round%5) * 50 * time.Microsecond)
		cancel()
		<-done
		// The slot of the canceled call is free; the next call must not
		// overtake its cancellation.
		for {
			_, err := host.Call(t.Context(), "plain", nil)
			if err == nil {
				break
			}
			if !errors.Is(err, contract.Fail(contract.NotReady, "")) {
				t.Fatalf("round %d: %v (session: %v)", round, err, guest.Err())
			}
		}
	}
}
