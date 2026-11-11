package cordis

import (
	"errors"
	"fmt"
	"runtime/debug"
)

// Sentinel errors. Kernel errors wrap one of these with the instance and the
// service or event involved, so callers match with errors.Is.
var (
	// ErrInactive reports use of a scope or handle whose activation has ended.
	ErrInactive = errors.New("cordis: scope is no longer active")
	// ErrConflict reports a second provider of a service in one resolution space.
	ErrConflict = errors.New("cordis: service already provided")
	// ErrReentrant reports a wait that could only finish after its own caller returns.
	ErrReentrant = errors.New("cordis: wait would deadlock on the calling callback")
	// ErrSuperseded reports that a newer configuration replaced the awaited one.
	ErrSuperseded = errors.New("cordis: superseded by a newer update")
	// ErrNotRequired reports access to a service the caller has not declared.
	ErrNotRequired = errors.New("cordis: service is not declared in Requires")
	// ErrUnavailable reports that no provider is currently available.
	ErrUnavailable = errors.New("cordis: service is unavailable")
	// ErrTypeMismatch reports one name used with two different Go types.
	ErrTypeMismatch = errors.New("cordis: type mismatch")
	// ErrPanic marks an error recovered from a panic in plugin code.
	ErrPanic = errors.New("cordis: panic")
)

// HandlerError locates a failure in one listener, handler or middleware.
type HandlerError struct {
	Name     string // event, query or chain name
	Instance Info   // instance that registered the callback
	Err      error
}

func (e *HandlerError) Error() string {
	return fmt.Sprintf("%s: handler of %q: %v", e.Name, e.Instance.Name, e.Err)
}

func (e *HandlerError) Unwrap() error { return e.Err }

// guard runs fn and converts a panic into an error naming the stage.
func (a *App) guard(stage, name string, fn func() error) (err error) {
	defer func() {
		value := recover()
		if value == nil {
			return
		}
		if cause, ok := value.(error); ok {
			err = fmt.Errorf("%s of %q: %w: %w", stage, name, ErrPanic, cause)
		} else {
			err = fmt.Errorf("%s of %q: %w: %v", stage, name, ErrPanic, value)
		}
		if a.options.CapturePanic && a.options.Logger != nil {
			a.options.Logger.Error("plugin panic", "instance", name, "stage", stage, "stack", string(debug.Stack()))
		}
	}()
	return fn()
}
