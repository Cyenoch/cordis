package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

func main() {
	if err := sdk.Run(context.Background(), NewCounterDefinition(start)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type counter struct {
	value, events int64
}

func start(ctx context.Context, scope contract.Scope, config CounterConfig) error {
	c := &counter{}
	if config.Initial.Present {
		value, err := config.Initial.Value.Float64()
		if err != nil {
			return err
		}
		c.value = int64(value) // The generated integer codec already enforces the safe integer range.
	}
	if _, err := scope.On(ctx, "counter.changed", contract.SubscriptionOptions{}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
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
