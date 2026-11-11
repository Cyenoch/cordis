package loader_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/loader"
)

const two = `
plugins:
  - id: a
    plugin: probe
    config: {name: a, inPlace: true}
  - id: b
    plugin: probe
    config: {name: b}
`

// raw is a patch that replaces the configuration.
func raw(text string) loader.EntryPatch {
	message := json.RawMessage(text)
	return loader.EntryPatch{Config: &message}
}

func TestDisableAndEnable(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	must(t, f.loader.Update(f.ctx(), "b", loader.EntryPatch{Disabled: ptr(true)}))
	f.settle()
	if got := f.entry("b"); got.State != "disabled" || !got.Disabled {
		t.Fatalf("entry = %+v", got)
	}
	contains(t, f.read("app.yml"), "disabled: true")
	must(t, f.loader.Update(f.ctx(), "b", loader.EntryPatch{Disabled: ptr(false)}))
	f.settle()
	f.want("start a port=8080 tags=2", "start b port=8080 tags=2", "stop b", "start b port=8080 tags=2")
	if strings.Contains(f.read("app.yml"), "disabled") {
		t.Fatalf("file still marks the entry:\n%s", f.read("app.yml"))
	}
}

func TestDisabledGroupKeepsItsChildrenUnmarked(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: g
    plugins:
      - {id: a, plugin: probe, config: {name: a}}
`).start()
	must(t, f.loader.Update(f.ctx(), "g", loader.EntryPatch{Disabled: ptr(true)}))
	f.settle()
	f.want("start a port=8080 tags=2", "stop a")
	if strings.Count(f.read("app.yml"), "disabled") != 1 || f.entry("a").Disabled {
		t.Fatalf("the child was marked:\n%s", f.read("app.yml"))
	}
	must(t, f.loader.Update(f.ctx(), "g", loader.EntryPatch{Disabled: ptr(false)}))
	f.settle()
	f.want("start a port=8080 tags=2", "stop a", "start a port=8080 tags=2")
}

func TestConfigOnlyUpdateIsAppliedInPlace(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	must(t, f.loader.Update(f.ctx(), "a", raw(`{"name":"a","inPlace":true,"port":9}`)))
	f.settle()
	f.want("start a port=8080 tags=2", "start b port=8080 tags=2", "update a port=9")
}

func TestChangedIsolationReloadsTheSubtree(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: g
    plugins:
      - {id: a, plugin: probe, config: {name: a}}
`).start()
	must(t, f.loader.Update(f.ctx(), "g", loader.EntryPatch{Isolate: &map[string]string{"db": ""}}))
	f.settle()
	f.want("start a port=8080 tags=2", "stop a", "start a port=8080 tags=2")
	contains(t, f.read("app.yml"), "db: true")
}

func TestInvalidEditChangesNothing(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	before := f.read("app.yml")
	if _, err := f.loader.Create(f.ctx(), "", loader.EntryOptions{ID: "x"}); err == nil {
		t.Fatal("an entry without a plugin or children was accepted")
	}
	if _, err := f.loader.Create(f.ctx(), "", loader.EntryOptions{ID: "a", Plugin: "probe"}); err == nil {
		t.Fatal("a duplicate id was accepted")
	}
	if len(f.loader.Entries()) != 2 || f.read("app.yml") != before {
		t.Fatal("a rejected edit took effect")
	}
}

func TestEditsReturnBeforeTheInstanceSettles(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	must(t, f.loader.Update(f.ctx(), "b", raw(`{"name":"b","delay":"gate"}`)))
	contains(t, f.read("app.yml"), "delay: gate")
	if f.log.count("start b port=8080 tags=2") != 1 {
		t.Fatal("the slow restart finished before it was released")
	}
	close(f.gate)
	f.settle()
	if f.log.count("start b port=8080 tags=2") != 2 || f.entry("b").State != cordis.Active.String() {
		t.Fatalf("events = %q, entry = %+v", f.log.get(), f.entry("b"))
	}
}

