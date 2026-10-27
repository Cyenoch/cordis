package contract

import (
	"errors"
	"unicode/utf8"
)

type NamespaceOperation string

const (
	NamespaceRead   NamespaceOperation = "read"
	NamespaceWrite  NamespaceOperation = "write"
	NamespaceDelete NamespaceOperation = "delete"
)

// NamespaceRequest never carries an owner, package or storage namespace.
// Value uses the standard JSON []byte base64 representation.
type NamespaceRequest struct {
	Operation NamespaceOperation `json:"operation"`
	Key       string             `json:"key"`
	Value     []byte             `json:"value,omitempty"`
}

func (r NamespaceRequest) Validate() error {
	if r.Key == "" || !utf8.ValidString(r.Key) {
		return Fail(Invalid, "namespace key must be nonempty UTF-8")
	}
	switch r.Operation {
	case NamespaceWrite:
	case NamespaceRead, NamespaceDelete:
		if len(r.Value) != 0 {
			return Fail(Invalid, "namespace value is only valid for write")
		}
	default:
		return Fail(Invalid, "invalid namespace operation")
	}
	return nil
}

type NamespaceOutcome string

const (
	NamespaceOK               NamespaceOutcome = "ok"
	NamespaceMissing          NamespaceOutcome = "missing"
	NamespaceDenied           NamespaceOutcome = "denied"
	NamespaceQuota            NamespaceOutcome = "quota"
	NamespaceNotCommitted     NamespaceOutcome = "not-committed"
	NamespaceCommitted        NamespaceOutcome = "committed"
	NamespaceRecoveryRequired NamespaceOutcome = "recovery-required"
)

// NamespaceResult is a storage reply, not evidence about a lost reply. It never
// serializes local causes; submission-unknown is exclusively a client error.
type NamespaceResult struct {
	Outcome NamespaceOutcome `json:"outcome"`
	Value   []byte           `json:"value,omitempty"`
}

func (r NamespaceResult) Validate(operation NamespaceOperation) error {
	switch operation {
	case NamespaceRead, NamespaceWrite, NamespaceDelete:
	default:
		return Fail(Invalid, "invalid namespace operation")
	}
	switch r.Outcome {
	case NamespaceOK, NamespaceDenied, NamespaceQuota,
		NamespaceNotCommitted, NamespaceRecoveryRequired:
	case NamespaceMissing:
		if operation == NamespaceWrite {
			return Fail(Invalid, "write cannot report a missing key")
		}
	case NamespaceCommitted:
		if operation == NamespaceRead {
			return Fail(Invalid, "read cannot report a committed mutation")
		}
	default:
		return Fail(Invalid, "invalid namespace outcome")
	}
	if len(r.Value) != 0 && (r.Outcome != NamespaceOK || operation != NamespaceRead) {
		return Fail(Invalid, "unexpected namespace result value")
	}
	return nil
}

var ErrNamespaceMissing = errors.New("namespace key not found")
var ErrNamespaceQuota = errors.New("namespace quota exceeded")

func (r NamespaceResult) Err() error {
	switch r.Outcome {
	case NamespaceOK:
		return nil
	case NamespaceMissing:
		return ErrNamespaceMissing
	case NamespaceDenied:
		return Fail(Denied, "namespace access denied")
	case NamespaceQuota:
		return ErrNamespaceQuota
	case NamespaceNotCommitted:
		return &NotCommittedError{}
	case NamespaceCommitted:
		return &NamespaceCommittedError{}
	case NamespaceRecoveryRequired:
		return &NamespaceRecoveryError{}
	default:
		return Fail(Invalid, "invalid namespace outcome")
	}
}

// NotCommittedError is only used with proof that no mutation was committed.
type NotCommittedError struct{ Cause error }

func (e *NotCommittedError) Error() string { return "namespace operation not committed" }
func (e *NotCommittedError) Unwrap() error { return e.Cause }

// NamespaceCommittedError means a complete synchronized record was committed,
// but a subsequent close or directory synchronization failed.
type NamespaceCommittedError struct{ Cause error }

func (e *NamespaceCommittedError) Error() string {
	return "namespace committed but durability sync failed"
}
func (e *NamespaceCommittedError) Unwrap() error { return e.Cause }

// NamespaceRecoveryError means neither rollback nor durable completion could be
// proven. The host must reopen and validate storage before continuing.
type NamespaceRecoveryError struct{ Cause error }

func (e *NamespaceRecoveryError) Error() string { return "namespace requires recovery" }
func (e *NamespaceRecoveryError) Unwrap() error { return e.Cause }

// SubmissionUnknownError must not be treated as permission to retry: invocation
// began but no valid, timely storage reply was obtained.
type SubmissionUnknownError struct{ Cause error }

func (e *SubmissionUnknownError) Error() string { return "namespace submission state unknown" }
func (e *SubmissionUnknownError) Unwrap() error { return e.Cause }
