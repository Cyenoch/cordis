package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

type Request struct {
	Operation string `json:"operation"`
	Delta     int    `json:"delta"`
}
type Response struct {
	Value  int    `json:"value"`
	Events int    `json:"events"`
	Host   string `json:"host,omitempty"`
}

func main() {
	err := sdk.Serve(context.Background(), contract.Definition{ID: "counter", Start: func(ctx context.Context, scope contract.Scope) error {
		var config struct {
			Initial int `json:"initial"`
		}
		if err := contract.Decode(scope.Config(), &config); err != nil {
			return err
		}
		value := config.Initial
		events := 0
		if err := scope.On("counter.changed", func(context.Context, json.RawMessage) (contract.EventResult, error) {
			events++
			return contract.EventResult{}, nil
		}); err != nil {
			return err
		}
		return sdk.Provide(scope, contract.Service{Name: "example.counter", Major: 1}, func(ctx context.Context, request Request) (Response, error) {
			switch request.Operation {
			case "add":
				value += request.Delta
			case "get":
			case "host":
				client, ok := scope.(*sdk.Client)
				if !ok {
					return Response{}, fmt.Errorf("external SDK client required")
				}
				data, err := client.InvokeHost(ctx, "example.greeting", json.RawMessage(`{"name":"plugin"}`))
				if err != nil {
					return Response{}, err
				}
				var greeting struct {
					Message string `json:"message"`
				}
				if err := contract.Decode(data, &greeting); err != nil {
					return Response{}, err
				}
				return Response{Value: value, Events: events, Host: greeting.Message}, nil
			case "wait":
				<-ctx.Done()
				return Response{}, ctx.Err()
			case "spin":
				for {
				}
			case "crash":
				panic("intentional plugin crash")
			default:
				return Response{}, contract.Fail(contract.Invalid, "unknown operation")
			}
			return Response{Value: value, Events: events}, nil
		})
	}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
