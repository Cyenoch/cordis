package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

func main() {
	if err := sdk.Run(context.Background(), sdk.Define(CounterPlugin(start))); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// changed is an event anyone in the host may emit.
var changed = contract.NewEventKey[struct{}]("counter.changed")

// counter is called concurrently, so its state is atomic.
type counter struct {
	value, events atomic.Int64
}

func start(ctx context.Context, s *cordis.Scope, config CounterConfig) error {
	c := &counter{}
	if config.Initial.Present {
		value, err := config.Initial.Value.Float64()
		if err != nil {
			return err
		}
		c.value.Store(int64(value)) // The generated integer codec already enforces the safe integer range.
	}
	if _, err := changed.On(ctx, s, func(context.Context, struct{}) error {
		c.events.Add(1)
		return nil
	}); err != nil {
		return err
	}
	return ExampleCounterEndpoint.Provide(s, NewExampleCounterEndpoint(c))
}

func (c *counter) Execute(ctx context.Context, input ExampleCounterExecuteRequest) (ExampleCounterExecuteResponse, error) {
	out := ExampleCounterExecuteResponse{}
	switch input.Operation {
	case "add":
		delta, err := input.Delta.Float64()
		if err != nil {
			return out, err
		}
		c.value.Add(int64(delta))
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
	out.Value = json.Number(strconv.FormatInt(c.value.Load(), 10))
	out.Events = json.Number(strconv.FormatInt(c.events.Load(), 10))
	return out, nil
}
