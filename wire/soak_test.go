package wire

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

// Many callers start, abandon and cancel streams and plain calls at once.
// Whatever each of them sees, the session keeps no request behind and still
// answers afterwards.
func TestSessionSurvivesChurn(t *testing.T) {
	var live atomic.Int64
	serve := func(ctx context.Context, _ string, input json.RawMessage) iter.Seq2[json.RawMessage, error] {
		return func(yield func(json.RawMessage, error) bool) {
			live.Add(1)
			defer live.Add(-1)
			var count int
			if err := json.Unmarshal(input, &count); err != nil {
				yield(nil, err)
				return
			}
			for i := range count {
				if ctx.Err() != nil || !yield(json.RawMessage(fmt.Sprint(i)), nil) {
					return
				}
			}
		}
	}
	// On a machine busy with other tests an answer to a cancellation can
	// take longer than the default second; that is not what this is about.
	host, guest := streamPair(t, Limits{Grace: 30 * time.Second}, contract.MaxFrameBytes, serve)

	const workers, rounds = 48, 40
	var completed, refused atomic.Int64
	var group sync.WaitGroup
	for worker := range workers {
		group.Go(func() {
			random := rand.New(rand.NewPCG(uint64(worker), 7))
			for range rounds {
				ctx, cancel := context.WithCancel(t.Context())
				var err error
				switch random.IntN(4) {
				case 0: // a plain call
					_, err = host.Call(ctx, "plain", nil)
				case 1: // a stream read to its end
					want, got := random.IntN(3*DefaultWindow), 0
					for _, itemErr := range host.Stream(ctx, "numbers", json.RawMessage(fmt.Sprint(want))) {
						if err = itemErr; err != nil {
							break
						}
						got++
					}
					if err == nil && got != want {
						err = fmt.Errorf("stream of %d delivered %d items", want, got)
					}
				case 2: // a stream the consumer leaves early
					for _, itemErr := range host.Stream(ctx, "numbers", json.RawMessage("1000")) {
						err = itemErr
						break
					}
				case 3: // a stream whose context ends while it runs
					seen := 0
					for _, itemErr := range host.Stream(ctx, "numbers", json.RawMessage("1000")) {
						if itemErr != nil {
							if !errors.Is(itemErr, context.Canceled) {
								err = itemErr
							}
							break
						}
						if seen++; seen == 3 {
							cancel()
						}
					}
				}
				cancel()
				switch {
				case err == nil:
					completed.Add(1)
				case errors.Is(err, contract.Fail(contract.NotReady, "")):
					// More callers than the session admits at once: refused, not queued.
					refused.Add(1)
				default:
					t.Errorf("worker %d: %v", worker, err)
				}
			}
		})
	}
	group.Wait()
	if completed.Load() == 0 {
		t.Fatal("every request was refused")
	}
	t.Logf("%d completed, %d refused at the in-flight limit", completed.Load(), refused.Load())

	settled := func() bool {
		guest.mu.Lock()
		defer guest.mu.Unlock()
		return pendingCount(host) == 0 && guest.activeIncoming == 0 && live.Load() == 0
	}
	for deadline := time.Now().Add(5 * time.Second); !settled(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the session kept requests behind: host pending %d, producers %d", pendingCount(host), live.Load())
		}
	}
	if host.Err() != nil || guest.Err() != nil {
		t.Fatalf("the session ended: host %v, guest %v", host.Err(), guest.Err())
	}
	if out, err := host.Call(t.Context(), "plain", nil); err != nil || string(out) != `"plain"` {
		t.Fatalf("call after the churn = %s, %v", out, err)
	}
}

