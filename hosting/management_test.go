package hosting

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/loader"
	"github.com/Cyenoch/cordis/sdk"
)

// configured is a definition whose configuration must be {"label": string}.
func (f *fixture) configured() (contract.Manifest, sdk.Definition) {
	type config struct {
		Label string `json:"label"`
	}
	manifest := contract.Manifest{ID: "test.configured", Definitions: []contract.DefinitionSpec{{ID: "main",
		ConfigSchema: []byte(`{"type":"object","properties":{"label":{"type":"string"}},"required":["label"],"additionalProperties":false}`)}}}
	return manifest, sdk.Define(cordis.Plugin[config]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, c config) error {
		f.log.add("start " + c.Label)
		s.Defer(func(context.Context) error {
			f.log.add("stop " + c.Label)
			return nil
		})
		if c.Label == "slow" {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}})
}

func TestUpdateValidatesBeforeItSavesOrStops(t *testing.T) {
	f := newFixture(t)
	f.pack(f.configured())
	f.open()
	if _, err := f.host.Enable(f.ctx(), contract.Entry{ID: "bad", Package: "test.configured", Config: []byte(`{"label":1}`)}); contract.ErrorCode(err) != contract.Invalid {
		t.Fatal("an entry with an invalid configuration was enabled")
	}
	if entries, _ := f.host.Store().Entries(); len(entries) != 0 {
		t.Fatalf("a rejected entry was saved: %+v", entries)
	}
	instance := f.enable(contract.Entry{ID: "entry", Package: "test.configured", Config: []byte(`{"label":"one"}`)})
	must(t, instance.Wait(f.ctx()))
	if _, err := instance.Update(f.ctx(), []byte(`{"label":"two","extra":true}`)); contract.ErrorCode(err) != contract.Invalid {
		t.Fatal("an invalid update was accepted")
	}
	if got := f.log.get(); !slices.Equal(got, []string{"start one"}) {
		t.Fatalf("a rejected update disturbed the entry: %q", got)
	}
	result, err := instance.Update(f.ctx(), []byte(`{"label":"two"}`))
	if err != nil || !result.Applied || result.Revision != 2 {
		t.Fatalf("update = %+v, %v", result, err)
	}
	if status := instance.Status(); status.Desired != 2 || status.Applied != 2 || status.State != contract.Active {
		t.Fatalf("status = %+v", status)
	}
	entries, _ := f.host.Store().Entries()
	if len(entries) != 1 || string(entries[0].Config) != `{"label":"two"}` {
		t.Fatalf("saved entries = %+v", entries)
	}
	if got := f.log.get(); !slices.Equal(got, []string{"start one", "stop one", "start two"}) {
		t.Fatalf("events = %q", got)
	}
}

func TestLaterUpdateSupersedesAnEarlierOne(t *testing.T) {
	f := newFixture(t)
	f.pack(f.configured())
	f.open()
	instance := f.enable(contract.Entry{ID: "entry", Package: "test.configured", Config: []byte(`{"label":"one"}`)})
	must(t, instance.Wait(f.ctx()))
	first := make(chan contract.UpdateResult, 1)
	go func() {
		result, _ := instance.Update(f.ctx(), []byte(`{"label":"slow"}`))
		first <- result
	}()
	eventually(t, "the slow revision to start", func() bool { return f.log.count("start slow") == 1 })
	result, err := instance.Update(f.ctx(), []byte(`{"label":"three"}`))
	if err != nil || !result.Applied || result.Revision != 3 {
		t.Fatalf("update = %+v, %v", result, err)
	}
	if superseded := <-first; !superseded.Superseded || superseded.Applied {
		t.Fatalf("the earlier update = %+v", superseded)
	}
}

