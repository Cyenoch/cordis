package main

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/schema"
	"github.com/Cyenoch/cordis/sdk"
)

func main() { sdk.Main(NewCounterDefinition(start)) }

type counter struct {
	scope         contract.Scope
	value, events int64
}

func start(_ context.Context, scope contract.Scope, config CounterConfig) error {
	c := &counter{scope: scope}
	if config.Initial.Present {
		value, err := config.Initial.Value.Float64()
		if err != nil {
			return err
		}
		c.value = int64(value) // The generated integer codec already enforces the safe integer range.
	}
	if err := scope.On("counter.changed", func(context.Context, json.RawMessage) (contract.EventResult, error) {
		c.events++
		return contract.EventResult{}, nil
	}); err != nil {
		return err
	}
	return RegisterExampleCounterServer(scope, c)
}

func (c *counter) Execute(ctx context.Context, input ExampleCounterExecuteRequest) (ExampleCounterExecuteResponse, error) {
	out := ExampleCounterExecuteResponse{}
	switch input.Operation {
	case "add":
		delta, err := input.Delta.Float64()
		if err != nil {
			return out, err
		}
		c.value += int64(delta)
	case "get":
	case "host":
		greeting, err := contract.InvokeHost[struct {
			Name string `json:"name"`
		}, struct {
			Message string `json:"message"`
		}](ctx, c.scope, "example.greeting", struct {
			Name string `json:"name"`
		}{Name: "plugin"})
		if err != nil {
			return out, err
		}
		out.Host = schema.Some(greeting.Message)
	case "wait":
		<-ctx.Done()
		return out, ctx.Err()
	case "spin":
		for {
		}
	case "crash":
		panic("intentional plugin crash")
	default:
		return out, contract.Fail(contract.Invalid, "unknown operation")
	}
	out.Value = json.Number(strconv.FormatInt(c.value, 10))
	out.Events = json.Number(strconv.FormatInt(c.events, 10))
	return out, nil
}
