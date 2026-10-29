// Package contract defines data and lifecycle contracts shared by hosts and guests.
package contract

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const PackageSchema = "cordis-go.package/v1"
const SessionSchema = "cordis-go.session/v1"

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

type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string             { return string(e.Code) + ": " + e.Message }
func Fail(code Code, message string) error { return &Error{Code: code, Message: message} }
func (e *Error) Is(target error) bool {
	if other, ok := target.(*Error); ok {
		return other != nil && e.Code == other.Code
	}
	return (target == context.Canceled && e.Code == Canceled) || (target == context.DeadlineExceeded && e.Code == Timeout)
}

func NewID() string {
	var id [16]byte
	_, err := rand.Read(id[:])
	if err != nil {
		panic(fmt.Errorf("activation identity: %w", err))
	}
	return hex.EncodeToString(id[:])
}

type Identity struct {
	Package    string `json:"package"`
	Entry      string `json:"entry"`
	Activation string `json:"activation"`
	Digest     string `json:"digest"`
}

type Protocol struct {
	Major            uint32   `json:"major"`
	MinMinor         uint32   `json:"minMinor"`
	MaxMinor         uint32   `json:"maxMinor"`
	RequiredFeatures []string `json:"requiredFeatures,omitempty"`
}

func (p Protocol) Negotiate(other Protocol) (uint32, error) {
	for _, features := range [][]string{p.RequiredFeatures, other.RequiredFeatures} {
		for _, feature := range features {
			switch feature {
			case "cancel", "parent", "events", "children", "subscriptions":
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

type Service struct {
	Name  string `json:"name"`
	Major uint32 `json:"major"`
	Minor uint32 `json:"minor"`
}

func (s Service) Key() string { return fmt.Sprintf("%s@%d", s.Name, s.Major) }

type Requirement struct {
	Name     string `json:"name"`
	Major    uint32 `json:"major"`
	MinMinor uint32 `json:"minMinor"`
	MaxMinor uint32 `json:"maxMinor"`
}

func (r Requirement) Key() string { return Service{Name: r.Name, Major: r.Major}.Key() }
func (r Requirement) Accepts(s Service) bool {
	return r.Name == s.Name && r.Major == s.Major && s.Minor >= r.MinMinor && s.Minor <= r.MaxMinor
}

type PackageRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type Dependency struct {
	ID    string `json:"id"`
	Min   string `json:"min,omitempty"`
	Max   string `json:"max,omitempty"`
	Range string `json:"range,omitempty"`
}

type Artifact struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Path string `json:"path"`
}

type RuntimeSpec struct {
	Kind      string     `json:"kind"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

type DefinitionSpec struct {
	ID           string          `json:"id"`
	ConfigSchema json.RawMessage `json:"configSchema"`
	Requires     []Consumer      `json:"requires"`
	Provides     []Service       `json:"provides"`
}

type Manifest struct {
	Schema               string           `json:"schema"`
	ID                   string           `json:"id"`
	Version              string           `json:"version"`
	Publisher            string           `json:"publisher"`
	Protocol             Protocol         `json:"protocol"`
	Definitions          []DefinitionSpec `json:"definitions"`
	DefaultDefinition    string           `json:"defaultDefinition"`
	Runtime              RuntimeSpec      `json:"runtime"`
	Dependencies         []Dependency     `json:"dependencies"`
	OptionalDependencies []Dependency     `json:"optionalDependencies"`
}

type State string

const (
	Waiting   State = "waiting"
	Starting  State = "starting"
	Active    State = "active"
	Quiescing State = "quiescing"
	Stopped   State = "stopped"
	Failed    State = "failed"
)

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

type WaitReason struct {
	Kind        string       `json:"kind"`
	Requirement *Requirement `json:"requirement,omitempty"`
	Parent      string       `json:"parent,omitempty"`
	Visible     []Service    `json:"visible"`
	Reason      string       `json:"reason"`
}

type InstanceInfo struct {
	Entry   Entry      `json:"entry"`
	Package PackageRef `json:"package"`
	Status  Status     `json:"status"`
	Origin  string     `json:"origin"`
	Owner   *Identity  `json:"owner,omitempty"`
}

type RestoreEntry struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Status Status `json:"status"`
	Error  error  `json:"-"`
}

type RestoreReport struct {
	Entries []RestoreEntry `json:"entries"`
}

type UpdateResult struct {
	Revision   uint64
	Applied    bool
	Superseded bool
}

type Handler func(context.Context, json.RawMessage) (json.RawMessage, error)
type Cleanup func(context.Context) error

type Dispatch string

const (
	Sequential Dispatch = "sequential"
	Parallel   Dispatch = "parallel"
	First      Dispatch = "first"
)

type EventResult struct {
	Handled bool            `json:"handled"`
	Value   json.RawMessage `json:"value,omitempty"`
}

type EventHandler func(context.Context, json.RawMessage) (EventResult, error)

type Scope interface {
	Identity() Identity
	Config() json.RawMessage
	Context() context.Context
	Defer(Cleanup) error
	Provide(Service, Handler) error
	Call(context.Context, Requirement, json.RawMessage) (json.RawMessage, error)
	On(context.Context, string, SubscriptionOptions, EventHandler) (Subscription, error)
	Emit(context.Context, string, json.RawMessage, Dispatch) ([]EventResult, error)
}

type Definition struct {
	Children []Definition
	ID       string
	Requires []Requirement
	Provides []Service
	Validate func(json.RawMessage) error
	Start    func(context.Context, Scope) error
}

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

type Invocation struct {
	Service Service         `json:"service"`
	Input   json.RawMessage `json:"input"`
	Cleanup bool            `json:"cleanup"`
}

type Initialize struct {
	FrameBytes int             `json:"frameBytes"`
	Identity   Identity        `json:"identity"`
	Protocol   Protocol        `json:"protocol"`
	Definition string          `json:"definition"`
	Config     json.RawMessage `json:"config"`
}

type Registration struct {
	FrameBytes int       `json:"frameBytes"`
	Minor      uint32    `json:"minor"`
	Features   []string  `json:"features"`
	Services   []Service `json:"services"`
}

type Session interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
	// CallWithReceipt invokes a non-nil receipt exactly once before awaiting a
	// response: true means queued in session order; false means never sent.
	// The receipt must return promptly and does not acknowledge peer receipt.
	CallWithReceipt(context.Context, string, json.RawMessage, func(bool)) (json.RawMessage, error)
	Done() <-chan struct{}
	Err() error
	Close() error
}

type SessionHandler func(context.Context, string, json.RawMessage) (json.RawMessage, error)

const (
	MinFrameBytes = 4096
	MaxFrameBytes = 1 << 20
)

func ValidateFrameBytes(value int) error {
	if value < MinFrameBytes || value > MaxFrameBytes {
		return Fail(Invalid, "frameBytes must be between 4096 and 1048576")
	}
	return nil
}

type Launch struct {
	Identity   Identity
	Directory  string
	Artifact   string
	Handle     SessionHandler
	FrameBytes int
	Grace      time.Duration
	Stderr     func(StderrRecord) error
}

type Runtime interface {
	Start(context.Context, Launch) (Session, error)
}
