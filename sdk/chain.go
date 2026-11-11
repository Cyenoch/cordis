package sdk

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"sync"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

// chains is what a guest keeps for the middleware chains it takes part in.
type chains struct {
	mu          sync.Mutex
	sequence    uint64
	middlewares map[string]contract.RawMiddleware // by registration id
	finals      map[string]contract.RawHandler    // by execution token
}

func (g *guest) supportsChains() error {
	if !slices.Contains(g.features, "chains") {
		return contract.Fail(contract.Incompatible, "host lacks session feature: chains")
	}
	return nil
}

// UseChain implements contract.ChainUplink.
func (g *guest) UseChain(ctx context.Context, s *cordis.Scope, name string, options contract.SubscriptionOptions, middleware contract.RawMiddleware) (func(context.Context) error, error) {
	if err := g.supportsChains(); err != nil {
		return nil, err
	}
	g.register.Lock()
	defer g.register.Unlock()
	g.mu.Lock()
	if g.closing {
		g.mu.Unlock()
		return nil, contract.Fail(contract.NotReady, "plugin is closing")
	}
	g.sequence++
	id := g.identity.Activation + "/" + strconv.FormatUint(g.sequence, 10)
	g.mu.Unlock()
	g.chains.mu.Lock()
	g.chains.middlewares[id] = middleware
	g.chains.mu.Unlock()
	forget := func() {
		g.chains.mu.Lock()
		delete(g.chains.middlewares, id)
		g.chains.mu.Unlock()
	}
	spec := contract.SubscriptionSpec{ID: id, Kind: contract.KindChain, Name: name, Options: options}
	if err := g.control(ctx, "chain.use", spec, &struct{}{}); err != nil {
		forget()
		return nil, err
	}
	cancel := func(ctx context.Context) error {
		g.chains.mu.Lock()
		_, registered := g.chains.middlewares[id]
		g.chains.mu.Unlock()
		g.mu.Lock()
		closing := g.closing
		g.mu.Unlock()
		if !registered || closing {
			return nil
		}
		var state contract.SubscriptionState
		if err := g.control(ctx, "chain.unuse", contract.SubscriptionCancel{ID: id}, &state); err != nil {
			return err
		}
		forget()
		return nil
	}
	s.Defer(cancel)
	return cancel, nil
}

// DoChain implements contract.ChainUplink.
func (g *guest) DoChain(ctx context.Context, name string, input json.RawMessage, final contract.RawHandler) (json.RawMessage, error) {
	if err := g.supportsChains(); err != nil {
		return nil, err
	}
	g.chains.mu.Lock()
	g.chains.sequence++
	token := strconv.FormatUint(g.chains.sequence, 10)
	g.chains.finals[token] = final
	g.chains.mu.Unlock()
	defer func() {
		g.chains.mu.Lock()
		delete(g.chains.finals, token)
		g.chains.mu.Unlock()
	}()
	var output json.RawMessage
	err := g.request(ctx, "chain.do", contract.ChainDo{Name: name, Token: token, Input: input}, &output)
	return output, err
}

// invoke runs one of the plugin's middleware for the host.
func (g *guest) invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var request contract.ChainInvoke
	if err := contract.Decode(input, &request); err != nil {
		return nil, err
	}
	if err := g.admit(); err != nil {
		return nil, err
	}
	defer g.active.Done()
	g.chains.mu.Lock()
	middleware := g.chains.middlewares[request.ID]
	g.chains.mu.Unlock()
	if middleware == nil {
		return nil, contract.Fail(contract.NotReady, "middleware is no longer registered")
	}
	// next reaches the host at most once, and not after the middleware has
	// returned: the host has moved on by then.
	var mu sync.Mutex
	called, returned := false, false
	next := func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		refused := called || returned
		late := returned
		called = true
		mu.Unlock()
		if late {
			return nil, contract.Fail(contract.NotReady, "chain: next called after the middleware returned")
		}
		if refused {
			return nil, contract.Fail(contract.Reentrant, "chain: next called more than once")
		}
		var output json.RawMessage
		err := g.request(ctx, "chain.next", contract.ChainStep{Token: request.Token, Input: input}, &output)
		return output, err
	}
	output, err := middleware(ctx, request.Input, next)
	mu.Lock()
	returned = true
	mu.Unlock()
	return output, err
}

// final runs the last step of an execution the plugin started.
func (g *guest) final(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var request contract.ChainStep
	if err := contract.Decode(input, &request); err != nil {
		return nil, err
	}
	if err := g.admit(); err != nil {
		return nil, err
	}
	defer g.active.Done()
	g.chains.mu.Lock()
	final := g.chains.finals[request.Token]
	g.chains.mu.Unlock()
	if final == nil {
		return nil, contract.Fail(contract.NotReady, "chain execution has ended")
	}
	return final(ctx, request.Input)
}
