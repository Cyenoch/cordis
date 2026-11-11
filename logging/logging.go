// Package logging is a cordis plugin that hands every instance a named
// *slog.Logger. Levels can be set by default, per logger name and per scope;
// configuration changes apply without restarting anything.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Cyenoch/cordis"
)

// Service is the key under which Plugin provides the logging service.
var Service = cordis.NewService[*Provider]("logger")

// Name overrides the logger name for instances loaded on a scope. Without
// it the name is the instance's name. The innermost value wins.
var Name = cordis.NewSetting[string](Service)

// Level overrides the level for loggers obtained from a scope, taking
// precedence over the configured levels. The innermost value wins.
var Level = cordis.NewSetting[slog.Level](Service)

// Config configures Plugin. Updating it never restarts the plugin or the
// instances that use it.
type Config struct {
	// Level is the default threshold.
	Level slog.Level
	// Levels sets the threshold per logger name.
	Levels map[string]slog.Level
	// Format of the default output: "text" (default) or "json".
	Format string
	// Buffer is how many recent records Recent keeps. Zero means 1000.
	Buffer int
	// Output receives the default output. Nil means os.Stderr rather than
	// standard output, because native plugins speak their protocol there.
	// It is part of the configuration: an update that leaves it nil goes
	// back to os.Stderr.
	Output io.Writer
}

func (c Config) validate() error {
	if c.Format != "" && c.Format != "text" && c.Format != "json" {
		return fmt.Errorf("logging: format must be \"text\" or \"json\", got %q", c.Format)
	}
	if c.Buffer < 0 {
		return fmt.Errorf("logging: buffer must not be negative")
	}
	return nil
}

// Plugin provides Service and records instance state transitions: failures
// at error level, everything else at debug level.
var Plugin = cordis.Plugin[Config]{
	Name:     "logging",
	Validate: Config.validate,
	Start: func(_ context.Context, s *cordis.Scope, config Config) error {
		p := &Provider{}
		p.apply(config)
		cordis.InstanceChanged.On(s, func(ctx context.Context, change cordis.InstanceChange) error {
			logger := slog.New(&handler{provider: p, name: change.Instance.Name})
			if change.To == cordis.Failed {
				logger.ErrorContext(ctx, "instance failed", "from", change.From.String(), "error", change.Err)
			} else {
				logger.DebugContext(ctx, "instance state", "from", change.From.String(), "to", change.To.String())
			}
			return nil
		})
		if err := cordis.OnUpdate(s, func(_ context.Context, next Config) (bool, error) {
			p.apply(next)
			return true, nil
		}); err != nil {
			return err
		}
		return Service.Provide(s, p)
	},
}

// Get returns the logger for the caller's scope. The instance must list
// Service in its Requires.
func Get(s *cordis.Scope) (*slog.Logger, error) {
	p, err := Service.Get(s)
	if err != nil {
		return nil, err
	}
	return p.Logger(s), nil
}

// Record is a log record kept by Recent.
type Record struct {
	Time    time.Time
	Level   slog.Level
	Name    string
	Message string
	Attrs   []slog.Attr
}

// Provider is the logging service.
type Provider struct {
	state atomic.Pointer[state]

	mu      sync.Mutex
	targets []*target
	recent  []Record // ring; next is the slot to overwrite once full
	next    int
	size    int // capacity of the ring
}

// target is one added handler. Identity is the pointer, so handlers of
// uncomparable types and duplicate registrations are told apart.
type target struct{ handler slog.Handler }

type state struct {
	level  slog.Level
	levels map[string]slog.Level
	output slog.Handler
}

func (p *Provider) apply(config Config) {
	out := config.Output
	if out == nil {
		out = os.Stderr
	}
	// The dispatcher has already filtered by level.
	options := &slog.HandlerOptions{Level: slog.Level(-1 << 30)}
	var output slog.Handler = slog.NewTextHandler(out, options)
	if config.Format == "json" {
		output = slog.NewJSONHandler(out, options)
	}
	size := config.Buffer
	if size == 0 {
		size = 1000
	}
	p.mu.Lock()
	kept := p.recentLocked()
	if len(kept) > size {
		kept = slices.Clone(kept[len(kept)-size:])
	}
	p.recent, p.next, p.size = kept, 0, size
	p.state.Store(&state{level: config.Level, levels: maps.Clone(config.Levels), output: output})
	p.mu.Unlock()
}

