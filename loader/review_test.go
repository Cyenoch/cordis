package loader_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/loader"
)

// Regressions for defects found by review.

var failing = func(string, []byte) error { return errors.New("disk is read-only") }

const three = `
plugins:
  - id: a
    plugin: probe
    config: {name: a, inPlace: true}
  - id: b
    plugin: probe
    config: {name: b}
  - id: c
    plugin: probe
    config: {name: c}
`

// ids lists the identifiers of the top-level entries.
func ids(list []loader.Entry) string {
	var out []string
	for _, entry := range list {
		id := entry.ID
		if len(entry.Children) > 0 {
			id += "[" + ids(entry.Children) + "]"
		}
		out = append(out, id)
	}
	return strings.Join(out, ",")
}

// order lists the identifiers in the order the file has them.
func order(text string) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if _, id, ok := strings.Cut(line, "- id: "); ok {
			out = append(out, id)
		}
	}
	return strings.Join(out, ",")
}

func TestRetriedWriteAppliesEachEditOnce(t *testing.T) {
	f := newFixture(t, "app.yml", three).start()
	loader.SetWrite(f.loader, failing)
	for _, edit := range []func() error{
		func() error { return f.loader.Remove(f.ctx(), "a") },
		func() error { return f.loader.Move(f.ctx(), "c", "", 0) },
		func() error {
			_, err := f.loader.Create(f.ctx(), "", loader.EntryOptions{ID: "x", Plugin: "probe"})
			return err
		},
	} {
		if err := edit(); err == nil {
			t.Fatal("a failed write was not reported")
		}
	}
	loader.SetWrite(f.loader, nil)
	must(t, f.loader.Reload(f.ctx()))
	f.settle()
	if got := order(f.read("app.yml")); got != "c,b,x" || ids(f.loader.Entries()) != "c,b,x" {
		t.Fatalf("file %q, entries %q", got, ids(f.loader.Entries()))
	}
	must(t, f.loader.Update(f.ctx(), "b", loader.EntryPatch{Disabled: ptr(true)}))
	contains(t, f.read("app.yml"), "disabled: true")
}

func TestFileMovesAnEntryBetweenGroups(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - {id: a, plugin: probe, config: {name: a}}
  - id: g
    plugins:
      - {id: b, plugin: probe, config: {name: b}}
`).start()
	f.write("app.yml", `
plugins:
  - id: g
    plugins:
      - {id: b, plugin: probe, config: {name: b}}
      - {id: a, plugin: probe, config: {name: a}}
`)
	must(t, f.loader.Reload(f.ctx()))
	f.settle()
	if got := ids(f.loader.Entries()); got != "g[b,a]" {
		t.Fatalf("entries = %q", got)
	}
	f.write("app.yml", `
plugins:
  - id: g
    plugins:
      - {id: a, plugin: probe, config: {name: a}}
  - {id: b, plugin: probe, config: {name: b}}
`)
	must(t, f.loader.Reload(f.ctx()))
	f.settle()
	if got := ids(f.loader.Entries()); got != "g[a],b" {
		t.Fatalf("entries = %q", got)
	}
	if f.entry("a").State != cordis.Active.String() || f.entry("b").State != cordis.Active.String() {
		t.Fatalf("a moved entry does not run: %+v %+v", f.entry("a"), f.entry("b"))
	}
}

func TestRemovedGroupTakesItsChildrenAlong(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: g
    plugins:
      - {id: a, plugin: probe, config: {name: a}}
  - id: h
    plugins:
      - {id: b, plugin: probe, config: {name: b}}
`).start()
	must(t, f.loader.Remove(f.ctx(), "g"))
	if err := f.loader.Update(f.ctx(), "a", raw(`{"name":"zombie"}`)); !errors.Is(err, loader.ErrNotFound) {
		t.Fatalf("update of a removed child = %v", err)
	}
	// The identifier is free again, also while the old entry still stops.
	_, err := f.loader.Create(f.ctx(), "h", loader.EntryOptions{ID: "a", Plugin: "probe", Config: []byte(`{"name":"new"}`)})
	must(t, err)
	f.settle()
	if got := ids(f.loader.Entries()); got != "h[b,a]" || order(f.read("app.yml")) != "h,a" {
		t.Fatalf("entries %q, file:\n%s", got, f.read("app.yml"))
	}
	if strings.Contains(f.read("app.yml"), "zombie") || f.log.count("start new port=8080 tags=2") != 1 {
		t.Fatalf("file:\n%s\nevents %q", f.read("app.yml"), f.log.get())
	}
}

