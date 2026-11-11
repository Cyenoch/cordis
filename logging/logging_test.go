package logging_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/logging"
)

// safeBuffer is an output shared by loggers on several goroutines.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// collector is a slog.Handler that records what it receives.
type collector struct {
	mu       sync.Mutex
	level    slog.Level
	fail     error
	messages []string
}

func (c *collector) Enabled(_ context.Context, level slog.Level) bool { return level >= c.level }
func (c *collector) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, r.Message)
	return c.fail
}
func (c *collector) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *collector) WithGroup(string) slog.Handler      { return c }
func (c *collector) got() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.messages, ",")
}

// setup starts the logging plugin writing to a buffer.
func setup(t *testing.T, config logging.Config) (*cordis.App, *cordis.Instance[logging.Config], *safeBuffer) {
	t.Helper()
	out := &safeBuffer{}
	config.Output = out
	app := cordis.New(cordis.Options{})
	inst := logging.Plugin.Load(app.Scope(), config)
	if err := inst.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	return app, inst, out
}

// user loads a plugin that requires logging and hands its logger to use.
func user(t *testing.T, s *cordis.Scope, name string, use func(*cordis.Scope, *slog.Logger)) *cordis.Instance[struct{}] {
	t.Helper()
	inst := cordis.Plugin[struct{}]{Name: name, Requires: []cordis.Requirement{logging.Service},
		Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			logger, err := logging.Get(s)
			if err != nil {
				return err
			}
			use(s, logger)
			return nil
		}}.Load(s, struct{}{})
	if err := inst.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	return inst
}

func TestLoggerNames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app, _, out := setup(t, logging.Config{})
		defer app.Close(context.Background())
		user(t, app.Scope(), "http", func(_ *cordis.Scope, l *slog.Logger) { l.Info("from default name") })
		renamed := logging.Name.With(app.Scope(), "api")
		user(t, renamed, "http", func(_ *cordis.Scope, l *slog.Logger) { l.Info("from setting") })
		for _, want := range []string{`msg="from default name" logger=http`, `msg="from setting" logger=api`} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output lacks %q:\n%s", want, out.String())
			}
		}
	})
}

func TestLevels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app, inst, out := setup(t, logging.Config{Level: slog.LevelInfo, Levels: map[string]slog.Level{"db": slog.LevelDebug}})
		defer app.Close(context.Background())
		var early *slog.Logger
		starts := 0
		user(t, app.Scope(), "db", func(_ *cordis.Scope, l *slog.Logger) { l.Debug("db debug") })
		user(t, app.Scope(), "http", func(_ *cordis.Scope, l *slog.Logger) {
			starts++
			early = l
			l.Debug("http debug")
		})
		group := logging.Level.With(app.Scope(), slog.LevelDebug)
		user(t, group, "inside", func(_ *cordis.Scope, l *slog.Logger) { l.Debug("inside debug") })

		got := out.String()
		if !strings.Contains(got, "db debug") || strings.Contains(got, "http debug") || !strings.Contains(got, "inside debug") {
			t.Fatalf("level filtering by name and scope:\n%s", got)
		}

		if err := inst.Update(t.Context(), logging.Config{Level: slog.LevelDebug, Format: "json", Buffer: 5, Output: out}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		early.Debug("after update")
		if !strings.Contains(out.String(), `"msg":"after update"`) {
			t.Fatalf("an earlier logger did not pick up the new level and format:\n%s", out.String())
		}
		if starts != 1 {
			t.Fatalf("a dependent restarted %d times because of a logging update", starts-1)
		}
		if got := inst.Status().Activations; got != 1 {
			t.Fatalf("the logging plugin restarted: %d activations", got)
		}
	})
}

func TestAddedHandlers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app, _, out := setup(t, logging.Config{})
		defer app.Close(context.Background())
		all, errorsOnly := &collector{level: slog.LevelDebug}, &collector{level: slog.LevelError}
		broken := &collector{level: slog.LevelDebug, fail: errors.New("sink is down")}
		var logger *slog.Logger
		owner := user(t, app.Scope(), "owner", func(s *cordis.Scope, l *slog.Logger) {
			p, err := logging.Service.Get(s)
			if err != nil {
				t.Error(err)
				return
			}
			p.AddHandler(s, broken)
			p.AddHandler(s, all)
			p.AddHandler(s, errorsOnly)
		})
		other := user(t, app.Scope(), "other", func(_ *cordis.Scope, l *slog.Logger) { logger = l })
		logger.Info("first")
		logger.Error("second")
		if all.got() != "first,second" || errorsOnly.got() != "second" {
			t.Fatalf("all = %q, errors only = %q", all.got(), errorsOnly.got())
		}
		if !strings.Contains(out.String(), "sink is down") || !strings.Contains(out.String(), "msg=first") {
			t.Fatalf("a failing handler must be reported and must not stop the default output:\n%s", out.String())
		}
		if err := owner.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		logger.Info("third")
		if all.got() != "first,second" {
			t.Fatalf("handler outlived the instance that added it: %q", all.got())
		}
		_ = other
	})
}

