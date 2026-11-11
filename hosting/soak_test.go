package hosting

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

// Callers keep calling a service and reading a stream while their providers
// are disabled and enabled again. A caller may be told the provider is gone;
// it is never left hanging, and every activation that started is cleaned up.
func TestCallsAndStreamsWhileProvidersComeAndGo(t *testing.T) {
	f := newFixture(t)
	var produced atomic.Int64
	f.pack(echoManifest("test.provider", true), sdk.Define(f.echoPlugin("main", "> ")))
	f.pack(countManifest("test.counter", true), sdk.Define(f.counter("main", &produced)))
	f.open()
	entries := []contract.Entry{{ID: "provider", Package: "test.provider"}, {ID: "counter", Package: "test.counter"}}
	for _, entry := range entries {
		must(t, f.enable(entry).Wait(f.ctx()))
	}

	// What a caller may see when its provider goes away under it.
	expected := func(err error) bool {
		_, coded := errors.AsType[*contract.Error](err)
		return coded || errors.Is(err, cordis.ErrUnavailable) || errors.Is(err, cordis.ErrInactive) ||
			errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	}
	ctx, stop := context.WithTimeout(t.Context(), 30*time.Second)
	defer stop()
	churning, cancel := context.WithCancel(ctx)
	var succeeded, turnedAway atomic.Int64
	var callers sync.WaitGroup
	for worker := range 12 {
		callers.Go(func() {
			random := rand.New(rand.NewPCG(uint64(worker), 11))
			for churning.Err() == nil {
				var err error
				if random.IntN(2) == 0 {
					_, err = f.host.Call(ctx, contract.Requirement{Name: "test.echo", Major: 1, MaxMinor: 9}, "echo", []byte(`{"text":"x"}`))
				} else if provider, getErr := countKey.Get(f.host.Scope()); getErr != nil {
					err = getErr
				} else {
					limit := random.IntN(40)
					for _, itemErr := range contract.InvokeStream[countRequest, string](ctx, provider, "count", countRequest{Upto: 60}) {
						if err = itemErr; err != nil {
							break
						}
						if limit--; limit < 0 {
							break
						}
					}
				}
				switch {
				case err == nil:
					succeeded.Add(1)
				case expected(err):
					turnedAway.Add(1)
				default:
					t.Errorf("worker %d: unexpected %T: %v", worker, err, err)
					return
				}
			}
		})
	}
	for round := range 12 {
		entry := entries[round%len(entries)]
		must(t, f.host.Disable(ctx, entry.ID))
		instance, err := f.host.Enable(ctx, entry)
		must(t, err)
		must(t, instance.Wait(ctx))
	}
	cancel()
	callers.Wait()
	if ctx.Err() != nil {
		t.Fatal("callers or the provider churn did not finish in time")
	}
	t.Logf("%d calls succeeded, %d were turned away", succeeded.Load(), turnedAway.Load())
	if succeeded.Load() == 0 {
		t.Fatal("no call ever succeeded")
	}

	// Both providers are back and answer.
	out, err := f.host.Call(f.ctx(), contract.Requirement{Name: "test.echo", Major: 1, MaxMinor: 9}, "echo", []byte(`{"text":"x"}`))
	if err != nil || string(out) != `{"text":"\u003e x"}` {
		t.Fatalf("call after the churn = %s, %v", out, err)
	}
	provider, err := countKey.Get(f.host.Scope())
	must(t, err)
	if items, err := collect(contract.InvokeStream[countRequest, string](f.ctx(), provider, "count", countRequest{Upto: 5})); err != nil || len(items) != 5 {
		t.Fatalf("stream after the churn = %v, %v", items, err)
	}
	// Every activation of the echo provider but the running one was stopped.
	eventually(t, "stopped activations to be cleaned up", func() bool {
		return f.log.count("start main")-f.log.count("stop main") == 1
	})
}

// A caller whose provider stopped under it can tell that from a failure of
// the call itself: the error always has a code.
func TestProviderGoneIsACodedError(t *testing.T) {
	r := &run{identity: contract.Identity{Entry: "provider"}}
	if err := r.gone(io.EOF); !errors.Is(err, contract.Fail(contract.NotReady, "")) {
		t.Fatalf("after an ordinary stop: %v", err)
	}
	r.failure = contract.Fail(contract.PluginFailed, "crashed")
	if err := r.gone(io.EOF); !errors.Is(err, contract.Fail(contract.PluginFailed, "")) {
		t.Fatalf("after a failure: %v", err)
	}
}
