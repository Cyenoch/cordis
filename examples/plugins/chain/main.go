package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

// Set with -ldflags '-X main.rejectInitialization=true' for recovery conformance.
var rejectInitialization string

func main() {
	err := sdk.Run(context.Background(),
		sdk.Define(ProviderPlugin), sdk.Define(MiddlePlugin), sdk.Define(ConsumerPlugin), sdk.Define(OptionalParentPlugin))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Three services with one shape: a provider, a plugin in the middle that
// needs the provider, and a consumer that needs the middle.

//cordis:service chain.a 1.2
type ChainA interface {
	Execute(ctx context.Context, request Request) (Response, error)
}

//cordis:service chain.b 1.2
type ChainB interface {
	Execute(ctx context.Context, request Request) (Response, error)
}

//cordis:service chain.c 1.2
type ChainC interface {
	Execute(ctx context.Context, request Request) (Response, error)
}

type Request struct {
	Operation string `json:"operation"`
	Owner     string `json:"owner,omitempty"`
}

type Response struct {
	Acquired int    `json:"acquired"`
	Released int    `json:"released"`
	Live     int    `json:"live"`
	Label    string `json:"label"`
}

type Settings struct {
	Label string `json:"label,omitempty"`
}

type ParentSettings struct {
	Label        string   `json:"label,omitempty"`
	ChildIsolate []string `json:"childIsolate,omitempty"`
}

// Queries the host asks; a zero answer is still an answer.
var (
	optionalReady = contract.NewQueryKey[struct{}, bool]("chain.optional-ready")
	handled       = contract.NewQueryKey[struct{}, int32]("chain.handled")
)

//cordis:plugin optional-parent
func startOptionalParent(ctx context.Context, s *cordis.Scope, settings ParentSettings) error {
	config, err := json.Marshal(Settings{Label: settings.Label})
	if err != nil {
		return err
	}
	child, err := sdk.CreateChild(ctx, s, contract.Child{ID: "optional", Definition: MiddleDefinitionSpec().ID, Config: config, Isolate: settings.ChildIsolate})
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

//cordis:plugin provider
//cordis:provides ChainA
func startProvider(ctx context.Context, s *cordis.Scope, settings Settings) error {
	if rejectInitialization == "true" {
		return contract.Fail(contract.PluginFailed, "intentional version-specific initialization failure")
	}
	if settings.Label == "fail-initialize" {
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
	p := &providerServer{label: settings.Label, leases: map[string]bool{}}
	s.Defer(func(context.Context) error {
		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.leases) != 0 || p.acquired != p.released {
			return fmt.Errorf("provider leaked resources: acquired=%d released=%d live=%d", p.acquired, p.released, len(p.leases))
		}
		return nil
	})
	return ProvideChainA(s, p)
}

func (p *providerServer) Execute(ctx context.Context, input Request) (Response, error) {
	owner := input.Owner
	switch input.Operation {
	case "wait":
		<-ctx.Done()
		return Response{}, ctx.Err()
	case "crash":
		os.Exit(7)
	case "panic":
		panic("chain provider panicked")
	case "exit":
		os.Exit(0)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch input.Operation {
	case "acquire":
		if owner == "" || p.leases[owner] {
			return Response{}, errors.New("invalid or duplicate resource owner")
		}
		p.leases[owner] = true
		p.acquired++
	case "release":
		if !p.leases[owner] {
			return Response{}, errors.New("resource not acquired")
		}
		delete(p.leases, owner)
		p.released++
	case "stats":
	default:
		return Response{}, contract.Fail(contract.Invalid, "unknown operation")
	}
	return Response{Acquired: p.acquired, Released: p.released, Live: len(p.leases), Label: p.label}, nil
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

//cordis:plugin middle
//cordis:provides ChainB
//cordis:requires ChainA 1-3
func startMiddle(ctx context.Context, s *cordis.Scope, _ Settings) error {
	upstream, err := GetChainA(s)
	if err != nil {
		return err
	}
	owner := owner(s)
	if _, err := upstream.Execute(ctx, Request{Operation: "acquire", Owner: owner}); err != nil {
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
		out, err := upstream.Execute(ctx, Request{Operation: "release", Owner: owner})
		if err != nil {
			return fmt.Errorf("middle cleanup release: %w", err)
		}
		if out.Live != 0 || out.Acquired != out.Released {
			return fmt.Errorf("middle cleanup left provider resources: %+v", out)
		}
		return nil
	})
	return ProvideChainB(s, m)
}

func (m *middleServer) Execute(ctx context.Context, input Request) (Response, error) {
	owner := input.Owner
	m.mu.Lock()
	held := m.leases[owner]
	m.mu.Unlock()
	if input.Operation == "acquire" && (owner == "" || held) {
		return Response{}, errors.New("invalid or duplicate consumer resource")
	}
	if input.Operation == "release" && !held {
		return Response{}, errors.New("consumer resource not acquired")
	}
	out, err := m.upstream.Execute(ctx, input)
	if err != nil {
		return Response{}, fmt.Errorf("middle forward: %w", err)
	}
	m.mu.Lock()
	switch input.Operation {
	case "acquire":
		m.leases[owner] = true
	case "release":
		delete(m.leases, owner)
	}
	m.mu.Unlock()
	return out, nil
}

type consumerServer struct{ upstream ChainB }

//cordis:plugin consumer
//cordis:provides ChainC
//cordis:requires ChainB 1-3
func startConsumer(ctx context.Context, s *cordis.Scope, _ Settings) error {
	upstream, err := GetChainB(s)
	if err != nil {
		return err
	}
	owner := owner(s)
	if _, err := upstream.Execute(ctx, Request{Operation: "acquire", Owner: owner}); err != nil {
		return fmt.Errorf("consumer acquire: %w", err)
	}
	s.Defer(func(ctx context.Context) error {
		out, err := upstream.Execute(ctx, Request{Operation: "release", Owner: owner})
		if err != nil {
			return fmt.Errorf("consumer transitive cleanup: %w", err)
		}
		if out.Live != 1 || out.Released == 0 {
			return fmt.Errorf("consumer cleanup did not release through B to A: %+v", out)
		}
		return nil
	})
	return ProvideChainC(s, consumerServer{upstream})
}

func (c consumerServer) Execute(ctx context.Context, input Request) (Response, error) {
	out, err := c.upstream.Execute(ctx, input)
	return out, err
}