func TestRecentBuffer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app, _, _ := setup(t, logging.Config{Buffer: 3})
		defer app.Close(context.Background())
		var p *logging.Provider
		user(t, app.Scope(), "writer", func(s *cordis.Scope, l *slog.Logger) {
			p, _ = logging.Service.Get(s)
			for _, m := range []string{"1", "2", "3", "4", "5"} {
				l.Info(m)
			}
			l.Debug("filtered out")
		})
		var got []string
		for _, r := range p.Recent() {
			got = append(got, r.Name+":"+r.Message)
		}
		if strings.Join(got, ",") != "writer:3,writer:4,writer:5" {
			t.Fatalf("recent = %v", got)
		}
	})
}

func TestLifecycleRecords(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app, _, out := setup(t, logging.Config{Level: slog.LevelDebug})
		defer app.Close(context.Background())
		type secret struct{ Token string }
		failing := cordis.Plugin[secret]{Name: "broken", Start: func(context.Context, *cordis.Scope, secret) error {
			return errors.New("cannot connect")
		}}
		failing.Load(app.Scope(), secret{Token: "hunter2"}).Wait(t.Context())
		synctest.Wait()
		got := out.String()
		if !strings.Contains(got, "level=ERROR") || !strings.Contains(got, "logger=broken") || !strings.Contains(got, "cannot connect") {
			t.Fatalf("failure not recorded:\n%s", got)
		}
		if !strings.Contains(got, "level=DEBUG") || strings.Contains(got, "hunter2") {
			t.Fatalf("transitions must be at debug level and never include configuration:\n%s", got)
		}
	})
}

func TestDefaultOutputIsStandardError(t *testing.T) {
	stdout, stderr := os.Stdout, os.Stderr
	outFile, errFile := tempFile(t), tempFile(t)
	os.Stdout, os.Stderr = outFile, errFile
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()

	app := cordis.New(cordis.Options{})
	inst := logging.Plugin.Load(app.Scope(), logging.Config{})
	if err := inst.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	user(t, app.Scope(), "p", func(_ *cordis.Scope, l *slog.Logger) { l.Info("hello") })
	if err := app.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := read(t, outFile); got != "" {
		t.Fatalf("standard output received %q", got)
	}
	if got := read(t, errFile); !strings.Contains(got, "hello") {
		t.Fatalf("standard error received %q", got)
	}
}

func TestConfigValidation(t *testing.T) {
	app := cordis.New(cordis.Options{})
	defer app.Close(context.Background())
	if err := logging.Plugin.Load(app.Scope(), logging.Config{Format: "xml"}).Wait(t.Context()); err == nil {
		t.Fatal("an unknown format was accepted")
	}
}

func tempFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stream")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func read(t *testing.T, f *os.File) string {
	t.Helper()
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Regressions for defects found by review.

// valueHandler is a handler of an uncomparable type.
type valueHandler struct{ seen *[]string }

func (valueHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h valueHandler) Handle(_ context.Context, r slog.Record) error {
	*h.seen = append(*h.seen, r.Message)
	return nil
}
func (h valueHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h valueHandler) WithGroup(string) slog.Handler      { return h }

func TestUncomparableHandlerIsRemoved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app, _, _ := setup(t, logging.Config{})
		defer app.Close(context.Background())
		var seen []string
		var logger *slog.Logger
		owner := user(t, app.Scope(), "owner", func(s *cordis.Scope, l *slog.Logger) {
			p, _ := logging.Service.Get(s)
			remove := p.AddHandler(s, valueHandler{seen: &seen})
			p.AddHandler(s, valueHandler{seen: &seen})
			remove()
			logger = l
		})
		logger.Info("once")
		if err := owner.Stop(t.Context()); err != nil {
			t.Fatalf("stop = %v", err)
		}
		logger.Info("after stop")
		if strings.Join(seen, ",") != "once" {
			t.Fatalf("seen = %v, want the record once, from the handler that was not removed", seen)
		}
	})
}

func TestRecentKeepsLoggerAttributes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app, _, _ := setup(t, logging.Config{})
		defer app.Close(context.Background())
		var p *logging.Provider
		user(t, app.Scope(), "x", func(s *cordis.Scope, l *slog.Logger) {
			p, _ = logging.Service.Get(s)
			l.With("request", "r1").WithGroup("g").Info("hello", "k", "v")
		})
		var keys []string
		for _, attr := range p.Recent()[0].Attrs {
			keys = append(keys, attr.Key+"="+attr.Value.String())
		}
		if strings.Join(keys, ",") != "request=r1,g.k=v" {
			t.Fatalf("buffered attributes = %v", keys)
		}
	})
}

func TestScopeLevelOverridesNameLevel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		levels := map[string]slog.Level{"db": slog.LevelDebug}
		app, _, out := setup(t, logging.Config{Level: slog.LevelInfo, Levels: levels})
		defer app.Close(context.Background())
		levels["db"] = slog.LevelError // the caller's map is not the provider's
		quiet := logging.Level.With(app.Scope(), slog.LevelError)
		user(t, quiet, "db", func(_ *cordis.Scope, l *slog.Logger) { l.Debug("suppressed by the scope") })
		user(t, app.Scope(), "db", func(_ *cordis.Scope, l *slog.Logger) { l.Debug("allowed by the name") })
		got := out.String()
		if strings.Contains(got, "suppressed by the scope") || !strings.Contains(got, "allowed by the name") {
			t.Fatalf("precedence is scope, then name, then default:\n%s", got)
		}
	})
}
