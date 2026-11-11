package loader_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/loader"
)

// events records what the test plugins observe.
type events struct {
	mu   sync.Mutex
	list []string
}

func (e *events) add(format string, args ...any) {
	e.mu.Lock()
	e.list = append(e.list, fmt.Sprintf(format, args...))
	e.mu.Unlock()
}

func (e *events) get() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.list)
}

func (e *events) count(event string) int {
	n := 0
	for _, got := range e.get() {
		if got == event {
			n++
		}
	}
	return n
}

func (e *events) reset() {
	e.mu.Lock()
	e.list = nil
	e.mu.Unlock()
}

type probeConfig struct {
	Name    string            `json:"name"`
	Port    int               `json:"port"`
	Tags    map[string]string `json:"tags"`
	InPlace bool              `json:"inPlace"`
	Delay   string            `json:"delay"`
}

var (
	DB      = cordis.NewService[string]("db")
	Timeout = cordis.NewSetting[string](DB)
)

type fixture struct {
	t      *testing.T
	dir    string
	file   string
	log    *events
	reg    *loader.Registry
	app    *cordis.App
	inst   *cordis.Instance[loader.Config]
	loader *loader.Loader
	gate   chan struct{} // closed to let "slow" plugins finish starting
}

// probe records its start, stop and in-place updates.
func (f *fixture) probe() cordis.Plugin[probeConfig] {
	return cordis.Plugin[probeConfig]{
		Name: "probe",
		Validate: func(c probeConfig) error {
			if c.Port < 0 {
				return fmt.Errorf("port must not be negative")
			}
			return nil
		},
		Start: func(ctx context.Context, s *cordis.Scope, c probeConfig) error {
			if c.Delay == "gate" {
				select {
				case <-f.gate:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if c.Tags != nil {
				c.Tags[c.Name] = "own"
			}
			f.log.add("start %s port=%d tags=%d", c.Name, c.Port, len(c.Tags))
			s.Defer(func(context.Context) error {
				if c.Delay == "stop" {
					<-f.gate
				}
				f.log.add("stop %s", c.Name)
				return nil
			})
			if c.InPlace {
				return cordis.OnUpdate(s, func(_ context.Context, c probeConfig) (bool, error) {
					f.log.add("update %s port=%d", c.Name, c.Port)
					return true, nil
				})
			}
			return nil
		},
	}
}

func newFixture(t *testing.T, name, content string) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir(), log: &events{}, reg: loader.NewRegistry(), gate: make(chan struct{})}
	f.file = f.write(name, content)
	must(t, loader.Register(f.reg, "probe", f.probe(), loader.Default(func() probeConfig {
		return probeConfig{Port: 8080, Tags: map[string]string{"default": "yes"}}
	})))
	must(t, loader.Register(f.reg, "db", cordis.Plugin[probeConfig]{Name: "db",
		Start: func(_ context.Context, s *cordis.Scope, c probeConfig) error {
			f.log.add("db %s", c.Name)
			return DB.Provide(s, c.Name)
		}}))
	must(t, loader.Register(f.reg, "consumer", cordis.Plugin[probeConfig]{Name: "consumer",
		Requires: []cordis.Requirement{DB},
		Start: func(_ context.Context, s *cordis.Scope, c probeConfig) error {
			db, err := DB.Get(s)
			f.log.add("%s bound %s %v", c.Name, db, Timeout.Values(s))
			return err
		}}))
	must(t, loader.RegisterSetting(f.reg, Timeout))
	return f
}

func (f *fixture) write(name, content string) string {
	f.t.Helper()
	path := filepath.Join(f.dir, name)
	must(f.t, os.WriteFile(path, []byte(strings.TrimLeft(content, "\n")), 0o644))
	return path
}

func (f *fixture) read(name string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	must(f.t, err)
	return string(data)
}

// start loads the loader plugin and waits until the entries have settled.
func (f *fixture) start() *fixture {
	f.t.Helper()
	f.app = cordis.New(cordis.Options{})
	f.t.Cleanup(func() { f.app.Close(context.Background()) })
	f.inst = loader.Plugin.Load(f.app.Scope(), loader.Config{File: f.file, Registry: f.reg})
	must(f.t, f.inst.Wait(f.ctx()))
	if status := f.inst.Status(); status.State != cordis.Active {
		f.t.Fatalf("loader is %v: %v", status.State, status.Err)
	}
	found := make(chan *loader.Loader, 1)
	cordis.Plugin[struct{}]{Name: "grab", Requires: []cordis.Requirement{loader.Service},
		Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			l, err := loader.Service.Get(s)
			found <- l
			return err
		}}.Load(f.app.Scope(), struct{}{})
	f.loader = <-found
	f.settle()
	return f
}

func (f *fixture) ctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	f.t.Cleanup(cancel)
	return ctx
}

func (f *fixture) settle() {
	f.t.Helper()
	must(f.t, f.loader.Wait(f.ctx()))
}

// entry returns the snapshot of one entry.
func (f *fixture) entry(id string) loader.Entry {
	f.t.Helper()
	var find func([]loader.Entry) *loader.Entry
	find = func(list []loader.Entry) *loader.Entry {
		for i := range list {
			if list[i].ID == id {
				return &list[i]
			}
			if found := find(list[i].Children); found != nil {
				return found
			}
		}
		return nil
	}
	found := find(f.loader.Entries())
	if found == nil {
		f.t.Fatalf("no entry %q", id)
	}
	return *found
}

// want compares the recorded events. Entries start concurrently, so the
// order of a run of "start" events is not significant.
func (f *fixture) want(events ...string) {
	f.t.Helper()
	normal := func(list []string) []string {
		list = slices.Clone(list)
		for from := 0; from < len(list); {
			to := from
			for to < len(list) && strings.HasPrefix(list[to], "start ") {
				to++
			}
			slices.Sort(list[from:to])
			from = to + 1
		}
		return list
	}
	if got := normal(f.log.get()); !slices.Equal(got, normal(events)) {
		f.t.Fatalf("events:\n got %q\nwant %q", got, events)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

// eventually polls until ok holds.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func contains(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Fatalf("%q does not contain %q", text, part)
		}
	}
}
