package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

type observed struct {
	Identity contract.Identity `json:"identity"`
	Known    bool              `json:"known"`
}

type askRequest struct {
	Via string `json:"via"`
}

var (
	whoService     = contract.Service{Name: "test.who", Major: 1}
	whoKey         = contract.EndpointKey("test.who", 1)
	askService     = contract.Service{Name: "test.ask", Major: 1}
	askKey         = contract.EndpointKey("test.ask", 1)
	askRequirement = contract.Requirement{Name: "test.ask", Major: 1}
)

func consumes(name string) contract.Consumer {
	return contract.Consumer{Requirement: contract.Requirement{Name: name, Major: 1}, Contract: strings.Repeat("ab", 32)}
}

// asker provides test.ask: it asks the host's test.who who is calling, once
// with a call and once with a stream, or has the host relay the question
// to the entry named by Via.
func asker() cordis.Plugin[struct{}] {
	return cordis.Plugin[struct{}]{Name: "main", Requires: []cordis.Requirement{contract.Require(whoKey, 0, 0)},
		Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			host, err := whoKey.Get(s)
			if err != nil {
				return err
			}
			return askKey.Provide(s, contract.Methods(askService, map[string]contract.Handler{
				"ask": contract.Typed(func(ctx context.Context, in askRequest) ([]observed, error) {
					if in.Via != "" {
						return contract.Invoke[askRequest, []observed](ctx, host, "relay", in)
					}
					called, err := contract.Invoke[struct{}, observed](ctx, host, "who", struct{}{})
					if err != nil {
						return nil, err
					}
					streamed := []observed{called}
					for item, err := range contract.InvokeStream[struct{}, observed](ctx, host, "whos", struct{}{}) {
						if err != nil {
							return nil, err
						}
						streamed = append(streamed, item)
					}
					return streamed, nil
				}),
			}))
		}}
}

func TestHostServiceSeesTheRequestingActivation(t *testing.T) {
	f := newFixture(t)
	manifest := contract.Manifest{ID: "test.asker", Definitions: []contract.DefinitionSpec{{ID: "main",
		Requires: []contract.Consumer{consumes("test.who")}, Provides: []contract.Service{askService}}}}
	manifest.Protocol = contract.Protocol{Major: contract.ProtocolMajor, RequiredFeatures: []string{"streams"}}
	f.pack(manifest, sdk.Define(asker()))
	f.open()
	requester := func(ctx context.Context) observed {
		identity, known := Requester(ctx)
		return observed{Identity: identity, Known: known}
	}
	who := cordis.Plugin[struct{}]{Name: "who", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		return whoKey.Provide(s, contract.ServiceMethods{Provides: whoService,
			Calls: map[string]contract.Handler{
				"who": contract.Typed(func(ctx context.Context, _ struct{}) (observed, error) { return requester(ctx), nil }),
				// Asks another entry with the ctx of this request.
				"relay": contract.Typed(func(ctx context.Context, in askRequest) ([]observed, error) {
					view, err := f.host.Caller(in.Via)
					if err != nil {
						return nil, err
					}
					endpoint, err := view.Endpoint(askRequirement)
					if err != nil {
						return nil, err
					}
					return contract.Invoke[askRequest, []observed](ctx, endpoint, "ask", askRequest{})
				}),
			},
			Streams: map[string]contract.StreamHandler{
				"whos": contract.TypedStream(func(ctx context.Context, _ struct{}) iter.Seq2[observed, error] {
					return func(yield func(observed, error) bool) { yield(requester(ctx), nil) }
				}),
			}})
	}}
	must(t, who.Load(f.host.Scope(), struct{}{}).Wait(f.ctx()))
	for _, id := range []string{"left", "right"} {
		must(t, f.enable(contract.Entry{ID: id, Package: "test.asker", Isolate: []string{"test.ask@1"}}).Wait(f.ctx()))
	}
	identity := func(id string) contract.Identity {
		status, err := f.host.Status(id)
		must(t, err)
		return status.Identity
	}
	askOf := func(id, via string) []observed {
		view, err := f.host.Caller(id)
		must(t, err)
		endpoint, err := view.Endpoint(askRequirement)
		must(t, err)
		answers, err := contract.Invoke[askRequest, []observed](f.ctx(), endpoint, "ask", askRequest{Via: via})
		must(t, err)
		return answers
	}

	for _, id := range []string{"left", "right"} {
		answers := askOf(id, "")
		if len(answers) != 2 || answers[0] != (observed{identity(id), true}) || answers[1] != answers[0] {
			t.Fatalf("%s: the host service saw %+v, want %+v for a call and a stream", id, answers, identity(id))
		}
	}
	left, right := identity("left"), identity("right")
	if left.Entry == right.Entry || left.Activation == right.Activation || left.Package != right.Package || left.Digest != right.Digest {
		t.Fatalf("two entries of one package: %+v, %+v", left, right)
	}
	// left's request has the host ask right: right sends the request the
	// host service answers.
	if answers := askOf("left", "right"); len(answers) != 2 || answers[0].Identity != right || answers[1].Identity != right {
		t.Fatalf("a request passed on to right reports %+v", answers)
	}
	// The application's own call.
	out, err := f.host.Call(f.ctx(), contract.Requirement{Name: "test.who", Major: 1}, "who", json.RawMessage(`{}`))
	must(t, err)
	var own observed
	must(t, json.Unmarshal(out, &own))
	if own.Known {
		t.Fatalf("a call of the application reports requester %+v", own.Identity)
	}
}