func TestUnwrittenRemovalSurvivesAReload(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	loader.SetWrite(f.loader, failing)
	if err := f.loader.Remove(f.ctx(), "a"); err == nil {
		t.Fatal("a failed write was not reported")
	}
	f.settle()
	loader.SetWrite(f.loader, nil)
	f.write("app.yml", strings.Replace(two, "{name: b}", "{name: b2}", 1))
	must(t, f.loader.Reload(f.ctx()))
	f.settle()
	if got := ids(f.loader.Entries()); got != "b" || order(f.read("app.yml")) != "b" {
		t.Fatalf("entries %q, file:\n%s", got, f.read("app.yml"))
	}
	contains(t, f.read("app.yml"), "name: b2")
	if f.log.count("start a port=8080 tags=2") != 1 {
		t.Fatalf("the removed entry was started again: %q", f.log.get())
	}
}

func TestFileChangeCancelsAnUnwrittenRemoval(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: g
    plugins:
      - {id: a, plugin: probe, config: {name: a}}
  - {id: b, plugin: probe, config: {name: b}}
`).start()
	loader.SetWrite(f.loader, failing)
	if err := f.loader.Remove(f.ctx(), "g"); err == nil {
		t.Fatal("a failed write was not reported")
	}
	f.settle()
	loader.SetWrite(f.loader, nil)
	f.write("app.yml", strings.Replace(f.read("app.yml"), "{name: a}", "{name: a2}", 1))
	err := f.loader.Reload(f.ctx())
	var dropped loader.Dropped
	if !errors.As(err, &dropped) || dropped.ID != "g" || dropped.Kind != loader.Removed {
		t.Fatalf("reload = %v", err)
	}
	f.settle()
	if got := ids(f.loader.Entries()); got != "g[a],b" || f.log.count("start a2 port=8080 tags=2") != 1 {
		t.Fatalf("entries %q, events %q", got, f.log.get())
	}
	contains(t, f.read("app.yml"), "name: a2")
}

func TestUnwrittenEditsBelowWhatTheFileRemovesAreReported(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: g
    plugins:
      - {id: a, plugin: probe, config: {name: a}}
  - {id: b, plugin: probe, config: {name: b}}
`).start()
	loader.SetWrite(f.loader, failing)
	f.loader.Create(f.ctx(), "g", loader.EntryOptions{ID: "x", Plugin: "probe"})
	f.loader.Update(f.ctx(), "a", raw(`{"name":"edited"}`))
	loader.SetWrite(f.loader, nil)
	f.write("app.yml", "plugins:\n  - {id: b, plugin: probe, config: {name: b}}\n")
	err := f.loader.Reload(f.ctx())
	lost := map[string]bool{}
	var collect func(error)
	collect = func(err error) {
		if dropped, ok := err.(loader.Dropped); ok {
			lost[string(dropped.Kind)+" "+dropped.ID] = true
		}
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, part := range joined.Unwrap() {
				collect(part)
			}
		}
	}
	collect(err)
	if !lost["create x"] || !lost["update a"] {
		t.Fatalf("reload = %v", err)
	}
	f.settle()
	// Nothing of the removed group reaches the file, and later edits are written.
	must(t, f.loader.Update(f.ctx(), "b", loader.EntryPatch{Disabled: ptr(true)}))
	if got := f.read("app.yml"); order(got) != "" || strings.Contains(got, "edited") || !strings.Contains(got, "disabled: true") {
		t.Fatalf("file:\n%s", got)
	}
}