func TestStopResumeDisableAndRestore(t *testing.T) {
	f := newFixture(t)
	f.pack(f.configured())
	f.open()
	instance := f.enable(contract.Entry{ID: "entry", Package: "test.configured", Config: []byte(`{"label":"one"}`)})
	off := f.enable(contract.Entry{ID: "off", Package: "test.configured", Config: []byte(`{"label":"off"}`)})
	must(t, instance.Wait(f.ctx()))
	must(t, off.Wait(f.ctx()))

	// A temporary stop keeps the entry, its handle and its saved intent.
	must(t, instance.Stop(f.ctx()))
	if status := instance.Status(); status.State != contract.Stopped || !status.TemporaryStopped || !status.Enabled {
		t.Fatalf("status = %+v", status)
	}
	if report, err := f.host.Restore(f.ctx()); err != nil || f.state("entry") != contract.Stopped {
		t.Fatalf("restore undid a temporary stop: %+v, %v", report, err)
	}
	must(t, f.host.Resume(f.ctx(), "entry"))
	if f.state("entry") != contract.Active || f.log.count("start one") != 2 {
		t.Fatalf("resume: %s, %q", f.state("entry"), f.log.get())
	}

	// Disabling removes the registration and saves the intent.
	must(t, f.host.Disable(f.ctx(), "off"))
	if _, err := f.host.Status("off"); err == nil {
		t.Fatal("a disabled entry is still registered")
	}
	if err := f.host.Resume(f.ctx(), "off"); err == nil {
		t.Fatal("a disabled entry was resumed")
	}
	must(t, instance.Stop(f.ctx()))

	// After reopening, Restore starts what is enabled, including what was
	// only stopped temporarily, and leaves what is disabled.
	must(t, f.host.Close(f.ctx()))
	if _, err := f.host.Enable(f.ctx(), contract.Entry{ID: "late", Package: "test.configured"}); err == nil {
		t.Fatal("a closed host admitted an entry")
	}
	f.open()
	report, err := f.host.Restore(f.ctx())
	must(t, err)
	actions := map[string]string{}
	for _, entry := range report.Entries {
		actions[entry.ID] = entry.Action
	}
	if actions["entry"] != "registered" || actions["off"] != "disabled" || f.state("entry") != contract.Active {
		t.Fatalf("restore = %+v", report)
	}
	if again, err := f.host.Restore(f.ctx()); err != nil || again.Entries[0].Action != "existing" {
		t.Fatalf("a second restore = %+v, %v", again, err)
	}

	// Remove deletes the record, not the package.
	must(t, f.host.Remove(f.ctx(), "entry"))
	entries, _ := f.host.Store().Entries()
	if len(entries) != 1 || entries[0].ID != "off" {
		t.Fatalf("entries after remove = %+v", entries)
	}
	if _, err := f.host.Store().Selected("test.configured"); err != nil {
		t.Fatalf("remove touched the package: %v", err)
	}
}

func TestReplaceRestartsTheEntriesOfAPackage(t *testing.T) {
	f := newFixture(t)
	manifest, definition := f.configured()
	first := f.pack(manifest, definition)
	manifest.Version = "2.0.0"
	second := f.pack(manifest, definition)
	f.open()
	must(t, f.host.Select(f.ctx(), first))
	instance := f.enable(contract.Entry{ID: "entry", Package: "test.configured", Config: []byte(`{"label":"one"}`)})
	must(t, instance.Wait(f.ctx()))
	if _, err := f.host.Uninstall(f.ctx(), first); err == nil {
		t.Fatal("a package in use was uninstalled")
	}
	must(t, f.host.Replace(f.ctx(), second))
	replaced, err := f.host.Instance("entry")
	must(t, err)
	if status := replaced.Status(); status.State != contract.Active || status.Identity.Digest != second.Digest {
		t.Fatalf("status = %+v", status)
	}
	if _, err := instance.Update(f.ctx(), []byte(`{"label":"x"}`)); !errors.Is(err, contract.Fail(contract.NotReady, "")) {
		t.Fatalf("the old handle still controls the entry: %v", err)
	}
	if got := f.log.get(); !slices.Equal(got, []string{"start one", "stop one", "start one"}) {
		t.Fatalf("events = %q", got)
	}
}

