package hosting

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

// chainCalls is what a run keeps for the middleware chains its guest takes
// part in. The host's kernel chain decides the order and that next runs at
// most once; this only forwards.
type chainCalls struct {
	stops map[string]func() // registered middleware, by id; guarded by run.mu

	mu       sync.Mutex
	sequence uint64
	nexts    map[string]*chainCall // middleware runs in flight, by token
}

// chainCall is one run of a guest's middleware.
type chainCall struct {
	next    contract.RawHandler
	pending sync.WaitGroup // next calls the guest has started
}

// useChain registers a middleware of the guest in the host's kernel.
func (r *run) useChain(spec contract.SubscriptionSpec) error {
	number, err := contract.SubscriptionNumber(r.identity.Activation, spec.ID)
	if err != nil {
		return err
	}
	if spec.Name == "" || spec.Kind != contract.KindChain {
		return contract.Fail(contract.Invalid, "chain name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return contract.Fail(contract.NotReady, "activation is stopping")
	}
	if number <= r.last {
		return contract.Fail(contract.Invalid, "registration sequence already used")
	}
	if len(r.listened)+len(r.chains.stops) >= contract.MaxSubscriptions {
		return contract.Fail(contract.Denied, "registration quota exceeded")
	}
	r.last = number
	var options []cordis.ListenOption
	if spec.Options.Prepend {
		options = append(options, cordis.Prepend)
	}
	stop := contract.RawChain(spec.Name).Use(r.scope, func(ctx context.Context, input json.RawMessage, next contract.RawHandler) (json.RawMessage, error) {
		return r.invokeChain(ctx, spec.ID, input, next)
	}, options...)
	r.chains.stops[spec.ID] = stop
	return nil
}

func (r *run) unuseChain(id string) error {
	if _, err := contract.SubscriptionNumber(r.identity.Activation, id); err != nil {
		return err
	}
	r.mu.Lock()
	stop := r.chains.stops[id]
	delete(r.chains.stops, id)
	r.mu.Unlock()
	if stop != nil {
		stop()
	}
	return nil
}

// invokeChain runs the guest's middleware for one execution.
func (r *run) invokeChain(ctx context.Context, id string, input json.RawMessage, next contract.RawHandler) (json.RawMessage, error) {
	call := &chainCall{next: next}
	r.chains.mu.Lock()
	r.chains.sequence++
	token := strconv.FormatUint(r.chains.sequence, 10)
	r.chains.nexts[token] = call
	r.chains.mu.Unlock()
	var output json.RawMessage
	release, err := r.tryAcquire()
	if err == nil {
		err = r.call(ctx, "chain.invoke", contract.ChainInvoke{ID: id, Token: token, Input: input}, &output)
		release()
	}
	// The token is void from here on. A next the guest had started still
	// runs downstream; wait for it, so that the execution has really ended
	// when its starter gets the result.
	r.chains.mu.Lock()
	delete(r.chains.nexts, token)
	r.chains.mu.Unlock()
	call.pending.Wait()
	if err != nil {
		select {
		case <-r.session.Done():
			err = r.gone(err)
		default:
		}
		return nil, err
	}
	return output, nil
}

// nextChain continues an execution when the guest's middleware calls next.
func (r *run) nextChain(ctx context.Context, step contract.ChainStep) (json.RawMessage, error) {
	r.chains.mu.Lock()
	call := r.chains.nexts[step.Token]
	if call != nil {
		call.pending.Add(1)
	}
	r.chains.mu.Unlock()
	if call == nil {
		return nil, contract.Fail(contract.NotReady, "chain: the middleware call has ended")
	}
	defer call.pending.Done()
	return call.next(ctx, step.Input)
}

// doChain runs a chain for the guest; the final step is the guest's.
func (r *run) doChain(ctx context.Context, request contract.ChainDo) (json.RawMessage, error) {
	if request.Name == "" {
		return nil, contract.Fail(contract.Invalid, "chain name is required")
	}
	return contract.RawChain(request.Name).Do(ctx, r.scope, request.Input, func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		release, err := r.tryAcquire()
		if err != nil {
			return nil, err
		}
		defer release()
		var output json.RawMessage
		err = r.call(ctx, "chain.final", contract.ChainStep{Token: request.Token, Input: input}, &output)
		return output, err
	})
}