func TestConcurrentPluginChangesConverge(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	f.log.reset()
	var group sync.WaitGroup
	for _, plugin := range []string{"db", "consumer"} {
		group.Go(func() {
			if err := f.loader.Update(f.ctx(), "b", loader.EntryPatch{Plugin: &plugin}); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	f.settle()
	final := f.entry("b").Plugin
	contains(t, f.read("app.yml"), "plugin: "+final)
	var last string
	for _, event := range f.log.get() {
		if event == "db b" || strings.HasPrefix(event, "b bound") {
			last = event
		}
	}
	// Without a db the consumer waits, so only "db" leaves a trace.
	if final == "db" && (last != "db b" || f.log.count("db b") != 1) {
		t.Fatalf("final plugin db, events %q", f.log.get())
	}
	if final == "consumer" && f.entry("b").State != cordis.Waiting.String() {
		t.Fatalf("final plugin consumer, entry %+v", f.entry("b"))
	}
}

func TestMoveWithinAGroupDoesNotRestart(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	must(t, f.loader.Move(f.ctx(), "b", "", 0))
	f.settle()
	f.want("start a port=8080 tags=2", "start b port=8080 tags=2")
	text := f.read("app.yml")
	if strings.Index(text, "id: b") > strings.Index(text, "id: a") {
		t.Fatalf("order was not written:\n%s", text)
	}
	if ids := f.loader.Entries(); ids[0].ID != "b" || ids[1].ID != "a" {
		t.Fatalf("entries = %+v", ids)
	}
}

func TestWriteBackKeepsCommentsAndOrder(t *testing.T) {
	f := newFixture(t, "app.yml", `
# Application plugins.
plugins:
  # The first one.
  - id: a
    plugin: probe # trailing
    config:
      name: 'a' # quoted
      # stays in place
      inPlace: true
  - id: b
    plugin: probe
    config:
      # b's name
      name: "b"
`).start()
	must(t, f.loader.Update(f.ctx(), "a", raw(`{"name":"a","inPlace":true,"port":9}`)))
	id, err := f.loader.Create(f.ctx(), "", loader.EntryOptions{ID: "c", Plugin: "probe",
		Requires: []string{"db"}, Config: []byte(`{"name":"c"}`)})
	must(t, err)
	if id != "c" {
		t.Fatalf("id = %q", id)
	}
	const want = `# Application plugins.
plugins:
  # The first one.
  - id: a
    plugin: probe # trailing
    config:
      name: 'a' # quoted
      # stays in place
      inPlace: true
      port: 9
  - id: b
    plugin: probe
    config:
      # b's name
      name: "b"
  - id: c
    plugin: probe
    requires:
      - db
    config:
      name: c
`
	if got := f.read("app.yml"); got != want {
		t.Fatalf("file:\n%s\nwant:\n%s", got, want)
	}
}

func TestExternalChangeIsMergedBeforeWriting(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	f.write("app.yml", strings.Replace(two, "{name: a, inPlace: true}", "{name: a, inPlace: true, port: 7}", 1))
	must(t, f.loader.Update(f.ctx(), "b", loader.EntryPatch{Requires: &[]string{"db"}}))
	f.settle()
	contains(t, f.read("app.yml"), "port: 7", "requires:")
	if f.log.count("update a port=7") != 1 {
		t.Fatalf("the external change is not live: %q", f.log.get())
	}
}

// lateCancel is a context that is canceled by the time the edit reaches its
// write.
type lateCancel struct{ context.Context }

func (lateCancel) Err() error { return context.Canceled }

func TestCanceledWriteKeepsTheEditPending(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	before := f.read("app.yml")
	err := f.loader.Update(lateCancel{context.Background()}, "a", raw(`{"name":"a","inPlace":true,"port":9}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("update = %v", err)
	}
	f.settle()
	if f.log.count("update a port=9") != 1 || f.read("app.yml") != before {
		t.Fatal("the edit must be live and unwritten")
	}
	must(t, f.loader.Reload(f.ctx()))
	contains(t, f.read("app.yml"), "port: 9")
}

func TestPendingEditsAreWrittenOnStop(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	loader.SetWrite(f.loader, func(string, []byte) error { return errors.New("disk is read-only") })
	if err := f.loader.Update(f.ctx(), "a", raw(`{"name":"a","inPlace":true,"port":9}`)); err == nil {
		t.Fatal("a failed write was not reported")
	}
	loader.SetWrite(f.loader, nil)
	must(t, f.inst.Stop(f.ctx()))
	contains(t, f.read("app.yml"), "port: 9")
}

func TestFailedEntryRecoversThroughAConfigUpdate(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: a
    plugin: probe
    config: {name: a, nope: 1}
  - id: late
    plugin: probe
    requires: [db]
    config: {name: before}
`).start()
	if f.entry("a").State != "unresolved" {
		t.Fatalf("entry = %+v", f.entry("a"))
	}
	must(t, f.loader.Update(f.ctx(), "a", raw(`{"name":"fixed"}`)))
	// An entry that still waits for its requirement starts with the
	// configuration it has by then.
	must(t, f.loader.Update(f.ctx(), "late", raw(`{"name":"after"}`)))
	_, err := f.loader.Create(f.ctx(), "", loader.EntryOptions{Plugin: "db", Config: []byte(`{"name":"main"}`)})
	must(t, err)
	f.settle()
	if f.log.count("start fixed port=8080 tags=2") != 1 || f.log.count("start after port=8080 tags=2") != 1 ||
		f.log.count("start before port=8080 tags=2") != 0 {
		t.Fatalf("events = %q", f.log.get())
	}
}
