package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

// tagged provides test.count; its stream yields the entry's id with each
// number until the consumer goes away, or up to Upto when that is set.
func (f *fixture) tagged() cordis.Plugin[struct{}] {
	return cordis.Plugin[struct{}]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		self, _ := sdk.Identity(s)
		return countKey.Provide(s, contract.ServiceMethods{Provides: countService, Streams: map[string]contract.StreamHandler{
			"count": contract.TypedStream(func(ctx context.Context, in countRequest) iter.Seq2[string, error] {
				return func(yield func(string, error) bool) {
					defer func() { f.log.add(fmt.Sprintf("%s ended: %v", self.Entry, ctx.Err())) }()
					for n := 0; in.Upto == 0 || n < in.Upto; n++ {
						if !yield(fmt.Sprintf("%s %d", self.Entry, n), nil) {
							return
						}
					}
				}
			}),
		}})
	}}
}

func TestEntryViewsGiveEndpointsForClients(t *testing.T) {
	f := newFixture(t)
	f.pack(countManifest("test.counter", true), sdk.Define(f.tagged()))
	f.open()
	enable := func(id string) {
		must(t, f.enable(contract.Entry{ID: id, Package: "test.counter", Isolate: []string{"test.count@1"}}).Wait(f.ctx()))
	}
	enable("left")
	enable("right")
	must(t, f.echoPlugin("root", "root ").Load(f.host.Scope(), struct{}{}).Wait(f.ctx()))
	count := contract.Requirement{Name: "test.count", Major: 1}
	echo := contract.Requirement{Name: "test.echo", Major: 1, MaxMinor: 9}

	if _, err := countKey.Get(f.host.Scope()); err == nil {
		t.Fatal("the root scope sees a provider inside an isolated entry")
	}
	for _, id := range []string{"left", "right"} {
		view, err := f.host.Caller(id)
		must(t, err)
		endpoint, err := view.Endpoint(count)
		must(t, err)
		if endpoint.Service() != countService {
			t.Fatalf("%s: endpoint provides %s", id, endpoint.Service().Key())
		}
		// Canceling the read reaches the stream of that entry's plugin.
		ctx, cancel := context.WithCancel(f.ctx())
		var items []string
		for item, err := range contract.InvokeStream[countRequest, string](ctx, endpoint, "count", countRequest{}) {
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("%s: a canceled stream ended with %v", id, err)
				}
				break
			}
			items = append(items, item)
			if len(items) == 3 {
				cancel()
			}
		}
		cancel()
		for n, item := range items {
			if item != fmt.Sprintf("%s %d", id, n) {
				t.Fatalf("%s: items %q", id, items)
			}
		}
		eventually(t, id+"'s stream to see the cancellation", func() bool { return f.log.count(id+" ended: context canceled") == 1 })
	}

	view, err := f.host.Caller("left")
	must(t, err)
	if _, err := view.Endpoint(contract.Requirement{Name: "test.count", Major: 1, MinMinor: 1, MaxMinor: 2}); !errors.Is(err, contract.Fail(contract.Incompatible, "")) {
		t.Fatalf("a requirement the provider does not meet = %v", err)
	}
	// A service the entry does not isolate resolves as the root sees it.
	if out, err := view.Call(f.ctx(), echo, "echo", json.RawMessage(`{"text":"x"}`)); err != nil || !strings.Contains(string(out), "root x") {
		t.Fatalf("echo through the view = %s, %v", out, err)
	}

	// The view belongs to that registration of the entry.
	must(t, f.host.Remove(f.ctx(), "left"))
	enable("left")
	for _, required := range []contract.Requirement{count, echo} {
		if _, err := view.Endpoint(required); !errors.Is(err, contract.Fail(contract.NotReady, "")) || !strings.Contains(err.Error(), `"left"`) {
			t.Fatalf("an old view resolved %s: %v", required.Key(), err)
		}
	}
	fresh, err := f.host.Caller("left")
	must(t, err)
	endpoint, err := fresh.Endpoint(count)
	must(t, err)
	items, err := collect(contract.InvokeStream[countRequest, string](f.ctx(), endpoint, "count", countRequest{Upto: 2}))
	if err != nil || strings.Join(items, ",") != "left 0,left 1" {
		t.Fatalf("the new registration streamed %q, %v", items, err)
	}
}