func TestMoveCountsTheEntriesTheFileHas(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: extra
    include: extra.yml
    patches:
      - insert:
          - {id: added, plugin: probe, config: {name: added}}
`)
	f.write("extra.yml", `
plugins:
  - {id: cache, plugin: probe, config: {name: cache, inPlace: true}}
  - {id: x, plugin: probe, config: {name: x}}
`)
	f.start()
	must(t, f.loader.Move(f.ctx(), "extra:x", "extra", 5))
	must(t, f.loader.Move(f.ctx(), "extra:cache", "extra", 1))
	must(t, f.loader.Update(f.ctx(), "extra:cache", raw(`{"name":"cache","inPlace":true,"port":99}`)))
	f.settle()
	if got := ids(f.loader.Entries()); got != "extra[extra:added,extra:cache,extra:x]" {
		t.Fatalf("entries = %q", got)
	}
	text := f.read("extra.yml")
	if order(text) != "" && !strings.Contains(text, "{id: cache") {
		t.Fatalf("file:\n%s", text)
	}
	if strings.Index(text, "id: cache") > strings.Index(text, "id: x") ||
		strings.Index(text, "port: 99") > strings.Index(text, "id: x") {
		t.Fatalf("the edit landed on the wrong entry:\n%s", text)
	}
	if err := f.loader.Move(f.ctx(), "extra:cache", "extra", 1); err != nil {
		t.Fatal(err)
	}
}

func TestClearingAKeyThatAPatchProvides(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: extra
    include: extra.yml
    patches:
      - id: cache
        disabled: true
      - insert:
          - {id: added, plugin: probe, disabled: true, config: {name: added}}
`)
	f.write("extra.yml", extra)
	f.start()
	f.want()
	must(t, f.loader.Update(f.ctx(), "extra:cache", loader.EntryPatch{Disabled: ptr(false)}))
	must(t, f.loader.Update(f.ctx(), "extra:added", loader.EntryPatch{Disabled: ptr(false)}))
	f.settle()
	f.want("start cache port=8080 tags=2", "start added port=8080 tags=2")
	main := f.read("app.yml")
	if strings.Contains(main, "null") || strings.Contains(main, "disabled: true") || !strings.Contains(main, "disabled: false") {
		t.Fatalf("file:\n%s", main)
	}
	must(t, f.loader.Reload(f.ctx()))
	if unapplied := f.entry("extra").Unapplied; len(unapplied) != 0 {
		t.Fatalf("the written patches no longer apply: %v", unapplied)
	}
}