// Logger returns the logger for the caller's scope. Its name is the last
// Name setting on the scope, or else the instance's name.
func (p *Provider) Logger(s *cordis.Scope) *slog.Logger {
	h := &handler{provider: p, name: s.Info().Name}
	if names := Name.Values(s); len(names) > 0 {
		h.name = names[len(names)-1]
	}
	if levels := Level.Values(s); len(levels) > 0 {
		h.level = &levels[len(levels)-1]
	}
	return slog.New(h)
}

// AddHandler sends records to h as well, until the returned function is
// called or the scope's instance stops. h receives the records that passed
// the provider's level and may narrow them further through its own Enabled.
// An error from h is written to the default output and does not reach the
// code that logged. h must not log through this provider.
func (p *Provider) AddHandler(s *cordis.Scope, h slog.Handler) (remove func()) {
	added := &target{handler: h}
	p.mu.Lock()
	p.targets = append(slices.Clone(p.targets), added)
	p.mu.Unlock()
	release := s.Defer(func(context.Context) error {
		p.mu.Lock()
		defer p.mu.Unlock()
		if i := slices.Index(p.targets, added); i >= 0 {
			p.targets = slices.Delete(slices.Clone(p.targets), i, i+1)
		}
		return nil
	})
	return func() { release(context.Background()) }
}

// Recent returns the most recent records that passed the level filter,
// oldest first.
func (p *Provider) Recent() []Record {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.recentLocked()
}

func (p *Provider) recentLocked() []Record {
	return append(slices.Clone(p.recent[p.next:]), p.recent[:p.next]...)
}

func (p *Provider) remember(record Record) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.recent) < p.size {
		p.recent = append(p.recent, record)
		return
	}
	p.recent[p.next] = record
	p.next = (p.next + 1) % len(p.recent)
}

// handler is the slog.Handler behind every logger: it filters by level and
// fans a record out to the default output, the buffer and the added handlers.
type handler struct {
	provider *Provider
	name     string
	level    *slog.Level // scope override
	steps    []step      // WithAttrs and WithGroup calls, in order
}

type step struct {
	group string
	attrs []slog.Attr
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	current := h.provider.state.Load()
	threshold := current.level
	if named, ok := current.levels[h.name]; ok {
		threshold = named
	}
	if h.level != nil {
		threshold = *h.level
	}
	return level >= threshold
}

func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	p := h.provider
	current := p.state.Load()
	p.remember(h.keep(record))

	p.mu.Lock()
	targets := p.targets
	p.mu.Unlock()
	for _, target := range targets {
		if !target.handler.Enabled(ctx, record.Level) {
			continue
		}
		if err := h.derive(target.handler).Handle(ctx, record.Clone()); err != nil {
			// Reported outside the logger's groups; nowhere else to report
			// a failure of the default output itself.
			report := slog.NewRecord(record.Time, slog.LevelError, "log handler failed", 0)
			report.AddAttrs(slog.String("logger", h.name), slog.Any("error", err))
			current.output.Handle(ctx, report)
		}
	}
	return h.derive(current.output).Handle(ctx, record)
}

// keep builds the buffered form of a record: the logger's own attributes
// and groups are folded in, with group names joined into the keys, and
// values are resolved so that the buffer does not pin lazy values.
func (h *handler) keep(record slog.Record) Record {
	kept := Record{Time: record.Time, Level: record.Level, Name: h.name, Message: record.Message}
	prefix := ""
	add := func(attr slog.Attr) {
		attr.Value = attr.Value.Resolve()
		attr.Key = prefix + attr.Key
		kept.Attrs = append(kept.Attrs, attr)
	}
	for _, s := range h.steps {
		if s.group != "" {
			prefix += s.group + "."
			continue
		}
		for _, attr := range s.attrs {
			add(attr)
		}
	}
	record.Attrs(func(attr slog.Attr) bool {
		add(attr)
		return true
	})
	return kept
}

// derive applies the logger's name and its WithAttrs/WithGroup calls to a sink.
func (h *handler) derive(sink slog.Handler) slog.Handler {
	sink = sink.WithAttrs([]slog.Attr{slog.String("logger", h.name)})
	for _, s := range h.steps {
		if s.group != "" {
			sink = sink.WithGroup(s.group)
		} else {
			sink = sink.WithAttrs(s.attrs)
		}
	}
	return sink
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	derived := *h
	derived.steps = append(slices.Clone(h.steps), step{attrs: attrs})
	return &derived
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	derived := *h
	derived.steps = append(slices.Clone(h.steps), step{group: name})
	return &derived
}