// FuzzPeer plays an arbitrary peer against a host that has a call and a
// stream in flight. Whatever arrives, the host neither panics nor hangs: its
// callers return and the session winds down once the transport closes.
func FuzzPeer(f *testing.F) {
	frame := func(fields string) string {
		return `{"schema":"` + contract.SessionSchema + `","activation":"sample",` + fields + "}\n"
	}
	for _, seed := range []string{
		"",
		frame(`"kind":"response","id":"h:2","method":"plain","payload":"x"`),
		frame(`"kind":"response","id":"h:2","method":"plain","error":{"code":"invalid","message":"no"}`),
		frame(`"kind":"item","id":"h:3","payload":1`) + frame(`"kind":"item","id":"h:3","payload":2`) + frame(`"kind":"response","id":"h:3","method":"numbers","payload":{}`),
		frame(`"kind":"item","id":"h:2","payload":1`),
		frame(`"kind":"item","id":"h:9","payload":1`),
		frame(`"kind":"credit","id":"h:3","credit":4`),
		frame(`"kind":"cancel","id":"g:1"`),
		frame(`"kind":"request","id":"g:1","method":"anything","payload":{},"deadlineMs":50`),
		frame(`"kind":"request","id":"g:1","method":"numbers","payload":{},"deadlineMs":50,"stream":true,"window":1`) + frame(`"kind":"credit","id":"g:1","credit":256`),
		frame(`"kind":"request","id":"h:1","method":"anything","payload":{},"deadlineMs":50`),
		frame(`"kind":"response","id":"h:3","method":"numbers","payload":{}`) + frame(`"kind":"response","id":"h:3","method":"numbers","payload":{}`),
		strings.Repeat(frame(`"kind":"item","id":"h:3","payload":0`), 40),
		"{}\n", "null\n", "\n\n", `{"schema":"x"}` + "\n", strings.Repeat("a", 5000) + "\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 1<<16 {
			t.Skip()
		}
		transport, peer := net.Pipe()
		handler := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"plain"`), nil
		}
		stream := func(context.Context, string, json.RawMessage) iter.Seq2[json.RawMessage, error] {
			return func(yield func(json.RawMessage, error) bool) {
				for yield(json.RawMessage("0"), nil) {
				}
			}
		}
		host, err := New(Options{FrameBytes: contract.MinFrameBytes, Transport: transport, Identity: contract.Identity{Activation: "sample"},
			Handler: handler, Stream: stream, Limits: Limits{Deadline: time.Second, Grace: 50 * time.Millisecond}})
		if err != nil {
			t.Fatal(err)
		}
		// The peer answers initialize like a real guest, then says whatever
		// the fuzzer came up with and hangs up.
		reader := bufio.NewReader(peer)
		initialized := make(chan error, 1)
		go func() {
			input, _ := json.Marshal(contract.Initialize{FrameBytes: contract.MinFrameBytes, Inflight: contract.DefaultInflight, Identity: contract.Identity{Activation: "sample"},
				Protocol: contract.Protocol{Major: 1}, Definition: "test", Config: json.RawMessage(`{}`),
				Services: []contract.Service{}, Features: []string{}, Provides: []contract.Service{}})
			_, err := host.Call(context.Background(), "initialize", input)
			initialized <- err
		}()
		if _, err := reader.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		registration, _ := json.Marshal(contract.Registration{FrameBytes: contract.MinFrameBytes, Features: []string{}, Services: []contract.Service{}})
		if _, err := peer.Write([]byte(frame(`"kind":"response","id":"h:1","method":"initialize","payload":` + string(registration)))); err != nil {
			t.Fatal(err)
		}
		if err := <-initialized; err != nil {
			t.Fatal(err)
		}
		// Everything the host writes from here on is read and dropped.
		go func() {
			for {
				if _, err := reader.ReadString('\n'); err != nil {
					return
				}
			}
		}()
		var callers sync.WaitGroup
		callers.Go(func() { host.Call(context.Background(), "plain", nil) }) // h:2
		callers.Go(func() {                                                  // h:3
			for _, err := range host.Stream(context.Background(), "numbers", nil) {
				if err != nil {
					return
				}
			}
		})
		peer.SetWriteDeadline(time.Now().Add(2 * time.Second))
		peer.Write([]byte(input))
		// Stop the deadline's timer: left armed it fires during a later
		// test, where its goroutine would be counted.
		peer.SetWriteDeadline(time.Time{})
		peer.Close()

		finished := make(chan struct{})
		go func() {
			callers.Wait()
			host.Wait()
			close(finished)
		}()
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Fatal("the host did not wind down after its peer hung up")
		}
	})
}
