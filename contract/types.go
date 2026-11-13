// Package contract defines data and lifecycle contracts shared by hosts and guests.
package contract

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"time"
)

const PackageSchema = "cordis-go.package/v1"
const SessionSchema = "cordis-go.session/v1"

// ProtocolMajor is the session protocol this module speaks.
const ProtocolMajor = 1

// Code classifies an error across the session; ErrorCode reads it.
type Code string

const (
	Invalid      Code = "invalid_message"
	Incompatible Code = "incompatible"
	NotReady     Code = "not_ready"
	Denied       Code = "denied"
	Timeout      Code = "timeout"
	Canceled     Code = "canceled"
	Reentrant    Code = "reentrant_call"
	PluginFailed Code = "plugin_failed"
	Internal     Code = "internal"
	Conflict     Code = "conflict"
	Superseded   Code = "superseded"
	Panicked     Code = "panic"
)

// Error is an error with a Code, as it crosses the session.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	cause   error
}

// Error is the code and the message.
func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Unwrap yields the error Wrap classified; nil for one from Fail or decoding.
func (e *Error) Unwrap() error { return e.cause }

// Fail returns an *Error with code and message.
func Fail(code Code, message string) error { return &Error{Code: code, Message: message} }

// Wrap classifies err with code. The message is err's text and err stays
// reachable through errors.Is and errors.As. An *Error that already has
// code is returned as it is; over another code the outer one wins for
// ErrorCode, while errors.Is matches both. Wrap of nil is nil.
func Wrap(code Code, err error) error {
	if err == nil {
		return nil
	}
	if coded, ok := err.(*Error); ok && coded.Code == code {
		return err
	}
	return &Error{Code: code, Message: err.Error(), cause: err}
}

// Is matches an *Error with the same Code, context.Canceled for Canceled and
// context.DeadlineExceeded for Timeout.
func (e *Error) Is(target error) bool {
	if other, ok := target.(*Error); ok {
		return other != nil && e.Code == other.Code
	}
	return (target == context.Canceled && e.Code == Canceled) || (target == context.DeadlineExceeded && e.Code == Timeout)
}

// NewID returns a random 128-bit identifier in hex, as activations use.
func NewID() string {
	var id [16]byte
	_, err := rand.Read(id[:])
	if err != nil {
		panic(fmt.Errorf("activation identity: %w", err))
	}
	return hex.EncodeToString(id[:])
}

// Identity names one activation: its package, entry, activation and the
// digest of the package version it runs.
type Identity struct {
	Package    string `json:"package"`
	Entry      string `json:"entry"`
	Activation string `json:"activation"`
	Digest     string `json:"digest"`
}

// Protocol is a range of session protocol minors within one major, and the
// session features a side requires.
type Protocol struct {
	Major            uint32   `json:"major"`
	MinMinor         uint32   `json:"minMinor"`
	MaxMinor         uint32   `json:"maxMinor"`
	RequiredFeatures []string `json:"requiredFeatures,omitempty"`
}

// Negotiate returns the highest minor both ranges share, or incompatible.
func (p Protocol) Negotiate(other Protocol) (uint32, error) {
	for _, features := range [][]string{p.RequiredFeatures, other.RequiredFeatures} {
		for _, feature := range features {
			switch feature {
			case "cancel", "parent", "events", "children", "subscriptions", "streams", "chains":
			default:
				return 0, Fail(Incompatible, "unsupported required session feature: "+feature)
			}
		}
	}
	if p.Major != other.Major || p.MinMinor > p.MaxMinor || other.MinMinor > other.MaxMinor {
		return 0, Fail(Incompatible, "protocol major or range mismatch")
	}
	low := max(p.MinMinor, other.MinMinor)
	high := min(p.MaxMinor, other.MaxMinor)
	if low > high {
		return 0, Fail(Incompatible, "no common protocol minor")
	}
	return high, nil
}

// Service is a provided service at an exact version.
type Service struct {
	Name  string `json:"name"`
	Major uint32 `json:"major"`
	Minor uint32 `json:"minor"`
}

// Key is name@major, the identity under which providers are found.
func (s Service) Key() string { return fmt.Sprintf("%s@%d", s.Name, s.Major) }

// Requirement is a service a definition needs: one major and a range of
// minors.
type Requirement struct {
	Name     string `json:"name"`
	Major    uint32 `json:"major"`
	MinMinor uint32 `json:"minMinor"`
	MaxMinor uint32 `json:"maxMinor"`
}

// Key is name@major of the required service.
func (r Requirement) Key() string { return Service{Name: r.Name, Major: r.Major}.Key() }

