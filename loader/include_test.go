package loader_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/loader"
)

const extra = `
plugins:
  - id: cache
    plugin: probe
    config: {name: cache, inPlace: true}
`

func TestIncludedFileHasItsOwnIdentifiersAndWriteBack(t *testing.T) {
	const main = `
plugins:
  - id: extra
    include: sub/extra.yml
`
	f := newFixture(t, "app.yml", main)
	must(t, os.Mkdir(filepath.Join(f.dir, "sub"), 0o755))
	f.write("sub/extra.yml", extra)
	f.start()
	f.want("start cache port=8080 tags=2")
	if got := f.entry("extra:cache"); !strings.HasSuffix(got.Source, "extra.yml") || got.Line != 2 {
		t.Fatalf("entry = %+v", got)
	}
	must(t, f.loader.Update(f.ctx(), "extra:cache", raw(`{"name":"cache","inPlace":true,"port":5}`)))
	f.settle()
	f.want("start cache port=8080 tags=2", "update cache port=5")
	contains(t, f.read("sub/extra.yml"), "port: 5")
	if f.read("app.yml") != strings.TrimLeft(main, "\n") {
		t.Fatal("the edit was written to the including file")
	}
	id, err := f.loader.Create(f.ctx(), "extra", loader.EntryOptions{ID: "more", Plugin: "probe"})
	must(t, err)
	if id != "extra:more" {
		t.Fatalf("id = %q", id)
	}
	contains(t, f.read("sub/extra.yml"), "id: more")

	must(t, f.loader.Update(f.ctx(), "extra", loader.EntryPatch{Disabled: ptr(true)}))
	f.settle()
	if f.log.count("stop cache") != 1 || len(f.entry("extra").Children) != 0 {
		t.Fatalf("events = %q, entry = %+v", f.log.get(), f.entry("extra"))
	}
}