type loopRequest struct {
	Hold bool `json:"hold"`
}

var (
	loopService = contract.Service{Name: "test.loop", Major: 1}
	loopKey     = contract.EndpointKey("test.loop", 1)
	backKey     = contract.EndpointKey("test.back", 1)
)

// The host, serving a call of a plugin, calls that plugin again.
func TestCallbackIntoTheCallingPlugin(t *testing.T) {
	f := newFixture(t)
	loop := cordis.Plugin[struct{}]{Name: "main", Requires: []cordis.Requirement{contract.Require(backKey, 0, 0)},
		Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			back, err := backKey.Get(s)
			if err != nil {
				return err
			}
			return loopKey.Provide(s, contract.Methods(loopService, map[string]contract.Handler{
				"relay": contract.Typed(func(ctx context.Context, in loopRequest) (string, error) {
					return contract.Invoke[loopRequest, string](ctx, back, "back", in)
				}),
				"leaf": contract.Typed(func(context.Context, struct{}) (string, error) { return "leaf", nil }),
				"hold": contract.Typed(func(ctx context.Context, _ struct{}) (string, error) {
					f.log.add("hold started")
					<-ctx.Done()
					f.log.add("hold ended: " + ctx.Err().Error())
					return "", ctx.Err()
				}),
			}))
		}}
	manifest := contract.Manifest{ID: "test.loop", Definitions: []contract.DefinitionSpec{{ID: "main",
		Requires: []contract.Consumer{consumes("test.back")}, Provides: []contract.Service{loopService}}}}
	f.pack(manifest, sdk.Define(loop))
	f.open()
	back := cordis.Plugin[struct{}]{Name: "back", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		return backKey.Provide(s, contract.Methods(contract.Service{Name: "test.back", Major: 1}, map[string]contract.Handler{
			"back": contract.Typed(func(ctx context.Context, in loopRequest) (string, error) {
				plugin, err := loopKey.Get(f.host.Scope())
				if err != nil {
					return "", err
				}
				if in.Hold {
					return contract.Invoke[struct{}, string](ctx, plugin, "hold", struct{}{})
				}
				// With the ctx of the request being served, and with one
				// that has nothing to do with it.
				within, err := contract.Invoke[struct{}, string](ctx, plugin, "leaf", struct{}{})
				if err != nil {
					return "", err
				}
				unrelated, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				apart, err := contract.Invoke[struct{}, string](unrelated, plugin, "leaf", struct{}{})
				return within + " " + apart, err
			}),
		}))
	}}
	must(t, back.Load(f.host.Scope(), struct{}{}).Wait(f.ctx()))
	must(t, f.enable(contract.Entry{ID: "loop", Package: "test.loop"}).Wait(f.ctx()))
	relay := func(ctx context.Context, hold bool) (string, error) {
		plugin, err := loopKey.Get(f.host.Scope())
		if err != nil {
			return "", err
		}
		return contract.Invoke[loopRequest, string](ctx, plugin, "relay", loopRequest{Hold: hold})
	}

	if out, err := relay(f.ctx(), false); err != nil || out != "leaf leaf" {
		t.Fatalf("relay = %q, %v", out, err)
	}

	// Canceling the outermost call reaches the callback inside the plugin.
	ctx, cancel := context.WithCancel(f.ctx())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := relay(ctx, true); done <- err }()
	eventually(t, "the callback to start", func() bool { return f.log.count("hold started") == 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("the canceled relay = %v", err)
	}
	eventually(t, "the callback to see the cancellation", func() bool { return f.log.count("hold ended: context canceled") == 1 })
	if out, err := relay(f.ctx(), false); err != nil || out != "leaf leaf" {
		t.Fatalf("relay after the cancellation = %q, %v", out, err)
	}
	if f.state("loop") != contract.Active {
		t.Fatalf("the plugin is %s", f.state("loop"))
	}
}
