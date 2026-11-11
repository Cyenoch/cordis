package loader_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/loader"
)

func TestReloadOnlyTouchesWhatChanged(t *testing.T) {
	const content = `
plugins:
  - plugin: probe
    config: {name: anonymous}
  - id: a
    plugin: probe
    config: {name: a, inPlace: true}
`
	f := newFixture(t, "app.yml", content).start()
	f.write("app.yml", strings.Replace(content, "inPlace: true", "inPlace: true, port: 7", 1))
	must(t, f.loader.Reload(f.ctx()))
	f.settle()
	f.want("start anonymous port=8080 tags=2", "start a port=8080 tags=2", "update a port=7")
	must(t, f.loader.Reload(f.ctx())) // nothing changed
	f.settle()
	f.want("start anonymous port=8080 tags=2", "start a port=8080 tags=2", "update a port=7")
}

func TestReloadAddsAndRemovesEntries(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	f.write("app.yml", `
plugins:
  - id: b
    plugin: probe
    config: {name: b}
  - id: c
    plugin: probe
    config: {name: c}
`)
	must(t, f.loader.Reload(f.ctx()))
	f.settle()
	f.want("start a port=8080 tags=2", "start b port=8080 tags=2", "stop a", "start c port=8080 tags=2")
	if list := f.loader.Entries(); len(list) != 2 || list[0].ID != "b" || list[1].ID != "c" {
		t.Fatalf("entries = %+v", list)
	}
}

func TestBrokenFileIsNeitherAppliedNorOverwritten(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	const broken = "plugins:\n  - id: a\n    plugin: [\n"
	f.write("app.yml", broken)
	err := f.loader.Reload(f.ctx())
	if err == nil {
		t.Fatal("a broken file was accepted")
	}
	contains(t, err.Error(), "app.yml")
	f.settle()
	f.want("start a port=8080 tags=2", "start b port=8080 tags=2")
	if err := f.loader.Update(f.ctx(), "a", raw(`{"name":"a","inPlace":true,"port":9}`)); err == nil {
		t.Fatal("an edit claimed to have written a broken file")
	}
	if f.read("app.yml") != broken {
		t.Fatal("the broken file was overwritten")
	}
}

func TestFileWinsOverAnUnwrittenEditOfTheSameKey(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	loader.SetWrite(f.loader, func(string, []byte) error { return errors.New("disk is read-only") })
	if err := f.loader.Update(f.ctx(), "a", raw(`{"name":"a","inPlace":true,"port":1}`)); err == nil {
		t.Fatal("a failed write was not reported")
	}
	if err := f.loader.Update(f.ctx(), "b", loader.EntryPatch{Requires: &[]string{"db"}}); err == nil {
		t.Fatal("a failed write was not reported")
	}
	loader.SetWrite(f.loader, nil)
	f.write("app.yml", strings.Replace(two, "inPlace: true", "inPlace: true, port: 2", 1))
	err := f.loader.Reload(f.ctx())
	var dropped loader.Dropped
	if !errors.As(err, &dropped) || dropped.ID != "a" || dropped.Key != "config" {
		t.Fatalf("reload = %v", err)
	}
	f.settle()
	if f.log.count("update a port=2") != 1 {
		t.Fatalf("the file's value is not live: %q", f.log.get())
	}
	// The edit of another key survives and is written.
	contains(t, f.read("app.yml"), "port: 2", "requires:")
}

func TestChangedReportsEditsAndReloads(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	var seen events
	loader.Changed.On(f.app.Scope(), func(_ context.Context, change loader.Change) error {
		seen.add("%s %s", change.Kind, change.ID)
		return nil
	})
	must(t, f.loader.Update(f.ctx(), "a", raw(`{"name":"a","inPlace":true,"port":9}`)))
	f.write("app.yml", strings.Replace(f.read("app.yml"), "name: b", "name: renamed", 1))
	must(t, f.loader.Reload(f.ctx()))
	if got := seen.get(); len(got) != 2 || got[0] != "update a" || got[1] != "update b" {
		t.Fatalf("changes = %q", got)
	}
}

