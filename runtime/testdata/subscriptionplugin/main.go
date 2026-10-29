package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

var service = contract.Service{Name: "test.events", Major: 1}

type command struct {
	Operation  string                       `json:"operation"`
	ID         string                       `json:"id"`
	Name       string                       `json:"name"`
	Options    contract.SubscriptionOptions `json:"options"`
	SelfCancel bool                         `json:"selfCancel"`
	Fail       bool                         `json:"fail"`
	Handled    bool                         `json:"handled"`
}

func main() {
	definition := contract.Definition{ID: "events", Provides: []contract.Service{service}, Start: start}
	if err := sdk.Run(context.Background(), definition); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func start(ctx context.Context, scope contract.Scope) error {
	subs := map[string]contract.Subscription{}
	calls := map[string]int{}
	listen := func(ctx context.Context, cmd command) error {
		if _, exists := subs[cmd.ID]; exists {
			return contract.Fail(contract.Conflict, "listener label already used")
		}
		var sub contract.Subscription
		var err error
		sub, err = scope.On(ctx, cmd.Name, cmd.Options, func(ctx context.Context, _ json.RawMessage) (contract.EventResult, error) {
			calls[cmd.ID]++
			if cmd.SelfCancel {
				if err := sub.Cancel(ctx); err != nil {
					return contract.EventResult{}, err
				}
			}
			if cmd.Fail {
				return contract.EventResult{}, contract.Fail(contract.Invalid, "listener failed: "+cmd.ID)
			}
			return contract.EventResult{Handled: cmd.Handled, Value: json.RawMessage(`false`)}, nil
		})
		if err == nil {
			subs[cmd.ID] = sub
		}
		return err
	}
	for _, id := range []string{"native-a", "native-b"} {
		if err := listen(ctx, command{ID: id, Name: "mixed", Handled: id == "native-b"}); err != nil {
			return err
		}
	}
	if string(scope.Config()) == `{"fail":true}` {
		return contract.Fail(contract.PluginFailed, "intentional subscription initialization failure")
	}
	return scope.Provide(service, func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		var cmd command
		if err := contract.Decode(input, &cmd); err != nil {
			return nil, err
		}
		switch cmd.Operation {
		case "listen":
			if err := listen(ctx, cmd); err != nil {
				return nil, err
			}
		case "cancel":
			sub := subs[cmd.ID]
			if sub == nil {
				return nil, contract.Fail(contract.Invalid, "unknown listener label")
			}
			if err := sub.Cancel(ctx); err != nil {
				return nil, err
			}
		case "emit":
			if _, err := scope.Emit(ctx, cmd.Name, json.RawMessage(`null`), contract.Sequential); err != nil {
				return nil, err
			}
		case "counts":
		default:
			return nil, contract.Fail(contract.Invalid, "unknown event command")
		}
		return json.Marshal(calls)
	})
}