func TestRecoveryRetriesWithinItsBudget(t *testing.T) {
	f := newFixture(t)
	failing := func(name string, failures int) sdk.Definition {
		return sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(context.Context, *cordis.Scope, struct{}) error {
			f.log.add(name)
			if f.log.count(name) <= failures {
				return errors.New(name + " is not ready")
			}
			return nil
		}})
	}
	f.pack(plainManifest("test.flaky"), failing("flaky", 3))
	f.pack(plainManifest("test.broken"), failing("broken", 1000))
	f.open()
	for _, id := range []string{"flaky", "broken"} {
		if err := f.enable(contract.Entry{ID: id, Package: "test." + id}).Wait(f.ctx()); err == nil {
			t.Fatalf("%s started", id)
		}
	}
	// A failed entry is not restarted unless recovery is configured for it:
	// nothing is scheduled, so nothing can start it again.
	if scheduled := f.host.RecoveryStatus(); len(scheduled) != 0 || f.log.count("flaky") != 1 {
		t.Fatalf("without a policy: recovery %+v, attempts %q", scheduled, f.log.get())
	}
	f.host.Close(f.ctx()) // reports the failures of the two entries

	// Recovery is configured for entries that exist when the host opens.
	policy := RecoveryPolicy{MaxRetries: 2, InitialDelay: 10 * time.Millisecond, MaxDelay: 20 * time.Millisecond}
	f.open(func(o *Options) { o.Recovery = map[string]RecoveryPolicy{"flaky": policy, "broken": policy} })
	f.host.Restore(f.ctx())
	eventually(t, "flaky to recover", func() bool { return f.state("flaky") == contract.Active })
	eventually(t, "broken to exhaust its budget", func() bool {
		for _, info := range f.host.RecoveryStatus() {
			if info.Entry == "broken" && info.State == RecoveryExhausted {
				return true
			}
		}
		return false
	})
	// One start by Restore and two retries each; flaky needed all of them.
	if f.log.count("flaky") != 4 || f.log.count("broken") != 4 || f.state("broken") != contract.Failed {
		t.Fatalf("attempts: flaky %d, broken %d", f.log.count("flaky"), f.log.count("broken"))
	}
	// Exhausted is final: no further retry is scheduled.
	for _, info := range f.host.RecoveryStatus() {
		if info.Entry == "broken" && (info.State != RecoveryExhausted || info.NextRetryAt != nil || info.Used != info.MaxRetries) {
			t.Fatalf("an exhausted entry may be retried: %+v", info)
		}
	}
	// A manual retry is one more attempt and a fresh budget.
	if err := f.host.Retry(f.ctx(), "broken"); err == nil || !strings.Contains(err.Error(), "broken is not ready") {
		t.Fatalf("retry = %v", err)
	}
	eventually(t, "the new budget to be used", func() bool { return f.log.count("broken") == 7 })
}

func TestLoaderRunsNativePackages(t *testing.T) {
	f := newFixture(t)
	ref := f.pack(echoManifest("test.provider", true), sdk.Define(f.echoPlugin("main", "native:")))
	f.open()
	file := filepath.Join(t.TempDir(), "app.yml")
	must(t, os.WriteFile(file, []byte(`plugins:
  - id: echo
    plugin: package:test.provider
  - id: user
    plugin: user
  - id: missing
    plugin: package:test.nowhere
`), 0o600))
	registry := loader.NewRegistry()
	must(t, loader.Register(registry, "user", f.userPlugin("user")))
	before, err := f.host.Store().Entries()
	must(t, err)
	instance := loader.Plugin.Load(f.host.Scope(), loader.Config{File: file, Registry: registry, Resolvers: []loader.Resolver{f.host.Resolver()}})
	must(t, instance.Wait(f.ctx()))
	eventually(t, "the in-process consumer of the native endpoint", func() bool { return f.log.count("user got native:start") == 1 })

	infos := f.host.Instances()
	if len(infos) != 1 || infos[0].Origin != "dynamic" || infos[0].Package != ref || infos[0].Status.State != contract.Active {
		t.Fatalf("instances = %+v", infos)
	}
	after, err := f.host.Store().Entries()
	must(t, err)
	if len(before) != 0 || len(after) != 0 {
		t.Fatalf("the loader touched the catalog: %+v", after)
	}
	if _, err := f.host.Uninstall(f.ctx(), ref); err == nil {
		t.Fatal("a package that a loader entry runs was uninstalled")
	}
	if f.host.Instances()[0].Status.State != contract.Active {
		t.Fatal("the rejected uninstall disturbed the entry")
	}

	// A package that is not selected fails like Enable does, without a process.
	_, enableErr := f.host.Enable(f.ctx(), contract.Entry{ID: "x", Package: "test.nowhere"})
	found := make(chan *loader.Loader, 1)
	cordis.Plugin[struct{}]{Name: "grab", Requires: []cordis.Requirement{loader.Ready},
		Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			l, err := loader.Service.Get(s)
			found <- l
			return err
		}}.Load(f.host.Scope(), struct{}{})
	l := <-found
	for _, entry := range l.Entries() {
		if entry.ID == "missing" && (entry.Err == nil || enableErr == nil || !strings.Contains(entry.Err.Error(), enableErr.Error())) {
			t.Fatalf("unselected package: loader %v, enable %v", entry.Err, enableErr)
		}
	}

	must(t, instance.Stop(f.ctx()))
	if len(f.host.Instances()) != 0 {
		t.Fatalf("instances after the loader stopped: %+v", f.host.Instances())
	}
	if _, err := f.host.Uninstall(f.ctx(), ref); err != nil {
		t.Fatalf("uninstall after the entry stopped: %v", err)
	}
}

