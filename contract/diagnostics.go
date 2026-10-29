package contract

import (
	"context"
	"errors"
	"time"
)

type ExitReason string

const (
	ExitStopped     ExitReason = "requested_stop"
	ExitUnexpected  ExitReason = "unexpected_exit"
	ExitProtocol    ExitReason = "protocol_failure"
	ExitInterrupted ExitReason = "interrupted"
)

// Outcome contains bounded facts, never plugin output or arbitrary error text.
// Forced is independent of Reason: a requested stop can require termination.
// FailureCode and CleanupCode use framework categories; unknown plugin codes
// are classified as PluginFailed while the original error remains available.
type Outcome struct {
	Reason          ExitReason    `json:"reason,omitempty"`
	ExitCode        *int          `json:"exitCode,omitempty"`
	Signal          string        `json:"signal,omitempty"`
	Forced          bool          `json:"forced,omitempty"`
	StartupDuration time.Duration `json:"startupDuration,omitempty"`
	FailureCode     Code          `json:"failureCode,omitempty"`
	CleanupCode     Code          `json:"cleanupCode,omitempty"`
}

func (o Outcome) Clone() Outcome {
	if o.ExitCode != nil {
		code := *o.ExitCode
		o.ExitCode = &code
	}
	return o
}

// ProcessSession exposes facts collected by an operating-system runtime.
// Outcome is final after Done closes; an absent exit code is not exit zero.
type ProcessSession interface {
	Session
	Outcome() Outcome
}

// StderrRecord is sent only to an explicitly configured output sink.
// A truncation record can have no Data and is emitted once per activation.
type StderrRecord struct {
	Owner     Identity `json:"owner"`
	Data      []byte   `json:"data,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
}

// ErrorCode classifies an error for bounded diagnostics. Application-specific
// codes remain in the original error but do not extend the persisted vocabulary.
func ErrorCode(err error) Code {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Timeout
	}
	if coded, ok := errors.AsType[*Error](err); ok {
		switch coded.Code {
		case Invalid, Incompatible, NotReady, Denied, Timeout, Canceled,
			Reentrant, PluginFailed, Internal, Conflict, Superseded, Panicked:
			return coded.Code
		}
	}
	return PluginFailed
}
