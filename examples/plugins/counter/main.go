package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

func main() {
	if err := sdk.Run(context.Background(), sdk.Define(CounterPlugin)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Counter is the service the plugin provides. The contract other processes
// see is derived from this interface and the types it uses.
//
//cordis:service example.counter 1
type Counter interface {
	Execute(ctx context.Context, request Request) (Response, error)
}

type Request struct {
	Operation string `json:"operation"`
	Delta     int64  `json:"delta"`
}

type Response struct {
	Value  int64 `json:"value"`
	Events int64 `json:"events"`
}

type Config struct {
	Initial int64 `json:"initial,omitempty"`
}

// changed is an event anyone in the host may emit.
var changed = contract.NewEventKey[struct{}]("counter.changed")

// counter is called concurrently, so its state is atomic.
type counter struct {
	value, events atomic.Int64
}

//cordis:plugin counter
//cordis:provides Counter
func start(ctx context.Context, s *cordis.Scope, config Config) error {
	c := &counter{}
	c.value.Store(config.Initial)
	if _, err := changed.On(ctx, s, func(context.Context, struct{}) error {
		c.events.Add(1)
		return nil
	}); err != nil {
		return err
	}
	return ProvideCounter(s, c)
}

func (c *counter) Execute(ctx context.Context, request Request) (Response, error) {
	switch request.Operation {
	case "add":
		c.value.Add(request.Delta)
	case "get":
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
	return Response{Value: c.value.Load(), Events: c.events.Load()}, nil
}
