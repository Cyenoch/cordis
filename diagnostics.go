package cordis

import (
	"context"
	"errors"
	"slices"

	"github.com/Cyenoch/cordis/access"
	"github.com/Cyenoch/cordis/contract"
)

func (h *Host) diagnose(ctx context.Context, owner contract.Identity, call, stage string, cause error) error {
	if cause == nil || h.options.Sink == nil {
		return cause
	}
	code := contract.Internal
	var coded *contract.Error
	if errors.As(cause, &coded) {
		code = coded.Code
	}
	err := h.options.Sink.Emit(context.WithoutCancel(ctx), access.DiagnosticInput{Owner: owner, Call: call, Stage: stage, Code: code, Err: cause})
	if err != nil {
		h.keepDiagnosticError(errors.Join(cause, err))
	}
	return errors.Join(cause, err)
}

func (h *Host) keepDiagnosticError(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Preserve the earliest failures without recursively invoking a failed sink.
	if len(h.diagnosticErrors) < 64 {
		h.diagnosticErrors = append(h.diagnosticErrors, err)
	}
}

func (h *Host) DiagnosticErrors() []error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.diagnosticErrors)
}

func (h *Host) startBuiltin(ctx context.Context, scope contract.Scope, realm string, definition contract.Definition) error {
	owner := scope.Identity()
	if err := h.policy.Activate(owner, definition.Capabilities); err != nil {
		return err
	}
	if err := scope.Defer(func(context.Context) error { h.onRetire(owner); return nil }); err != nil {
		return err
	}
	for _, capability := range definition.Capabilities {
		if h.options.Authorize == nil {
			continue
		}
		grant, err := h.options.Authorize(ctx, owner, capability)
		if err != nil {
			return err
		}
		if grant != nil {
			if _, err := h.policy.Grant(owner, capability.ID, grant); err != nil {
				return err
			}
		}
	}
	return h.diagnose(ctx, owner, "", "initialize", definition.Start(ctx, &hostScope{Scope: scope, host: h, realm: realm}))
}
