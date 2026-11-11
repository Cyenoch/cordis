package contract_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

type message struct {
	Count uint32 `json:"count"`
}

func TestKeysDecodeStrictlyBeforeCallingTheListener(t *testing.T) {
	app := cordis.New(cordis.Options{})
	defer app.Close(context.Background())
	root := app.Scope()
	event := contract.NewEventKey[message]("typed")
	calls := 0
	cancel, err := event.On(t.Context(), root, func(context.Context, message) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{"count":0,"extra":true}`, `{"count":"0"}`, `[]`} {
		if err := contract.RawEvent("typed").Emit(t.Context(), root, json.RawMessage(data)); err == nil {
			t.Fatalf("listener accepted %s", data)
		}
	}
	if calls != 0 {
		t.Fatal("the listener ran before its payload was decoded")
	}
	if err := event.Emit(t.Context(), root, message{Count: 1}); err != nil || calls != 1 {
		t.Fatalf("emit = %v, calls = %d", err, calls)
	}
	if err := errors.Join(cancel(t.Context()), cancel(t.Context())); err != nil {
		t.Fatalf("canceling twice = %v", err)
	}
	if err := event.EmitParallel(t.Context(), root, message{}); err != nil || calls != 1 {
		t.Fatalf("emit after cancel = %v, calls = %d", err, calls)
	}
}

func TestQueryStopsAtTheFirstAnswerEvenWhenItIsZero(t *testing.T) {
	app := cordis.New(cordis.Options{})
	defer app.Close(context.Background())
	root := app.Scope()
	query := contract.NewQueryKey[message, uint32]("typed")
	first, later := 0, 0
	if _, err := query.Handle(t.Context(), root, func(_ context.Context, in message) (uint32, bool, error) {
		first++
		return in.Count, true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := query.Handle(t.Context(), root, func(context.Context, message) (uint32, bool, error) {
		later++
		return 7, true, nil
	}); err != nil {
		t.Fatal(err)
	}
	out, handled, err := query.Ask(t.Context(), root, message{})
	if err != nil || !handled || out != 0 || first != 1 || later != 0 {
		t.Fatalf("ask = %d, %v, %v; calls %d, %d", out, handled, err, first, later)
	}
	// An event and a query of one name do not meet.
	if err := contract.NewEventKey[message]("typed").Emit(t.Context(), root, message{}); err != nil || first != 1 {
		t.Fatalf("emit = %v, calls %d", err, first)
	}
}

func TestKeysRejectTypesThatCannotCrossAProcess(t *testing.T) {
	app := cordis.New(cordis.Options{})
	defer app.Close(context.Background())
	if _, err := contract.NewEventKey[func()]("bad").On(t.Context(), app.Scope(), func(context.Context, func()) error { return nil }); err == nil {
		t.Fatal("a function payload was accepted")
	}
}

// uplink records what the keys send to the host.
type uplink struct{ seen []string }

func (u *uplink) Subscribe(_ context.Context, _ *cordis.Scope, kind contract.Kind, name string, options contract.SubscriptionOptions, _ contract.EventHandler) (func(context.Context) error, error) {
	u.seen = append(u.seen, "subscribe "+string(kind)+" "+name)
	if options.Once {
		u.seen = append(u.seen, "once")
	}
	return func(context.Context) error { return errors.New("host is gone") }, nil
}

func (u *uplink) Dispatch(_ context.Context, event contract.Event) (contract.EventResult, error) {
	u.seen = append(u.seen, "dispatch "+string(event.Kind)+" "+event.Name+" "+string(event.Mode)+" "+string(event.Input))
	return contract.EventResult{Handled: true, Value: json.RawMessage(`5`)}, nil
}

func TestKeysGoThroughTheUplinkWhenTheRootProvidesOne(t *testing.T) {
	app := cordis.New(cordis.Options{})
	defer app.Close(context.Background())
	up := &uplink{}
	if err := contract.UplinkKey.Provide(app.Scope(), up); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	cordis.Plugin[struct{}]{Name: "plugin", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		cancel, err := contract.NewEventKey[message]("e").On(ctx, s, func(context.Context, message) error { return nil }, cordis.Once)
		if err == nil {
			// A failed cancellation is reported, not swallowed.
			if cancel(ctx) == nil {
				err = errors.New("cancel error was lost")
			}
		}
		if err == nil {
			err = contract.NewEventKey[message]("e").EmitParallel(ctx, s, message{Count: 2})
		}
		if err == nil {
			out, handled, askErr := contract.NewQueryKey[message, uint32]("q").Ask(ctx, s, message{})
			if err = askErr; err == nil && (!handled || out != 5) {
				err = errors.New("wrong answer")
			}
		}
		done <- err
		return nil
	}}.Load(app.Scope(), struct{}{})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	want := []string{"subscribe event e", "once", `dispatch event e parallel {"count":2}`, `dispatch query q first {"count":0}`}
	if len(up.seen) != len(want) {
		t.Fatalf("uplink saw %q", up.seen)
	}
	for i := range want {
		if up.seen[i] != want[i] {
			t.Fatalf("uplink saw %q", up.seen)
		}
	}
}

func TestChainKeyMatchesTheKernelChain(t *testing.T) {
	app := cordis.New(cordis.Options{})
	defer app.Close(context.Background())
	root := app.Scope()
	chain := contract.NewChainKey[message, string]("typed")
	wrap := func(tag string) func(context.Context, message, func(context.Context, message) (string, error)) (string, error) {
		return func(ctx context.Context, in message, next func(context.Context, message) (string, error)) (string, error) {
			in.Count++
			out, err := next(ctx, in)
			return tag + "(" + out + ")", err
		}
	}
	if _, err := chain.Use(t.Context(), root, wrap("first")); err != nil {
		t.Fatal(err)
	}
	cancel, err := chain.Use(t.Context(), root, wrap("second"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chain.Use(t.Context(), root, wrap("front"), cordis.Prepend); err != nil {
		t.Fatal(err)
	}
	final := func(_ context.Context, in message) (string, error) { return string(rune('0' + in.Count)), nil }
	if out, err := chain.Do(t.Context(), root, message{}, final); err != nil || out != "front(first(second(3)))" {
		t.Fatalf("do = %q, %v", out, err)
	}
	if err := cancel(t.Context()); err != nil {
		t.Fatal(err)
	}
	if out, err := chain.Do(t.Context(), root, message{}, final); err != nil || out != "front(first(2))" {
		t.Fatalf("do after cancel = %q, %v", out, err)
	}
	// Input that does not fit the declared type never reaches a middleware.
	if _, err := contract.RawChain("typed").Do(t.Context(), root, json.RawMessage(`{"count":"x"}`),
		func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`""`), nil }); err == nil {
		t.Fatal("a mistyped input was accepted")
	}
}
