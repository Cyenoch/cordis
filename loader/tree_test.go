package loader_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/loader"
)

func TestUnknownPluginLeavesOtherEntriesAndTheFileAlone(t *testing.T) {
	const content = `
plugins:
  - id: ok
    plugin: probe
    config: {name: ok}
  - id: bad
    plugin: missing
`
	f := newFixture(t, "app.yml", content)
	before, _ := os.Stat(f.file)
	f.start()
	f.want("start ok port=8080 tags=2")
	bad := f.entry("bad")
	if bad.State != "unresolved" || bad.Err == nil {
		t.Fatalf("entry = %+v", bad)
	}
	for _, part := range []string{`"missing"`, "app.yml:6:5"} {
		if !strings.Contains(bad.Err.Error(), part) {
			t.Fatalf("error %q does not contain %q", bad.Err, part)
		}
	}
	after, _ := os.Stat(f.file)
	if f.read("app.yml") != strings.TrimLeft(content, "\n") || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("reading the file rewrote it")
	}
}

func TestDuplicateRegistration(t *testing.T) {
	f := newFixture(t, "app.yml", "plugins: []\n")
	err := loader.Register(f.reg, "probe", cordis.Plugin[struct{}]{Name: "other"})
	if !errors.Is(err, loader.ErrConflict) {
		t.Fatalf("second registration = %v", err)
	}
	if _, ok := f.reg.Resolve("probe"); !ok {
		t.Fatal("first registration was lost")
	}
}

func TestGroupChildrenStopBeforeTheGroupIsRemoved(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: g
    plugins:
      - {id: a, plugin: probe, config: {name: a}}
      - {id: b, plugin: probe, config: {name: b}}
`).start()
	if got := f.entry("g"); len(got.Children) != 2 || !got.Group {
		t.Fatalf("group = %+v", got)
	}
	must(t, f.loader.Remove(f.ctx(), "g"))
	f.settle()
	f.want("start a port=8080 tags=2", "start b port=8080 tags=2", "stop a", "stop b")
	if len(f.loader.Entries()) != 0 {
		t.Fatalf("entries = %+v", f.loader.Entries())
	}
}

func TestDefaultsAreDecodedOntoAFreshValuePerEntry(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - plugin: probe
    config: {name: one, tags: {a: x}}
  - plugin: probe
    config: {name: two, tags: {b: y}}
  - plugin: probe
`).start()
	// Each entry sees the default tag, its own tag and the one its plugin adds.
	f.want("start one port=8080 tags=3", "start two port=8080 tags=3", "start  port=8080 tags=2")
}

func TestUnknownConfigKeyIsLocated(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: bad
    plugin: probe
    config:
      name: bad
      nope: 1
  - id: invalid
    plugin: probe
    config: {port: -1}
  - id: ok
    plugin: probe
    config: {name: ok}
`).start()
	f.want("start ok port=8080 tags=2")
	bad := f.entry("bad")
	if bad.State != "unresolved" {
		t.Fatalf("entry = %+v", bad)
	}
	contains(t, bad.Err.Error(), "nope", "app.yml:6:7")
	invalid := f.entry("invalid")
	if invalid.State != cordis.Failed.String() {
		t.Fatalf("entry = %+v", invalid)
	}
	contains(t, invalid.Err.Error(), "port must not be negative")
}

func TestEntryRequirementsGateThePlugin(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: p
    plugin: probe
    requires: [db]
    config: {name: p}
`).start()
	f.want()
	waiting := f.entry("p")
	if waiting.State != cordis.Waiting.String() || len(waiting.Missing) != 1 || waiting.Missing[0].Service != "db" {
		t.Fatalf("entry = %+v", waiting)
	}
	id, err := f.loader.Create(f.ctx(), "", loader.EntryOptions{Plugin: "db", Config: []byte(`{"name":"main"}`)})
	must(t, err)
	f.settle()
	f.want("db main", "start p port=8080 tags=2")
	must(t, f.loader.Remove(f.ctx(), id))
	f.settle()
	eventually(t, "p to wait again", func() bool { return f.entry("p").State == cordis.Waiting.String() })
	f.want("db main", "start p port=8080 tags=2", "stop p")
}

func TestIsolationLabelsAndSettings(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: one
    isolate: {db: shared}
    plugins:
      - {plugin: db, config: {name: main}}
      - {plugin: consumer, config: {name: c1}, settings: {db: 5s}}
  - id: two
    isolate: {db: shared}
    plugins:
      - {plugin: consumer, config: {name: c2}}
  - id: three
    isolate: {db: true}
    plugins:
      - {id: c3, plugin: consumer, config: {name: c3}}
  - id: unknown
    plugin: probe
    settings: {cache: 1}
`).start()
	for _, event := range []string{"c1 bound main [5s]", "c2 bound main []"} {
		if f.log.count(event) != 1 {
			t.Fatalf("events %q do not contain %q once", f.log.get(), event)
		}
	}
	if got := f.entry("c3"); got.State != cordis.Waiting.String() {
		t.Fatalf("privately isolated consumer = %+v", got)
	}
	unknown := f.entry("unknown")
	if unknown.State != "unresolved" {
		t.Fatalf("entry = %+v", unknown)
	}
	contains(t, unknown.Err.Error(), `"cache"`, "app.yml:17:16")
}

func TestReadOnlyFormat(t *testing.T) {
	const content = `
[[plugins]]
id = "a"
plugin = "probe"
[plugins.config]
name = "a"
`
	f := newFixture(t, "app.toml", content).start()
	err := f.loader.Update(f.ctx(), "a", loader.EntryPatch{Disabled: ptr(true)})
	if !errors.Is(err, loader.ErrReadOnly) {
		t.Fatalf("update = %v", err)
	}
	f.settle()
	f.want("start a port=8080 tags=2")
	if f.entry("a").Disabled || f.read("app.toml") != strings.TrimLeft(content, "\n") {
		t.Fatal("a rejected edit took effect")
	}
}