// broken is a definition whose Start always fails.
func broken(name string) cordis.Plugin[struct{}] {
	return cordis.Plugin[struct{}]{Name: name, Start: func(context.Context, *cordis.Scope, struct{}) error {
		return errors.New("broken on purpose")
	}}
}

func TestRetiringAFailedEntrySucceeds(t *testing.T) {
	f := newFixture(t)
	f.pack(plainManifest("test.broken"), sdk.Define(broken("main")))
	f.open()
	for _, id := range []string{"disabled", "removed", "stopped"} {
		if err := f.enable(contract.Entry{ID: id, Package: "test.broken"}).Wait(f.ctx()); err == nil || !strings.Contains(err.Error(), "broken on purpose") {
			t.Fatalf("wait for %s = %v", id, err)
		}
	}

	// The stop itself went fine; the failure is history, not its result.
	must(t, f.host.Remove(f.ctx(), "removed"))
	must(t, f.host.Disable(f.ctx(), "disabled"))
	if _, err := f.host.Status("disabled"); err == nil {
		t.Fatal("the disabled entry is still registered")
	}
	entries, err := f.host.Store().Entries()
	must(t, err)
	saved := map[string]bool{}
	for _, entry := range entries {
		saved[entry.ID] = entry.Enabled
	}
	if enabled, exists := saved["disabled"]; !exists || enabled {
		t.Fatalf("the disabled entry is saved as %+v", entries)
	}
	if _, exists := saved["removed"]; exists {
		t.Fatalf("the removed entry is still saved: %+v", entries)
	}
	runs, err := f.host.Store().Runs()
	must(t, err)
	failed := map[string]bool{}
	for _, run := range runs {
		if run.State == "failed" {
			failed[run.Owner.Entry] = true
		}
	}
	if !failed["disabled"] || !failed["removed"] {
		t.Fatalf("the earlier failures are not in the history: %+v", runs)
	}

	stopped, err := f.host.Instance("stopped")
	must(t, err)
	must(t, stopped.Stop(f.ctx()))
	if status := stopped.Status(); status.State != contract.Stopped || !status.TemporaryStopped || status.Outcome.FailureCode == "" {
		t.Fatalf("status of the stopped entry = %+v", status)
	}
}