// Accepts reports whether s satisfies the requirement.
func (r Requirement) Accepts(s Service) bool {
	return r.Name == s.Name && r.Major == s.Major && s.Minor >= r.MinMinor && s.Minor <= r.MaxMinor
}

// PackageRef names one installed package version exactly.
type PackageRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// String is the exact reference, id@version#digest, that selects or
// uninstalls this package.
func (r PackageRef) String() string { return r.ID + "@" + r.Version + "#" + r.Digest }

// Dependency is another package a package needs, by ID and version range.
type Dependency struct {
	ID    string `json:"id"`
	Min   string `json:"min,omitempty"`
	Max   string `json:"max,omitempty"`
	Range string `json:"range,omitempty"`
}

// Artifact is the executable of a package for one platform.
type Artifact struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Path string `json:"path"`
}

// RuntimeSpec says how a package runs; Kind is "native".
type RuntimeSpec struct {
	Kind      string     `json:"kind"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

// DefinitionSpec is one plugin definition of a package: its config schema,
// the services it requires and those it provides.
type DefinitionSpec struct {
	ID           string          `json:"id"`
	ConfigSchema json.RawMessage `json:"configSchema"`
	Requires     []Consumer      `json:"requires"`
	Provides     []Service       `json:"provides"`
}

// Manifest is a package's plugin.json.
type Manifest struct {
	Schema               string           `json:"schema"`
	ID                   string           `json:"id"`
	Version              string           `json:"version"`
	Publisher            string           `json:"publisher"`
	Protocol             Protocol         `json:"protocol"`
	Definitions          []DefinitionSpec `json:"definitions"`
	DefaultDefinition    string           `json:"defaultDefinition"`
	Runtime              RuntimeSpec      `json:"runtime"`
	Resources            []string         `json:"resources,omitempty"`
	Dependencies         []Dependency     `json:"dependencies"`
	OptionalDependencies []Dependency     `json:"optionalDependencies"`
}

// State is where an instance stands in its lifecycle.
type State string

const (
	Waiting   State = "waiting"
	Starting  State = "starting"
	Active    State = "active"
	Quiescing State = "quiescing"
	Stopped   State = "stopped"
	Failed    State = "failed"
)

// Status is the observable state of an instance; Waiting says why one has
// not started.
type Status struct {
	Identity         Identity     `json:"identity"`
	State            State        `json:"state"`
	Enabled          bool         `json:"enabled"`
	TemporaryStopped bool         `json:"temporaryStopped"`
	Desired          uint64       `json:"desired"`
	Applied          uint64       `json:"applied"`
	Outcome          Outcome      `json:"outcome"`
	Waiting          []WaitReason `json:"waiting"`
	Error            string       `json:"error,omitempty"`
	Diagnostics      []string     `json:"diagnostics,omitempty"`
}

// WaitReason is one thing a waiting instance waits for: a requirement or a
// parent.
type WaitReason struct {
	Kind        string       `json:"kind"`
	Requirement *Requirement `json:"requirement,omitempty"`
	Parent      string       `json:"parent,omitempty"`
	Visible     []Service    `json:"visible"`
	Reason      string       `json:"reason"`
}

// InstanceInfo is one registered instance as Host.Instances lists it. Origin is
// "persistent" or "dynamic"; a dynamic one has an Owner.
type InstanceInfo struct {
	Entry   Entry      `json:"entry"`
	Package PackageRef `json:"package"`
	Status  Status     `json:"status"`
	Origin  string     `json:"origin"`
	Owner   *Identity  `json:"owner,omitempty"`
}

// RestoreEntry is what Restore did with one saved entry.
type RestoreEntry struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Status Status `json:"status"`
	Error  error  `json:"-"`
}

// RestoreReport lists what Restore did with each saved entry.
type RestoreReport struct {
	Entries []RestoreEntry `json:"entries"`
}

// UpdateResult is the outcome of a configuration update: the revision it
// got, and whether it was applied or a later revision or Stop superseded it.
type UpdateResult struct {
	Revision   uint64
	Applied    bool
	Superseded bool
}

// Dispatch is how an event reaches its listeners.
type Dispatch string

const (
	Sequential Dispatch = "sequential"
	Parallel   Dispatch = "parallel"
	First      Dispatch = "first"
)

// EventResult is what an event emission returned: whether a listener
// handled it, and its value.
type EventResult struct {
	Handled bool            `json:"handled"`
	Value   json.RawMessage `json:"value,omitempty"`
}

// EventHandler serves one event emission.
type EventHandler func(context.Context, json.RawMessage) (EventResult, error)

// Entry fixes ownership and service isolation for one registration.
// Isolate contains complete service keys (name@major). Each key starts a boundary
// inherited by descendants; other keys retain their nearest ancestor boundary.
// Update changes configuration only. Re-register to change these boundaries.
type Entry struct {
	ID         string          `json:"id"`
	Package    string          `json:"package"`
	Definition string          `json:"definition"`
	Parent     string          `json:"parent,omitempty"`
	Config     json.RawMessage `json:"config"`
	Enabled    bool            `json:"enabled"`
	Isolate    []string        `json:"isolate,omitempty"`
}

// Invocation is the host's call of one method of a service the guest
// registered.
type Invocation struct {
	Service Service         `json:"service"`
	Method  string          `json:"method"`
	Input   json.RawMessage `json:"input"`
}

// Initialize starts one activation in a guest. Protocol is the host's
// range; Services are the exact versions bound to the definition's
// requirements; Features are what the host supports.
type Initialize struct {
	FrameBytes int `json:"frameBytes"`
	// Inflight is the session's in-flight limit, for both directions.
	Inflight   int             `json:"inflight"`
	Identity   Identity        `json:"identity"`
	Protocol   Protocol        `json:"protocol"`
	Definition string          `json:"definition"`
	Config     json.RawMessage `json:"config"`
	Services   []Service       `json:"services"`
	Features   []string        `json:"features"`
	// Provides are the services the definition declares; the guest must
	// have published every one of them when it answers.
	Provides []Service `json:"provides"`
}

// Failure is a guest's report that its plugin failed after it had started.
// The guest closes the session right after sending it.
type Failure struct {
	Error Error `json:"error"`
}

// Registration is a guest's answer to Initialize: what it agreed to and the
// services it published.
type Registration struct {
	FrameBytes int       `json:"frameBytes"`
	Minor      uint32    `json:"minor"`
	Features   []string  `json:"features"`
	Services   []Service `json:"services"`
}

// ClosedError is what a caller gets when a session ended before its request
// was answered. Err is why the session ended; io.EOF for an orderly close.
// It tells "the peer is gone" from an error the peer answered with.
type ClosedError struct{ Err error }

// Error is "session closed: " followed by why the session ended.
func (e *ClosedError) Error() string { return "session closed: " + e.Err.Error() }

// Unwrap yields why the session ended.
func (e *ClosedError) Unwrap() error { return e.Err }

// Session is one side of a running session with a peer.
type Session interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
	// CallWithReceipt invokes a non-nil receipt exactly once before awaiting a
	// response: true means queued in session order; false means never sent.
	// The receipt must return promptly and does not acknowledge peer receipt.
	CallWithReceipt(context.Context, string, json.RawMessage, func(bool)) (json.RawMessage, error)
	// Stream calls a method whose results arrive one by one.
	Stream(context.Context, string, json.RawMessage) iter.Seq2[json.RawMessage, error]
	Done() <-chan struct{}
	Err() error
	Close() error
}

// SessionHandler serves the requests of the peer.
type SessionHandler func(context.Context, string, json.RawMessage) (json.RawMessage, error)

// SessionStreamHandler serves the stream requests of the peer.
type SessionStreamHandler func(context.Context, string, json.RawMessage) iter.Seq2[json.RawMessage, error]

const (
	MinFrameBytes = 4096
	MaxFrameBytes = 1 << 20
)

// The in-flight limit is how many requests either side of a session may
// have open at once, streams included. The host chooses it per session and
// tells the guest in Initialize.
const (
	MinInflight     = 16
	DefaultInflight = 64
	MaxInflight     = 1024
)

// ValidateInflight checks an in-flight limit.
func ValidateInflight(value int) error {
	if value < MinInflight || value > MaxInflight {
		return Fail(Invalid, "inflight must be between 16 and 1024")
	}
	return nil
}

// ValidateFrameBytes checks a frame size limit.
func ValidateFrameBytes(value int) error {
	if value < MinFrameBytes || value > MaxFrameBytes {
		return Fail(Invalid, "frameBytes must be between 4096 and 1048576")
	}
	return nil
}

// Launch is what a Runtime needs to start one activation.
type Launch struct {
	Identity   Identity
	Directory  string
	Artifact   string
	Handle     SessionHandler
	Stream     SessionStreamHandler
	FrameBytes int
	// Inflight is the in-flight limit of the session; zero is DefaultInflight.
	Inflight int
	Grace    time.Duration
	// Stderr receives the activation's stderr. A Runtime calls it from one
	// goroutine and, unless it returned an error, ends with a record whose
	// End is set.
	Stderr func(StderrRecord) error
}

// Runtime starts activations. Start's context bounds only the start.
type Runtime interface {
	Start(context.Context, Launch) (Session, error)
}