func TestInitialContentCreatesAMissingFile(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: extra
    include: extra.yml
    initial:
      plugins:
        - id: seeded
          plugin: probe
          config: {name: seeded}
`).start()
	f.want("start seeded port=8080 tags=2")
	contains(t, f.read("extra.yml"), "id: seeded")
}

func TestUnreadableIncludedFileIsNotReplacedByInitialContent(t *testing.T) {
	const broken = "plugins:\n  - id: [\n"
	f := newFixture(t, "app.yml", `
plugins:
  - id: extra
    include: extra.yml
    initial: {plugins: []}
  - id: absent
    include: absent.yml
`)
	f.write("extra.yml", broken)
	f.start()
	got := f.entry("extra")
	if got.State != "unresolved" {
		t.Fatalf("entry = %+v", got)
	}
	contains(t, got.Err.Error(), "extra.yml")
	if f.read("extra.yml") != broken {
		t.Fatal("the file was overwritten")
	}
	absent := f.entry("absent")
	if absent.State != "unresolved" || !errors.Is(absent.Err, os.ErrNotExist) {
		t.Fatalf("entry = %+v", absent)
	}
	contains(t, absent.Err.Error(), "app.yml:6:5")
}

const patched = `
plugins:
  - id: extra
    include: extra.yml
    patches:
      - id: cache
        config: {name: patched, inPlace: true}
      - id: ghost
        disabled: true
      - insert:
          - {id: added, plugin: probe, config: {name: added, inPlace: true}}
`

func TestPatchesReplaceKeysAndInsertEntries(t *testing.T) {
	f := newFixture(t, "app.yml", patched)
	f.write("extra.yml", extra)
	f.start()
	f.want("start patched port=8080 tags=2", "start added port=8080 tags=2")
	if f.read("extra.yml") != strings.TrimLeft(extra, "\n") {
		t.Fatal("a patch changed the included file")
	}
	unapplied := f.entry("extra").Unapplied
	if len(unapplied) != 1 {
		t.Fatalf("unapplied = %v", unapplied)
	}
	contains(t, unapplied[0].Error(), `"ghost"`, "app.yml:7:9")
}

func TestEditsAreWrittenToWhereTheKeyComesFrom(t *testing.T) {
	f := newFixture(t, "app.yml", patched)
	f.write("extra.yml", extra)
	f.start()
	must(t, f.loader.Update(f.ctx(), "extra:cache", raw(`{"name":"patched","inPlace":true,"port":7}`)))
	must(t, f.loader.Update(f.ctx(), "extra:cache", loader.EntryPatch{Requires: &[]string{"db"}}))
	must(t, f.loader.Update(f.ctx(), "extra:added", raw(`{"name":"added","inPlace":true,"port":3}`)))
	f.settle()
	main, sub := f.read("app.yml"), f.read("extra.yml")
	contains(t, main, "port: 7", "port: 3")
	contains(t, sub, "requires:", "name: cache")
	if strings.Contains(main, "requires") || strings.Contains(sub, "port") {
		t.Fatalf("keys went to the wrong file:\n%s\n%s", main, sub)
	}
	if f.log.count("update patched port=7") != 1 || f.log.count("update added port=3") != 1 {
		t.Fatalf("events = %q", f.log.get())
	}
	if err := f.loader.Remove(f.ctx(), "extra:added"); !errors.Is(err, loader.ErrPatched) {
		t.Fatalf("remove = %v", err)
	}

	// The written files load to the same tree.
	must(t, f.inst.Stop(f.ctx()))
	again := newFixture(t, "unused.yml", "plugins: []\n")
	again.dir, again.file = f.dir, f.file
	again.start()
	if again.log.count("start added port=3 tags=2") != 1 {
		t.Fatalf("events after restart = %q", again.log.get())
	}
}

func TestLaterPatchOwnsTheKeysItOverridesOnAnInsertedEntry(t *testing.T) {
	f := newFixture(t, "app.yml", `
plugins:
  - id: extra
    include: extra.yml
    patches:
      - insert:
          - {id: added, plugin: probe, config: {name: added}}
      - id: added
        plugin: probe
        config: {name: overridden, inPlace: true}
      - id: cache
        plugin: other
        disabled: true
`)
	f.write("extra.yml", extra)
	f.start()
	f.want("start cache port=8080 tags=2", "start overridden port=8080 tags=2")
	if unapplied := f.entry("extra").Unapplied; len(unapplied) != 1 {
		t.Fatalf("a patch that names another plugin must be skipped: %v", unapplied)
	}
	must(t, f.loader.Update(f.ctx(), "extra:added", raw(`{"name":"overridden","inPlace":true,"port":4}`)))
	f.settle()
	main := f.read("app.yml")
	if strings.Index(main, "port: 4") < strings.Index(main, "id: added\n") {
		t.Fatalf("the edit went to the insert, where the later patch hides it:\n%s", main)
	}
}

func TestPatchesSurviveAReloadOfTheIncludedFile(t *testing.T) {
	f := newFixture(t, "app.yml", patched)
	f.write("extra.yml", extra)
	f.start()
	f.log.reset()
	f.write("extra.yml", extra+`  - id: ghost
    plugin: probe
    config: {name: ghost}
`)
	must(t, f.loader.Reload(f.ctx()))
	f.settle()
	// The new entry is patched (disabled) as soon as it appears; the
	// patched and inserted entries are untouched.
	f.want()
	if got := f.entry("extra:ghost"); !got.Disabled || len(f.entry("extra").Unapplied) != 0 {
		t.Fatalf("entry = %+v", got)
	}

	// Removing the patches from the including file reverts the entries.
	f.write("app.yml", "plugins:\n  - id: extra\n    include: extra.yml\n")
	must(t, f.loader.Reload(f.ctx()))
	f.settle()
	f.want("stop patched", "stop added", "start cache port=8080 tags=2", "start ghost port=8080 tags=2")
	must(t, f.loader.Remove(f.ctx(), "extra:ghost"))
	f.settle()
	if strings.Contains(f.read("extra.yml"), "ghost") {
		t.Fatalf("removal through a composite id was not written:\n%s\n%+v\n%q", f.read("extra.yml"), f.loader.Entries(), f.log.get())
	}
}