func TestStoppingAFailedDynamicChildSucceeds(t *testing.T) {
	f := newFixture(t)
	created := make(chan error, 1)
	parent := cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		_, err := sdk.CreateChild(ctx, s, contract.Child{ID: "child", Definition: "broken"})
		created <- err
		return nil
	}}
	manifest := contract.Manifest{ID: "test.parent", Definitions: []contract.DefinitionSpec{{ID: "main"}, {ID: "broken"}}}
	f.pack(manifest, sdk.Define(parent), sdk.Define(broken("broken")))
	f.open()
	must(t, f.enable(contract.Entry{ID: "parent", Package: "test.parent"}).Wait(f.ctx()))
	must(t, <-created)
	child, err := f.host.Instance("parent.child")
	must(t, err)
	if err := child.Wait(f.ctx()); err == nil {
		t.Fatal("the broken child started")
	}
	must(t, child.Stop(f.ctx()))
	if _, err := f.host.Instance("parent.child"); err == nil {
		t.Fatal("the stopped child is still registered")
	}
}

func TestDisableReportsWhatGoesWrongWhileStopping(t *testing.T) {
	f := newFixture(t)
	f.pack(plainManifest("test.messy"), sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		s.Defer(func(context.Context) error { return errors.New("cleanup went wrong") })
		return nil
	}}))
	f.open()
	must(t, f.enable(contract.Entry{ID: "messy", Package: "test.messy"}).Wait(f.ctx()))
	stopped := f.enable(contract.Entry{ID: "stopped", Package: "test.messy"})
	must(t, stopped.Wait(f.ctx()))
	// The guest reports that a cleanup failed; its text stays in the guest.
	if err := f.host.Disable(f.ctx(), "messy"); contract.ErrorCode(err) != contract.PluginFailed {
		t.Fatalf("disable = %v", err)
	}
	if err := stopped.Stop(f.ctx()); contract.ErrorCode(err) != contract.PluginFailed {
		t.Fatalf("stop = %v", err)
	}
	if status := stopped.Status(); status.State != contract.Stopped || !status.TemporaryStopped {
		t.Fatalf("status after a stop whose cleanup failed = %+v", status)
	}
}

func TestStatusRightAfterEnableIsStartingNotReasonlessWaiting(t *testing.T) {
	f := newFixture(t)
	f.pack(plainManifest("test.plain"), sdk.Define(f.plain("plain")))
	f.pack(echoManifest("test.user", false, echoRequirement(0, 9)), sdk.Define(f.plain("user")))
	f.open()
	check := func(id string, status contract.Status) {
		t.Helper()
		if status.State == contract.Waiting && len(status.Waiting) == 0 {
			t.Fatalf("%s waits without a reason: %+v", id, status)
		}
		if status.State == contract.Starting && status.Applied == status.Desired {
			t.Fatalf("%s reports a revision applied while it starts: %+v", id, status)
		}
	}
	for n := range 20 {
		id := fmt.Sprintf("plain%d", n)
		instance := f.enable(contract.Entry{ID: id, Package: "test.plain"})
		check(id, instance.Status())
		must(t, instance.Wait(f.ctx()))
		if status := instance.Status(); status.State != contract.Active || status.Applied != status.Desired {
			t.Fatalf("%s after Wait = %+v", id, status)
		}
	}
	// An entry that does wait says why from the first status on.
	user := f.enable(contract.Entry{ID: "user", Package: "test.user"})
	eventually(t, "the user to wait for its service", func() bool {
		status := user.Status()
		check("user", status)
		return status.State == contract.Waiting
	})
}

// A stop that interleaves with an Update can end with a new failure of the
// same code as the one the entry had failed with before; that one is the
// stop's own and must be reported.
func TestStopResultKeepsANewFailureWithTheSameCode(t *testing.T) {
	old := contract.Fail(contract.PluginFailed, "failed before the stop")
	before := cordis.Status{State: cordis.Failed, Err: old}
	if err := since(before, old); err != nil {
		t.Fatalf("the earlier failure was reported: %v", err)
	}
	if err := since(before, errors.Join(old, errors.New("its own cleanup"))); err != nil {
		t.Fatalf("the earlier failure with its own cleanup was reported: %v", err)
	}
	fresh := contract.Fail(contract.PluginFailed, "failed while stopping")
	if err := since(before, fresh); err != fresh {
		t.Fatalf("a new failure with the same code = %v", err)
	}
	if err := since(before, errors.Join(fresh)); err == nil {
		t.Fatal("a joined new failure with the same code was dropped")
	}
}
