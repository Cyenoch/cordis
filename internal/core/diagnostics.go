package core

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

type panicFailure struct {
	cause error
	stack []byte
}

func (e *panicFailure) Error() string { return e.cause.Error() }
func (e *panicFailure) Unwrap() error { return e.cause }

func failureCode(err error) contract.Code {
	if err == nil {
		return ""
	}
	if _, ok := errors.AsType[*panicFailure](err); ok {
		return contract.Panicked
	}
	return contract.ErrorCode(err)
}

func (e *Engine) stateLocked(i *instance, next contract.State) {
	if i.state == next {
		return
	}
	previous := i.state
	i.state = next
	if e.hooks.Logger == nil {
		return
	}
	owner, outcome := i.identity, i.outcome
	if i.activation != nil {
		owner, outcome = i.activation.identity, i.activation.outcome
	}
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "plugin state", 0)
	r.AddAttrs(identityAttrs(owner)...)
	r.AddAttrs(slog.String("operation", "lifecycle"), slog.String("from", string(previous)),
		slog.String("stage", string(next)), slog.String("definition", i.entry.Definition))
	if parent := e.instances[i.entry.Parent]; parent != nil && parent.activation != nil {
		r.AddAttrs(slog.String("parentActivation", parent.activation.identity.Activation))
	}
	r.AddAttrs(slog.Duration("startupDuration", outcome.StartupDuration), slog.Any("outcome", outcome))
	if i.failure != nil {
		r.AddAttrs(slog.String("errorCode", string(failureCode(i.failure))), slog.String("error", i.failure.Error()))
		if p, ok := errors.AsType[*panicFailure](i.failure); ok && len(p.stack) > 0 {
			r.AddAttrs(slog.String("stack", string(p.stack)))
		}
	}
	// This batch is flushed synchronously after releasing state/commit locks.
	e.diagnostics = append(e.diagnostics, r)
}

func identityAttrs(owner contract.Identity) []slog.Attr {
	return []slog.Attr{
		slog.String("package", owner.Package), slog.String("entry", owner.Entry),
		slog.String("activation", owner.Activation), slog.String("digest", owner.Digest),
	}
}

func (e *Engine) flushDiagnostics() {
	if e.hooks.Logger == nil {
		return
	}
	e.mu.Lock()
	records := e.diagnostics
	e.diagnostics = nil
	e.mu.Unlock()
	handler := e.hooks.Logger.Handler()
	for _, record := range records {
		ctx := context.Background()
		if handler.Enabled(ctx, record.Level) {
			// slog handlers' errors are diagnostic only, not lifecycle failures.
			_ = handler.Handle(ctx, record)
		}
	}
}

func (e *Engine) callFailure(ctx context.Context, owner contract.Identity, target slog.Attr, err error) {
	if err == nil || e.hooks.Logger == nil {
		return
	}
	attrs := identityAttrs(owner)
	attrs = append(attrs, slog.String("operation", "call"), target,
		slog.String("errorCode", string(failureCode(err))), slog.String("error", err.Error()))
	if p, ok := errors.AsType[*panicFailure](err); ok && len(p.stack) > 0 {
		attrs = append(attrs, slog.String("stack", string(p.stack)))
	}
	e.hooks.Logger.LogAttrs(ctx, slog.LevelError, "plugin call failed", attrs...)
}

func capturePanic(ctx context.Context, cause error) error {
	failure := &panicFailure{cause: cause}
	if a, ok := ctx.Value(actorKey{}).(*activation); ok && a.instance.engine.hooks.CapturePanic {
		failure.stack = debug.Stack()
	}
	return failure
}

// SetOutcome records runtime facts without trusting guest-supplied identity.
func (e *Engine) SetOutcome(owner contract.Identity, outcome contract.Outcome) {
	e.mu.Lock()
	defer e.mu.Unlock()
	i := e.instances[owner.Entry]
	if i != nil && i.activation != nil && i.activation.identity == owner {
		outcome.StartupDuration = i.activation.outcome.StartupDuration
		i.activation.outcome = outcome.Clone()
	}
}