func TestPluginFindsAndDisablesItsOwnEntry(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: me
    plugin: self
`)
	must(t, loader.Register(f.reg, "self", cordis.Plugin[struct{}]{Name: "self",
		Requires: []cordis.Requirement{loader.Service},
		Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			l, err := loader.Service.Get(s)
			if err != nil {
				return err
			}
			id, ok := loader.EntryOf(s)
			f.log.add("entry %s %v", id, ok)
			s.Defer(func(context.Context) error {
				f.log.add("stopped")
				return nil
			})
			s.Go(func(ctx context.Context) error {
				// Disabling the entry cancels ctx; the write must outlive it.
				return l.Update(context.WithoutCancel(ctx), id, loader.EntryPatch{Disabled: ptr(true)})
			})
			return nil
		}}))
	f.start()
	eventually(t, "the entry to stop itself", func() bool { return f.log.count("stopped") == 1 })
	f.want("entry me true", "stopped")
	eventually(t, "the write", func() bool { return strings.Contains(f.read("app.yml"), "disabled: true") })
	if _, ok := loader.EntryOf(f.app.Scope()); ok {
		t.Fatal("the root scope belongs to no entry")
	}
}

func TestReadyGatesTheStartOnly(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: slow
    plugin: probe
    config: {name: slow, delay: gate}
  - id: stuck
    plugin: probe
    requires: [db]
  - id: other
    plugin: probe
    config: {name: other}
`)
	f.app = cordis.New(cordis.Options{})
	t.Cleanup(func() { f.app.Close(context.Background()) })
	f.inst = loader.Plugin.Load(f.app.Scope(), loader.Config{File: f.file, Registry: f.reg})
	must(t, f.inst.Wait(f.ctx()))
	var starts atomic.Int32
	found := make(chan *loader.Loader, 1)
	waiter := cordis.Plugin[struct{}]{Name: "waiter", Requires: []cordis.Requirement{loader.Ready},
		Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			starts.Add(1)
			l, err := loader.Service.Get(s)
			f.log.add("ready")
			found <- l
			return err
		}}.Load(f.app.Scope(), struct{}{})
	eventually(t, "the other entry", func() bool { return f.log.count("start other port=8080 tags=2") == 1 })
	if f.log.count("ready") != 0 {
		t.Fatal("started while an entry was still starting")
	}
	close(f.gate)
	eventually(t, "the waiter", func() bool { return waiter.Status().State == cordis.Active })
	// The slow entry settled first; the entry that lacks db did not hold it up.
	if got := f.log.get(); got[len(got)-1] != "ready" || f.log.count("start slow port=8080 tags=2") != 1 {
		t.Fatalf("events = %q", got)
	}
	f.loader = <-found
	f.settle()

	must(t, f.loader.Update(f.ctx(), "other", raw(`{"name":"again"}`)))
	f.settle()
	if f.log.count("start again port=8080 tags=2") != 1 {
		t.Fatalf("events = %q", f.log.get())
	}
	if starts.Load() != 1 || waiter.Status().State != cordis.Active {
		t.Fatalf("a later edit disturbed the waiter: starts=%d, %+v", starts.Load(), waiter.Status())
	}
}

func TestWatchReloadsAChangedFile(t *testing.T) {
	f := newFixture(t, "app.yml", two)
	f.app = cordis.New(cordis.Options{})
	t.Cleanup(func() { f.app.Close(context.Background()) })
	f.inst = loader.Plugin.Load(f.app.Scope(), loader.Config{File: f.file, Registry: f.reg, Watch: time.Millisecond})
	must(t, f.inst.Wait(f.ctx()))
	eventually(t, "the entries", func() bool { return len(f.log.get()) == 2 })
	f.write("app.yml", strings.Replace(two, "inPlace: true", "inPlace: true, port: 7", 1))
	eventually(t, "the reload", func() bool { return f.log.count("update a port=7") == 1 })
}
