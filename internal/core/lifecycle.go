package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/Cyenoch/cordis/contract"
)

var _ contract.ManagedScope = (*Scope)(nil)
var _ contract.ActivationScope = (*Scope)(nil)

// Go registers explicit concurrent work during Start, without using the service gate.
func (s *Scope) Go(work func(context.Context) error) error {
	if work == nil {
		return contract.Fail(contract.Invalid, "nil managed work")
	}
	a := s.activation
	e := a.instance.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if a.retiring || a.startFinished {
		return contract.Fail(contract.NotReady, "managed work registration is sealed")
	}
	a.managed = append(a.managed, work)
	return nil
}

// OnPublish runs after final registry commit, before ordinary admission opens.
func (s *Scope) OnPublish(hook func(context.Context) error) error {
	if hook == nil {
		return contract.Fail(contract.Invalid, "nil publication hook")
	}
	a := s.activation
	e := a.instance.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if a.retiring || a.startFinished {
		return contract.Fail(contract.NotReady, "publication hook registration is sealed")
	}
	a.publishHooks = append(a.publishHooks, hook)
	return nil
}

// OnRetire also accepts registration by an unfinished, canceled Start so that
// sessions acquired concurrently with retirement still receive cancellation.
func (s *Scope) OnRetire(hook func(context.Context) error) error {
	if hook == nil {
		return contract.Fail(contract.Invalid, "nil retirement hook")
	}
	a := s.activation
	e := a.instance.engine
	e.mu.Lock()
	if a.startFinished {
		e.mu.Unlock()
		return contract.Fail(contract.NotReady, "retirement hook registration is sealed")
	}
	if !a.retireStarted {
		a.retireHooks = append(a.retireHooks, hook)
		e.mu.Unlock()
		return nil
	}
	// Once retirement has begun, registration itself delivers cancellation. Start
	// cannot finish acquiring a session without notifying that late session.
	a.retireWork.Add(1)
	e.mu.Unlock()
	defer a.retireWork.Done()
	return e.invokeRetirement(a, hook)
}

func (e *Engine) publish(a *activation) error {
	ctx := context.WithValue(a.ctx, actorKey{}, a)
	for _, hook := range a.publishHooks {
		e.mu.Lock()
		publishable := a.committed && !a.ready && !a.retiring
		e.mu.Unlock()
		if !publishable {
			return nil
		}
		if err := invokeLifecycle("publication", hook, ctx); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) notifyRetire(a *activation) {
	e.mu.Lock()
	a.retireStarted = true
	hooks := a.retireHooks
	e.mu.Unlock()
	for _, hook := range hooks {
		e.invokeRetirement(a, hook)
	}
}

func (e *Engine) invokeRetirement(a *activation, hook func(context.Context) error) error {
	ctx := context.WithValue(context.Background(), actorKey{}, a)
	err := invokeLifecycle("retirement", hook, ctx)
	if err != nil {
		e.mu.Lock()
		a.retireErr = errors.Join(a.retireErr, err)
		e.mu.Unlock()
	}
	return err
}

func (e *Engine) runManaged(a *activation, work func(context.Context) error) {
	defer a.managedWork.Done()
	e.mu.Lock()
	retiring := a.retiring
	e.mu.Unlock()
	if retiring {
		return
	}
	if err := invokeLifecycle("managed work", func(context.Context) error {
		return work(a.ctx)
	}, context.WithValue(a.ctx, actorKey{}, a)); err != nil {
		if a.ctx.Err() != nil && errors.Is(err, a.ctx.Err()) {
			return
		}
		// Disposal waits for this worker, so its activation is still current.
		if failure := e.FailActivation(a.identity, err); failure != nil {
			panic(fmt.Errorf("fail managed activation %s: %w", a.identity.Activation, failure))
		}
	}
}

func invokeLifecycle(stage string, fn func(context.Context) error, ctx context.Context) (err error) {
	defer recoverCallback(ctx, stage, &err)
	if err := fn(ctx); err != nil {
		return fmt.Errorf("%s: %w", stage, err)
	}
	return nil
}

func recoverCallback(ctx context.Context, stage string, err *error) {
	if value := recover(); value != nil {
		if cause, ok := value.(error); ok {
			*err = fmt.Errorf("%s panic: %w", stage, cause)
		} else {
			*err = fmt.Errorf("%s panic: %v", stage, value)
		}
		*err = capturePanic(ctx, *err)
	}
}
