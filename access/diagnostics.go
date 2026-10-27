package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Cyenoch/cordis/contract"
)

// DiagnosticInput is host-only. Raw values reach only an explicitly installed
// redactor, never the output sink under the default policy.
type DiagnosticInput struct {
	Owner   contract.Identity
	Call    string
	Stage   string
	Code    contract.Code
	Err     error
	Payload json.RawMessage
	Summary string
}

type Diagnostic struct {
	Owner   contract.Identity `json:"owner"`
	Call    string            `json:"call,omitempty"`
	Stage   string            `json:"stage"`
	Code    contract.Code     `json:"code"`
	Summary string            `json:"summary"`
}

// Redactor must remove host-specific secrets and paths from its returned summary.
// An error fails closed: no raw record or unredacted fallback is emitted.
type Redactor func(context.Context, DiagnosticInput) (string, error)
type DiagnosticWriter func(context.Context, Diagnostic) error

type SinkOptions struct {
	Write    DiagnosticWriter
	Redact   Redactor
	MaxBytes int
}

type Sink struct{ options SinkOptions }

func NewSink(options SinkOptions) (*Sink, error) {
	if options.Write == nil {
		return nil, contract.Fail(contract.Invalid, "diagnostic writer required")
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = 4096
	}
	if options.MaxBytes < 512 {
		return nil, contract.Fail(contract.Invalid, "diagnostic limit below minimum")
	}
	return &Sink{options: options}, nil
}

func stableCode(code contract.Code) contract.Code {
	switch code {
	case contract.Invalid, contract.Incompatible, contract.NotReady, contract.Denied,
		contract.Timeout, contract.Canceled, contract.Reentrant, contract.PluginFailed,
		contract.Internal, contract.Conflict, contract.Superseded:
		return code
	default:
		return contract.Internal
	}
}

func safeLabel(value string) string {
	if len(value) > 64 {
		return "[redacted]"
	}
	for _, char := range value {
		allowed := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
		if !allowed && char != '-' && char != '_' && char != '.' && char != ':' {
			return "[redacted]"
		}
	}
	return value
}

func safeIdentity(owner contract.Identity) contract.Identity {
	return contract.Identity{
		Package: safeLabel(owner.Package), Entry: safeLabel(owner.Entry),
		Activation: safeLabel(owner.Activation), Digest: safeLabel(owner.Digest),
	}
}

func defaultSummary(code contract.Code) string {
	switch code {
	case contract.Denied:
		return "host access denied"
	case contract.Canceled:
		return "operation canceled"
	case contract.Timeout:
		return "operation timed out"
	case contract.Invalid:
		return "invalid request"
	case contract.Conflict:
		return "state conflict"
	case contract.PluginFailed:
		return "plugin operation failed"
	default:
		return "framework operation failed"
	}
}

func (s *Sink) Emit(ctx context.Context, input DiagnosticInput) error {
	if s == nil || s.options.Write == nil || ctx == nil {
		return contract.Fail(contract.Invalid, "diagnostic sink not configured")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("emit diagnostic: %w", err)
	}
	code := stableCode(input.Code)
	summary := defaultSummary(code)
	if s.options.Redact != nil {
		var err error
		summary, err = s.options.Redact(ctx, input)
		if err != nil {
			return fmt.Errorf("redact diagnostic: %w", err)
		}
	}
	// Reserve room for bounded metadata and JSON escaping (at most six bytes per
	// input byte). Truncation is on UTF-8 boundaries and precedes serialization.
	summary = boundedText(summary, (s.options.MaxBytes-384)/6)
	record := Diagnostic{
		Owner: safeIdentity(input.Owner), Call: safeLabel(input.Call), Stage: safeLabel(input.Stage),
		Code: code, Summary: summary,
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode diagnostic: %w", err)
	}
	if len(encoded) > s.options.MaxBytes {
		// Metadata can consume the budget; retain only the activation correlation.
		record.Owner.Package, record.Owner.Entry, record.Owner.Digest, record.Call = "", "", "", ""
		encoded, err = json.Marshal(record)
		if err != nil {
			return fmt.Errorf("encode compact diagnostic: %w", err)
		}
		if len(encoded) > s.options.MaxBytes {
			return contract.Fail(contract.Invalid, "diagnostic exceeds limit")
		}
	}
	if err := s.options.Write(ctx, record); err != nil {
		return fmt.Errorf("write diagnostic: %w", err)
	}
	return nil
}

func boundedText(value string, limit int) string {
	value = strings.ToValidUTF8(value, "?")
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

// PublicError projects a host error at a guest boundary without copying its
// message or wrapped cause. The original error remains the caller's property.
func (s *Sink) PublicError(err error) error {
	if err == nil {
		return nil
	}
	code := contract.Internal
	var protocolError *contract.Error
	if errors.As(err, &protocolError) {
		code = stableCode(protocolError.Code)
	}
	if errors.Is(err, context.Canceled) {
		code = contract.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code = contract.Timeout
	}
	return contract.Fail(code, defaultSummary(code))
}