func TestEntryWithRequirementsRecoversAndFollowsItsProvider(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - {id: db, plugin: db, config: {name: main}}
  - id: a
    plugin: probe
    requires: [db]
    config: {name: a, nope: 1}
`).start()
	if f.entry("a").Err == nil {
		t.Fatalf("entry = %+v", f.entry("a"))
	}
	must(t, f.loader.Update(f.ctx(), "a", raw(`{"name":"fixed"}`)))
	f.settle()
	if f.log.count("start fixed port=8080 tags=2") != 1 || f.entry("a").State != cordis.Active.String() {
		t.Fatalf("entry = %+v, events %q", f.entry("a"), f.log.get())
	}
	// While the provider is away the plugin does not run; a new
	// configuration is used when it comes back.
	must(t, f.loader.Update(f.ctx(), "db", loader.EntryPatch{Disabled: ptr(true)}))
	f.settle()
	must(t, f.loader.Update(f.ctx(), "a", raw(`{"name":"later"}`)))
	must(t, f.loader.Update(f.ctx(), "db", loader.EntryPatch{Disabled: ptr(false)}))
	f.settle()
	eventually(t, "the entry", func() bool { return f.entry("a").State == cordis.Active.String() })
	if got := f.entry("a"); got.Err != nil || f.log.count("start later port=8080 tags=2") != 1 {
		t.Fatalf("entry = %+v, events %q", got, f.log.get())
	}
}

func TestFileTakesOverAnIdentifierOfAnUnwrittenEntry(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	loader.SetWrite(f.loader, failing)
	f.loader.Create(f.ctx(), "", loader.EntryOptions{ID: "x", Plugin: "probe", Config: []byte(`{"name":"mine"}`)})
	loader.SetWrite(f.loader, nil)
	f.write("app.yml", two+"  - {id: x, plugin: probe, config: {name: theirs, inPlace: true}}\n")
	var dropped loader.Dropped
	if err := f.loader.Reload(f.ctx()); !errors.As(err, &dropped) || dropped.Kind != loader.Created {
		t.Fatalf("reload = %v", err)
	}
	f.settle()
	must(t, f.loader.Update(f.ctx(), "x", raw(`{"name":"theirs","inPlace":true,"port":3}`)))
	f.settle()
	if f.log.count("update theirs port=3") != 1 || ids(f.loader.Entries()) != "a,b,x" {
		t.Fatalf("entries %q, events %q", ids(f.loader.Entries()), f.log.get())
	}
}

func TestFileReaddsAnEntryThatStillStops(t *testing.T) {
	const slow = `
plugins:
  - {id: a, plugin: probe, config: {name: a, delay: stop}}
  - {id: b, plugin: probe, config: {name: b}}
`
	f := newFixture(t, "app.yml", slow).start()
	f.write("app.yml", "plugins:\n  - {id: b, plugin: probe, config: {name: b}}\n")
	must(t, f.loader.Reload(f.ctx()))
	f.write("app.yml", slow)
	must(t, f.loader.Reload(f.ctx()))
	close(f.gate)
	f.settle()
	if got := ids(f.loader.Entries()); got != "a,b" || f.entry("a").State != cordis.Active.String() {
		t.Fatalf("entries %q, a = %+v", got, f.entry("a"))
	}
}

func TestReloadRetriesAnIncludeThatFailed(t *testing.T) {
	f := newFixture(t, "app.yml", "plugins:\n  - id: extra\n    include: extra.yml\n")
	f.write("extra.yml", "plugins:\n  - id: [\n")
	f.start()
	if f.entry("extra").Err == nil {
		t.Fatal("a broken included file was accepted")
	}
	f.write("extra.yml", extra)
	must(t, f.loader.Reload(f.ctx()))
	f.settle()
	f.want("start cache port=8080 tags=2")
}

func TestReservedIdentifiersAreRejected(t *testing.T) {
	f := newFixture(t, "app.yml", "plugins:\n  - {id: \"~1\", plugin: probe}\n")
	f.app = cordis.New(cordis.Options{})
	t.Cleanup(func() { f.app.Close(context.Background()) })
	inst := loader.Plugin.Load(f.app.Scope(), loader.Config{File: f.file, Registry: f.reg})
	if err := inst.Wait(f.ctx()); err == nil || !strings.Contains(err.Error(), "~1") {
		t.Fatalf("loader = %v", err)
	}
}

func TestEditsThatChangeNothing(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	var seen events
	loader.Changed.On(f.app.Scope(), func(_ context.Context, change loader.Change) error {
		seen.add("%s %s", change.Kind, change.ID)
		return nil
	})
	before := f.read("app.yml")
	must(t, f.loader.Update(f.ctx(), "a", raw(`{"inPlace": true, "name": "a"}`)))
	must(t, f.loader.Move(f.ctx(), "a", "", 0))
	if len(seen.get()) != 0 || f.read("app.yml") != before {
		t.Fatalf("changes %q", seen.get())
	}
	entry := f.entry("a")
	if string(entry.Config) != `{"inPlace":true,"name":"a"}` {
		t.Fatalf("snapshot config = %s", entry.Config)
	}
}

func TestZeroRegistry(t *testing.T) {
	var registry loader.Registry
	must(t, loader.Register(&registry, "x", cordis.Plugin[struct{}]{Name: "x"}))
	must(t, loader.RegisterSetting(&registry, Timeout))
	if _, ok := registry.Resolve("x"); !ok {
		t.Fatal("not registered")
	}
}

// Regressions for defects found by the second review.

const grouped = `
plugins:
  - {id: a, plugin: probe, config: {name: a}}
  - id: g
    plugins:
      - {id: b, plugin: probe, config: {name: b}}
`

// reloaded starts a second loader on the files the first one wrote and
// returns its tree, which must equal the running one.
func (f *fixture) reloaded() string {
	f.t.Helper()
	again := newFixture(f.t, "unused.yml", "plugins: []\n")
	again.dir, again.file = f.dir, f.file
	again.start()
	return ids(again.loader.Entries())
}

func TestEntryMovedIntoAGroupTheFileRemoves(t *testing.T) {
	f := newFixture(t, "app.yml", grouped).start()
	loader.SetWrite(f.loader, failing)
	f.loader.Move(f.ctx(), "a", "g", 0)
	f.settle()
	loader.SetWrite(f.loader, nil)
	f.write("app.yml", "plugins:\n  - {id: a, plugin: probe, config: {name: a}}\n")
	var dropped loader.Dropped
	if err := f.loader.Reload(f.ctx()); !errors.As(err, &dropped) || dropped.Kind != loader.Moved {
		t.Fatalf("reload = %v", err)
	}
	f.settle()
	if got := ids(f.loader.Entries()); got != "a" || f.entry("a").State != cordis.Active.String() || f.reloaded() != "a" {
		t.Fatalf("entries %q, file:\n%s", got, f.read("app.yml"))
	}
}

func TestEntryMovedIntoAnUnwrittenGroupTheFileReplaces(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	loader.SetWrite(f.loader, failing)
	f.loader.Create(f.ctx(), "", loader.EntryOptions{ID: "n", Group: true})
	f.loader.Move(f.ctx(), "a", "n", 0)
	loader.SetWrite(f.loader, nil)
	f.write("app.yml", two+"  - {id: n, plugin: probe, config: {name: n}}\n")
	if err := f.loader.Reload(f.ctx()); err == nil {
		t.Fatal("dropped edits were not reported")
	}
	f.settle()
	if got := ids(f.loader.Entries()); got != "a,b,n" || f.reloaded() != "a,b,n" {
		t.Fatalf("entries %q, file:\n%s", got, f.read("app.yml"))
	}
}

func TestRevivedEntryLeavesTheRemovedGroupItWasMovedInto(t *testing.T) {
	f := newFixture(t, "app.yml", two).start()
	loader.SetWrite(f.loader, failing)
	f.loader.Create(f.ctx(), "", loader.EntryOptions{ID: "n", Group: true})
	f.loader.Move(f.ctx(), "a", "n", 0)
	f.loader.Remove(f.ctx(), "n")
	loader.SetWrite(f.loader, nil)
	f.write("app.yml", strings.Replace(two, "inPlace: true", "inPlace: true, port: 7", 1))
	if err := f.loader.Reload(f.ctx()); err == nil {
		t.Fatal("dropped edits were not reported")
	}
	f.settle()
	if got := ids(f.loader.Entries()); got != "a,b" || f.reloaded() != "a,b" {
		t.Fatalf("entries %q, file:\n%s", got, f.read("app.yml"))
	}
	contains(t, f.read("app.yml"), "port: 7")
}

func TestUnwrittenChildOfWhatIsNoLongerAGroup(t *testing.T) {
	f := newFixture(t, "app.yml", grouped).start()
	loader.SetWrite(f.loader, failing)
	f.loader.Create(f.ctx(), "g", loader.EntryOptions{ID: "x", Plugin: "probe"})
	loader.SetWrite(f.loader, nil)
	f.write("app.yml", "plugins:\n  - {id: a, plugin: probe, config: {name: a}}\n  - {id: g, plugin: probe, config: {name: g}}\n")
	var dropped loader.Dropped
	if err := f.loader.Reload(f.ctx()); !errors.As(err, &dropped) || dropped.ID != "x" {
		t.Fatalf("reload = %v", err)
	}
	f.settle()
	if got := ids(f.loader.Entries()); got != "a,g" {
		t.Fatalf("entries %q", got)
	}
	_, err := f.loader.Create(f.ctx(), "", loader.EntryOptions{ID: "x", Plugin: "probe"})
	must(t, err)
	if f.reloaded() != "a,g,x" {
		t.Fatalf("file:\n%s", f.read("app.yml"))
	}
}

func TestUnwrittenRemovalDoesNotDisturbAnUnwrittenMove(t *testing.T) {
	f := newFixture(t, "app.yml", three).start()
	loader.SetWrite(f.loader, failing)
	f.loader.Remove(f.ctx(), "a")
	f.settle()
	f.loader.Move(f.ctx(), "b", "", 1)
	loader.SetWrite(f.loader, nil)
	f.write("app.yml", three+"  - {id: d, plugin: probe, config: {name: d}}\n")
	must(t, f.loader.Reload(f.ctx()))
	f.settle()
	if got := ids(f.loader.Entries()); got != "c,b,d" || f.reloaded() != "c,b,d" {
		t.Fatalf("entries %q, file:\n%s", got, f.read("app.yml"))
	}
}

func TestWriteBackIntoUnusualFiles(t *testing.T) {
	t.Run("plugins without a value", func(t *testing.T) {
		f := newFixture(t, "app.yml", "plugins:\n").start()
		_, err := f.loader.Create(f.ctx(), "", loader.EntryOptions{ID: "x", Plugin: "probe"})
		must(t, err)
		if f.reloaded() != "x" {
			t.Fatalf("file:\n%s", f.read("app.yml"))
		}
	})
	t.Run("a commented scalar becomes a list", func(t *testing.T) {
		f := newFixture(t, "app.yml", `
plugins:
  - id: a
    plugin: probe
    config:
      name: a
      tags: old # keep
  - {id: b, plugin: probe}
`)
		must(t, loader.Register(f.reg, "any", cordis.Plugin[map[string]any]{Name: "any"}))
		f.write("app.yml", strings.Replace(f.read("app.yml"), "plugin: probe\n    config", "plugin: any\n    config", 1))
		f.start()
		must(t, f.loader.Update(f.ctx(), "a", raw(`{"name":"a","tags":[1,2]}`)))
		if f.reloaded() != "a,b" {
			t.Fatalf("file:\n%s", f.read("app.yml"))
		}
	})
}

func TestValuesYAMLMustCarry(t *testing.T) {
	f := newFixture(t, "app.yml", "plugins:\n  - {id: a, plugin: any}\n")
	must(t, loader.Register(f.reg, "any", cordis.Plugin[map[string]any]{Name: "any"}))
	f.start()
	for _, value := range []string{`-0`, `1.0`, `"\u0085"`, `"\u007f"`, `"😀"`, `"1e3"`, `"yes"`, `" x "`, `""`,
		`"a: b"`, `"# x"`, `"line\nbreak"`, `123456789012345678901234567890`, `null`, `[1,[2,{"k":null}]]`} {
		err := f.loader.Update(f.ctx(), "a", raw(`{"v":`+value+`}`))
		if err != nil {
			// A refused value must not be applied or block later writes.
			if string(f.entry("a").Config) == `{"v":`+value+`}` {
				t.Fatalf("%s was refused (%v) but applied", value, err)
			}
			continue
		}
		var got string
		for _, entry := range func() []loader.Entry {
			again := newFixture(t, "unused.yml", "plugins: []\n")
			again.reg, again.dir, again.file = f.reg, f.dir, f.file
			return again.start().loader.Entries()
		}() {
			got = string(entry.Config)
		}
		var wrote, read any
		want := string(f.entry("a").Config)
		json.Unmarshal([]byte(want), &wrote)
		json.Unmarshal([]byte(got), &read)
		if !reflect.DeepEqual(wrote, read) {
			t.Fatalf("%s: wrote %s, a fresh load reads %s\n%s", value, want, got, f.read("app.yml"))
		}
	}
	must(t, f.loader.Update(f.ctx(), "a", raw(`{"v":"last"}`)))
	contains(t, f.read("app.yml"), "v: last")
}

func TestPatchesFollowTheTree(t *testing.T) {
	const owner = `
plugins:
  - id: extra
    include: extra.yml
    patches:
      - id: cache
        plugin: probe
        disabled: true
      - id: g
        insert:
          - {id: added, plugin: probe, config: {name: added}}
`
	const included = `
plugins:
  - {id: cache, plugin: probe, config: {name: cache}}
  - {id: g, plugins: []}
`
	f := newFixture(t, "app.yml", owner)
	f.write("extra.yml", included)
	f.start()
	f.want("start added port=8080 tags=2")

	// The patch names another plugin now: it lets go of the key.
	f.write("extra.yml", strings.Replace(included, "plugin: probe, config: {name: cache}", "plugin: db, config: {name: cache}", 1))
	must(t, f.loader.Reload(f.ctx()))
	f.settle()
	if got := f.entry("extra:cache"); got.Disabled || len(f.entry("extra").Unapplied) != 1 || f.log.count("db cache") != 1 {
		t.Fatalf("entry = %+v, events %q", got, f.log.get())
	}

	// The group the patch inserts into goes away and comes back.
	f.write("extra.yml", "plugins:\n  - {id: cache, plugin: probe, config: {name: cache}}\n")
	must(t, f.loader.Reload(f.ctx()))
	f.settle()
	f.write("extra.yml", included)
	must(t, f.loader.Reload(f.ctx()))
	f.settle()
	if got := ids(f.loader.Entries()); got != "extra[extra:cache,extra:g[extra:added]]" || !f.entry("extra:cache").Disabled {
		t.Fatalf("entries %q", got)
	}

	// Edits are patched like the file's entries.
	must(t, f.loader.Remove(f.ctx(), "extra:cache"))
	if len(f.entry("extra").Unapplied) != 1 {
		t.Fatalf("unapplied = %v", f.entry("extra").Unapplied)
	}
	_, err := f.loader.Create(f.ctx(), "extra", loader.EntryOptions{ID: "cache", Plugin: "probe", Config: []byte(`{"name":"mine"}`)})
	must(t, err)
	f.settle()
	if got := f.entry("extra:cache"); !got.Disabled || len(f.entry("extra").Unapplied) != 0 {
		t.Fatalf("entry = %+v", got)
	}
	if strings.Contains(f.read("extra.yml"), "disabled") {
		t.Fatalf("a patched key was written to the included file:\n%s", f.read("extra.yml"))
	}
	if got := ids(f.loader.Entries()); got != f.reloaded() {
		t.Fatalf("running %q, fresh load %q", got, f.reloaded())
	}
}
