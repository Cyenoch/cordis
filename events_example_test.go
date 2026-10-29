package cordis_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

func ExampleHost_Emit() {
	directory, err := os.MkdirTemp("", "cordis-events-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(directory)
	ctx := context.Background()
	host, err := cordis.Open(cordis.Options{Directory: directory})
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := host.Close(ctx); err != nil {
			panic(err)
		}
	}()
	var subscription contract.Subscription
	instance, err := host.AddBuiltin(ctx, contract.Entry{ID: "listener"}, contract.Definition{Start: func(ctx context.Context, scope contract.Scope) error {
		var err error
		subscription, err = scope.On(ctx, "changed", contract.SubscriptionOptions{Prepend: true}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
			return contract.EventResult{Handled: true, Value: json.RawMessage(`false`)}, nil
		})
		return err
	}})
	if err != nil {
		panic(err)
	}
	if err := instance.Wait(ctx); err != nil {
		panic(err)
	}
	results, err := host.Emit(ctx, "changed", json.RawMessage(`{}`), contract.First)
	if err != nil {
		panic(err)
	}
	fmt.Println(results[0].Handled, string(results[0].Value))
	if err := subscription.Cancel(ctx); err != nil {
		panic(err)
	}
	results, err = host.Emit(ctx, "changed", json.RawMessage(`{}`), contract.First)
	if err != nil {
		panic(err)
	}
	fmt.Println("after cancel:", len(results))
	if err := instance.Stop(ctx); err != nil {
		panic(err)
	}
	fmt.Println(instance.Status().State)
	// Output:
	// true false
	// after cancel: 0
	// stopped
}
