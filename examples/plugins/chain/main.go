package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/schema"
	"github.com/Cyenoch/cordis/sdk"
)

// Set with -ldflags '-X main.rejectInitialization=true' for recovery conformance.
var rejectInitialization string

func main() {
	if err := sdk.Run(context.Background(), provider(), middle(), consumer(), optionalParent()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func optionalParent() contract.Definition {
	return NewOptionalParentDefinition(func(ctx context.Context, scope contract.Scope, settings OptionalParentConfig) error {
		config, err := json.Marshal(MiddleConfig{Label: settings.Label})
		if err != nil {
			return err
		}
		child, err := contract.CreateChild(ctx, scope, contract.Child{ID: "optional", Definition: MiddleDefinitionSpec().ID, Config: config, Isolate: settings.ChildIsolate.Value})
		if err != nil {
			return err
		}
		_, subscribeErr := scope.On(ctx, "chain.optional-ready", contract.SubscriptionOptions{}, func(ctx context.Context, _ json.RawMessage) (contract.EventResult, error) {
			if err := child.Wait(ctx); err != nil {
				return contract.EventResult{}, err
			}
			return contract.EventResult{Handled: true, Value: json.RawMessage(`true`)}, nil
		})
		return subscribeErr
	})
}

type providerServer struct {
	label              string
	leases             map[string]bool
	acquired, released int
}

func provider() contract.Definition {
	return NewProviderDefinition(func(ctx context.Context, scope contract.Scope, settings ProviderConfig) error {
		if rejectInitialization == "true" {
			return contract.Fail(contract.PluginFailed, "intentional version-specific initialization failure")
		}
		if settings.Label.Value == "fail-initialize" {
			return contract.Fail(contract.PluginFailed, "intentional failed initialization")
		}
		if _, err := scope.On(ctx, "chain.handled", contract.SubscriptionOptions{}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
			return contract.EventResult{Handled: true, Value: json.RawMessage(`0`)}, nil
		}); err != nil {
			return err
		}
		if _, err := scope.On(ctx, "chain.handled", contract.SubscriptionOptions{}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
			return contract.EventResult{}, contract.Fail(contract.PluginFailed, "second listener must not run in First")
		}); err != nil {
			return err
		}
		p := &providerServer{label: settings.Label.Value, leases: map[string]bool{}}
		if err := scope.Defer(func(context.Context) error {
			if len(p.leases) != 0 || p.acquired != p.released {
				return fmt.Errorf("provider leaked resources: acquired=%d released=%d live=%d", p.acquired, p.released, len(p.leases))
			}
			return nil
		}); err != nil {
			return err
		}
		return RegisterChainAServer(scope, p)
	})
}
func (p *providerServer) Execute(ctx context.Context, input ChainAExecuteRequest) (ChainAExecuteResponse, error) {
	owner := input.Owner.Value
	switch input.Operation {
	case "acquire":
		if owner == "" || p.leases[owner] {
			return ChainAExecuteResponse{}, errors.New("invalid or duplicate resource owner")
		}
		p.leases[owner] = true
		p.acquired++
	case "release":
		if !p.leases[owner] {
			return ChainAExecuteResponse{}, errors.New("resource not acquired")
		}
		delete(p.leases, owner)
		p.released++
	case "stats":
	case "wait":
		<-ctx.Done()
		return ChainAExecuteResponse{}, ctx.Err()
	case "crash":
		os.Exit(7)
	case "exit":
		os.Exit(0)
	default:
		return ChainAExecuteResponse{}, contract.Fail(contract.Invalid, "unknown operation")
	}
	return ChainAExecuteResponse{Acquired: json.Number(strconv.Itoa(p.acquired)), Released: json.Number(strconv.Itoa(p.released)), Live: json.Number(strconv.Itoa(len(p.leases))), Label: p.label}, nil
}

type middleServer struct {
	upstream *ChainAClient
	leases   map[string]bool
}

func middle() contract.Definition {
	return NewMiddleDefinition(func(ctx context.Context, scope contract.Scope, _ MiddleConfig) error {
		upstream, err := NewChainAClient(scope, MiddleUpstreamConsumer())
		if err != nil {
			return err
		}
		owner := scope.Identity().Activation
		if _, err := upstream.Execute(ctx, ChainAExecuteRequest{Operation: "acquire", Owner: schema.Some(owner)}); err != nil {
			return fmt.Errorf("middle acquire: %w", err)
		}
		m := &middleServer{upstream: upstream, leases: map[string]bool{}}
		if err := scope.Defer(func(ctx context.Context) error {
			if len(m.leases) != 0 {
				return fmt.Errorf("middle still owns %d consumer resources", len(m.leases))
			}
			out, err := upstream.Execute(ctx, ChainAExecuteRequest{Operation: "release", Owner: schema.Some(owner)})
			if err != nil {
				return fmt.Errorf("middle cleanup release: %w", err)
			}
			if out.Live != "0" || out.Acquired != out.Released {
				return fmt.Errorf("middle cleanup left provider resources: %+v", out)
			}
			return nil
		}); err != nil {
			return err
		}
		return RegisterChainBServer(scope, m)
	})
}
func (m *middleServer) Execute(ctx context.Context, input ChainBExecuteRequest) (ChainBExecuteResponse, error) {
	owner := input.Owner.Value
	if input.Operation == "acquire" && (owner == "" || m.leases[owner]) {
		return ChainBExecuteResponse{}, errors.New("invalid or duplicate consumer resource")
	}
	if input.Operation == "release" && !m.leases[owner] {
		return ChainBExecuteResponse{}, errors.New("consumer resource not acquired")
	}
	// Keep the inherited context so C -> B -> A cleanup retains its trusted lineage.
	out, err := m.upstream.Execute(ctx, ChainAExecuteRequest(input))
	if err != nil {
		return ChainBExecuteResponse{}, fmt.Errorf("middle forward: %w", err)
	}
	switch input.Operation {
	case "acquire":
		m.leases[owner] = true
	case "release":
		delete(m.leases, owner)
	}
	return ChainBExecuteResponse(out), nil
}

type consumerServer struct{ upstream *ChainBClient }

func consumer() contract.Definition {
	return NewConsumerDefinition(func(ctx context.Context, scope contract.Scope, _ ConsumerConfig) error {
		upstream, err := NewChainBClient(scope, ConsumerUpstreamConsumer())
		if err != nil {
			return err
		}
		owner := scope.Identity().Activation
		if _, err := upstream.Execute(ctx, ChainBExecuteRequest{Operation: "acquire", Owner: schema.Some(owner)}); err != nil {
			return fmt.Errorf("consumer acquire: %w", err)
		}
		if err := scope.Defer(func(ctx context.Context) error {
			out, err := upstream.Execute(ctx, ChainBExecuteRequest{Operation: "release", Owner: schema.Some(owner)})
			if err != nil {
				return fmt.Errorf("consumer transitive cleanup: %w", err)
			}
			if out.Live != "1" || out.Released == "0" {
				return fmt.Errorf("consumer cleanup did not release through B to A: %+v", out)
			}
			return nil
		}); err != nil {
			return err
		}
		return RegisterChainCServer(scope, &consumerServer{upstream})
	})
}
func (c *consumerServer) Execute(ctx context.Context, input ChainCExecuteRequest) (ChainCExecuteResponse, error) {
	out, err := c.upstream.Execute(ctx, ChainBExecuteRequest(input))
	return ChainCExecuteResponse(out), err
}
