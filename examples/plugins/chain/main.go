package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/schema"
	"github.com/Cyenoch/cordis/sdk"
)

// Set with -ldflags '-X main.rejectInitialization=true' for recovery conformance.
var rejectInitialization string

func main() {
	err := sdk.Run(context.Background(),
		sdk.Define(ProviderPlugin(startProvider)), sdk.Define(MiddlePlugin(startMiddle)),
		sdk.Define(ConsumerPlugin(startConsumer)), sdk.Define(OptionalParentPlugin(startOptionalParent)))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Queries the host asks; a zero answer is still an answer.
var (
	optionalReady = contract.NewQueryKey[struct{}, bool]("chain.optional-ready")
	handled       = contract.NewQueryKey[struct{}, int32]("chain.handled")
)

func startOptionalParent(ctx context.Context, s *cordis.Scope, settings OptionalParentConfig) error {
	config, err := json.Marshal(MiddleConfig{Label: settings.Label})
	if err != nil {
		return err
	}
	child, err := sdk.CreateChild(ctx, s, contract.Child{ID: "optional", Definition: MiddleDefinitionSpec().ID, Config: config, Isolate: settings.ChildIsolate.Value})
	if err != nil {
		return err
	}
	_, err = optionalReady.Handle(ctx, s, func(ctx context.Context, _ struct{}) (bool, bool, error) {
		if err := child.Wait(ctx); err != nil {
			return false, false, err
		}
		return true, true, nil
	})
	return err
}

// providerServer is called concurrently.
type providerServer struct {
	label              string
	mu                 sync.Mutex
	leases             map[string]bool
	acquired, released int
}

func startProvider(ctx context.Context, s *cordis.Scope, settings ProviderConfig) error {
	if rejectInitialization == "true" {
		return contract.Fail(contract.PluginFailed, "intentional version-specific initialization failure")
	}
	if settings.Label.Value == "fail-initialize" {
		return contract.Fail(contract.PluginFailed, "intentional failed initialization")
	}
	if _, err := handled.Handle(ctx, s, func(context.Context, struct{}) (int32, bool, error) {
		return 0, true, nil
	}); err != nil {
		return err
	}
	if _, err := handled.Handle(ctx, s, func(context.Context, struct{}) (int32, bool, error) {
		return 0, false, contract.Fail(contract.PluginFailed, "second handler must not run after an answer")
	}); err != nil {
		return err
	}
	p := &providerServer{label: settings.Label.Value, leases: map[string]bool{}}
	s.Defer(func(context.Context) error {
		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.leases) != 0 || p.acquired != p.released {
			return fmt.Errorf("provider leaked resources: acquired=%d released=%d live=%d", p.acquired, p.released, len(p.leases))
		}
		return nil
	})
	return ChainAEndpoint.Provide(s, NewChainAEndpoint(p))
}

func (p *providerServer) Execute(ctx context.Context, input ChainAExecuteRequest) (ChainAExecuteResponse, error) {
	owner := input.Owner.Value
	switch input.Operation {
	case "wait":
		<-ctx.Done()
		return ChainAExecuteResponse{}, ctx.Err()
	case "crash":
		os.Exit(7)
	case "exit":
		os.Exit(0)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
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
	default:
		return ChainAExecuteResponse{}, contract.Fail(contract.Invalid, "unknown operation")
	}
	return ChainAExecuteResponse{Acquired: json.Number(strconv.Itoa(p.acquired)), Released: json.Number(strconv.Itoa(p.released)), Live: json.Number(strconv.Itoa(len(p.leases))), Label: p.label}, nil
}

type middleServer struct {
	upstream ChainA
	mu       sync.Mutex
	leases   map[string]bool
}

// owner names the resources of one activation.
func owner(s *cordis.Scope) string {
	identity, _ := sdk.Identity(s)
	return identity.Activation
}

func startMiddle(ctx context.Context, s *cordis.Scope, _ MiddleConfig) error {
	endpoint, err := ChainAEndpoint.Get(s)
	if err != nil {
		return err
	}
	upstream, err := NewChainAClient(endpoint)
	if err != nil {
		return err
	}
	owner := owner(s)
	if _, err := upstream.Execute(ctx, ChainAExecuteRequest{Operation: "acquire", Owner: schema.Some(owner)}); err != nil {
		return fmt.Errorf("middle acquire: %w", err)
	}
	m := &middleServer{upstream: upstream, leases: map[string]bool{}}
	// The provider is still there while this runs: consumers clean up first.
	s.Defer(func(ctx context.Context) error {
		m.mu.Lock()
		live := len(m.leases)
		m.mu.Unlock()
		if live != 0 {
			return fmt.Errorf("middle still owns %d consumer resources", live)
		}
		out, err := upstream.Execute(ctx, ChainAExecuteRequest{Operation: "release", Owner: schema.Some(owner)})
		if err != nil {
			return fmt.Errorf("middle cleanup release: %w", err)
		}
		if out.Live != "0" || out.Acquired != out.Released {
			return fmt.Errorf("middle cleanup left provider resources: %+v", out)
		}
		return nil
	})
	return ChainBEndpoint.Provide(s, NewChainBEndpoint(m))
}

func (m *middleServer) Execute(ctx context.Context, input ChainBExecuteRequest) (ChainBExecuteResponse, error) {
	owner := input.Owner.Value
	m.mu.Lock()
	held := m.leases[owner]
	m.mu.Unlock()
	if input.Operation == "acquire" && (owner == "" || held) {
		return ChainBExecuteResponse{}, errors.New("invalid or duplicate consumer resource")
	}
	if input.Operation == "release" && !held {
		return ChainBExecuteResponse{}, errors.New("consumer resource not acquired")
	}
	out, err := m.upstream.Execute(ctx, ChainAExecuteRequest(input))
	if err != nil {
		return ChainBExecuteResponse{}, fmt.Errorf("middle forward: %w", err)
	}
	m.mu.Lock()
	switch input.Operation {
	case "acquire":
		m.leases[owner] = true
	case "release":
		delete(m.leases, owner)
	}
	m.mu.Unlock()
	return ChainBExecuteResponse(out), nil
}

type consumerServer struct{ upstream ChainB }

func startConsumer(ctx context.Context, s *cordis.Scope, _ ConsumerConfig) error {
	endpoint, err := ChainBEndpoint.Get(s)
	if err != nil {
		return err
	}
	upstream, err := NewChainBClient(endpoint)
	if err != nil {
		return err
	}
	owner := owner(s)
	if _, err := upstream.Execute(ctx, ChainBExecuteRequest{Operation: "acquire", Owner: schema.Some(owner)}); err != nil {
		return fmt.Errorf("consumer acquire: %w", err)
	}
	s.Defer(func(ctx context.Context) error {
		out, err := upstream.Execute(ctx, ChainBExecuteRequest{Operation: "release", Owner: schema.Some(owner)})
		if err != nil {
			return fmt.Errorf("consumer transitive cleanup: %w", err)
		}
		if out.Live != "1" || out.Released == "0" {
			return fmt.Errorf("consumer cleanup did not release through B to A: %+v", out)
		}
		return nil
	})
	return ChainCEndpoint.Provide(s, NewChainCEndpoint(consumerServer{upstream}))
}

func (c consumerServer) Execute(ctx context.Context, input ChainCExecuteRequest) (ChainCExecuteResponse, error) {
	out, err := c.upstream.Execute(ctx, ChainBExecuteRequest(input))
	return ChainCExecuteResponse(out), err
}
