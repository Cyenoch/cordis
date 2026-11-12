package hosting

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

func TestInProcessProviderAndNativeConsumer(t *testing.T) {
	f := newFixture(t)
	user := f.userPlugin("main")
	f.pack(echoManifest("test.user", false, echoRequirement(0, 9)), sdk.Define(user))
	f.open()
	provider := f.echoPlugin("provider", "local:").Load(f.host.Scope(), struct{}{})
	instance := f.enable(contract.Entry{ID: "user", Package: "test.user"})
	must(t, instance.Wait(f.ctx()))
	if f.state("user") != contract.Active {
		t.Fatalf("status = %+v", instance.Status())
	}
	must(t, provider.Stop(f.ctx()))
	eventually(t, "the consumer to wait", func() bool { return f.state("user") == contract.Waiting })
	want := []string{"start provider", "main got local:start", "main got local:cleanup", "stop provider"}
	if got := f.log.get(); !slices.Equal(got, want) {
		t.Fatalf("events = %q", got)
	}
	waiting := instance.Status().Waiting
	if len(waiting) != 1 || waiting[0].Kind != "service" || waiting[0].Requirement.Name != "test.echo" {
		t.Fatalf("waiting = %+v", waiting)
	}
}

func TestNativeProviderAndInProcessConsumer(t *testing.T) {
	f := newFixture(t)
	f.pack(echoManifest("test.provider", true), sdk.Define(f.echoPlugin("main", "native:")))
	f.open()
	user := f.userPlugin("user").Load(f.host.Scope(), struct{}{})
	must(t, user.Wait(f.ctx()))
	if user.Status().State != cordis.Waiting {
		t.Fatalf("user = %+v", user.Status())
	}
	f.enable(contract.Entry{ID: "provider", Package: "test.provider"})
	eventually(t, "the consumer", func() bool { return user.Status().State == cordis.Active })
	must(t, f.host.Disable(f.ctx(), "provider"))
	// The consumer cleaned up, calling the provider, before the provider's
	// process ended.
	want := []string{"start main", "user got native:start", "user got native:cleanup", "stop main"}
	if got := f.log.get(); !slices.Equal(got, want) {
		t.Fatalf("events = %q", got)
	}
	if user.Status().State != cordis.Waiting {
		t.Fatalf("user = %+v", user.Status())
	}
}

// plain is a native definition that only records its start and stop.
func (f *fixture) plain(name string) cordis.Plugin[struct{}] {
	return cordis.Plugin[struct{}]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		f.log.add("start " + name)
		s.Defer(func(context.Context) error {
			f.log.add("stop " + name)
			return nil
		})
		return nil
	}}
}

func plainManifest(id string) contract.Manifest {
	return contract.Manifest{ID: id, Definitions: []contract.DefinitionSpec{{ID: "main"}}}
}

func TestPersistentChildKeepsItsIdentityAcrossParentRestarts(t *testing.T) {
	f := newFixture(t)
	f.pack(echoManifest("test.parent", false, echoRequirement(0, 9)), sdk.Define(f.plain("parent")))
	f.pack(plainManifest("test.child"), sdk.Define(f.plain("child")))
	f.open()
	provider := f.echoPlugin("provider", "").Load(f.host.Scope(), struct{}{})
	parent := f.enable(contract.Entry{ID: "parent", Package: "test.parent"})
	child := f.enable(contract.Entry{ID: "child", Package: "test.child", Parent: "parent"})
	must(t, parent.Wait(f.ctx()))
	eventually(t, "the child", func() bool { return f.state("child") == contract.Active })
	first := child.Status().Identity.Activation

	// The parent restarts because its provider is replaced.
	must(t, provider.Restart(f.ctx()))
	eventually(t, "the child to restart", func() bool {
		status := child.Status()
		return status.State == contract.Active && status.Identity.Activation != first
	})
	want := []string{"start provider", "start parent", "start child", "stop child", "stop parent", "stop provider",
		"start provider", "start parent", "start child"}
	if got := f.log.get(); !slices.Equal(got, want) {
		t.Fatalf("events = %q", got)
	}
	current, err := f.host.Instance("child")
	if err != nil || current != child {
		t.Fatalf("the handle of the child changed: %v", err)
	}
}

func TestFailedChildIsNotRetriedWhenItsParentRestarts(t *testing.T) {
	f := newFixture(t)
	f.pack(echoManifest("test.parent", false, echoRequirement(0, 9)), sdk.Define(f.plain("parent")))
	f.pack(plainManifest("test.child"), sdk.Define(cordis.Plugin[struct{}]{Name: "main",
		Start: func(context.Context, *cordis.Scope, struct{}) error {
			f.log.add("child start")
			return errors.New("child cannot start")
		}}))
	f.open()
	provider := f.echoPlugin("provider", "").Load(f.host.Scope(), struct{}{})
	parent := f.enable(contract.Entry{ID: "parent", Package: "test.parent"})
	child := f.enable(contract.Entry{ID: "child", Package: "test.child", Parent: "parent"})
	must(t, parent.Wait(f.ctx()))
	eventually(t, "the child to fail", func() bool { return f.state("child") == contract.Failed })
	if !strings.Contains(child.Status().Error, "child cannot start") {
		t.Fatalf("status = %+v", child.Status())
	}
	must(t, provider.Restart(f.ctx()))
	must(t, parent.Wait(f.ctx()))
	eventually(t, "the parent", func() bool { return f.state("parent") == contract.Active })
	if f.log.count("child start") != 1 || f.state("child") != contract.Failed {
		t.Fatalf("the failed child was started again: %q, %s", f.log.get(), f.state("child"))
	}
}

func TestInProcessParent(t *testing.T) {
	f := newFixture(t)
	f.pack(plainManifest("test.child"), sdk.Define(f.plain("child")))
	f.open()
	child := f.enable(contract.Entry{ID: "child", Package: "test.child", Parent: "app"})
	eventually(t, "the child to report its missing parent", func() bool { return len(child.Status().Waiting) == 1 })
	waiting := child.Status()
	if waiting.State != contract.Waiting || len(waiting.Waiting) != 1 || waiting.Waiting[0].Kind != "parent" || waiting.Waiting[0].Parent != "app" {
		t.Fatalf("status = %+v", waiting)
	}
	parent := cordis.Plugin[struct{}]{Name: "app", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		f.log.add("start app")
		s.Defer(func(context.Context) error {
			f.log.add("stop app")
			return nil
		})
		return f.host.ProvideParent(s, "app")
	}}
	app := parent.Load(f.host.Scope(), struct{}{})
	eventually(t, "the child", func() bool { return f.state("child") == contract.Active })

	second := cordis.Plugin[struct{}]{Name: "second", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		return f.host.ProvideParent(s, "app")
	}}.Load(f.host.Scope(), struct{}{})
	if err := second.Wait(f.ctx()); !errors.Is(err, contract.Fail(contract.Conflict, "")) {
		t.Fatalf("a second parent with the same id = %v", err)
	}

	must(t, app.Restart(f.ctx()))
	eventually(t, "the child to come back", func() bool {
		return f.log.count("start child") == 2 && f.state("child") == contract.Active
	})
	want := []string{"start app", "start child", "stop child", "stop app", "start app", "start child"}
	if got := f.log.get(); !slices.Equal(got, want) {
		t.Fatalf("events = %q", got)
	}
	entries, err := f.host.Store().Entries()
	must(t, err)
	if len(entries) != 1 || entries[0].ID != "child" || entries[0].Parent != "app" {
		t.Fatalf("the catalog holds %+v", entries)
	}
}

func TestGuestKernelFailures(t *testing.T) {
	f := newFixture(t)
	fail := make(chan struct{})
	f.pack(echoManifest("test.silent", true), sdk.Define(f.plain("silent")))
	f.pack(echoManifest("test.worker", true), sdk.Define(cordis.Plugin[struct{}]{Name: "main",
		Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			s.Go(func(ctx context.Context) error {
				select {
				case <-fail:
					return contract.Fail(contract.Invalid, "worker lost its connection")
				case <-ctx.Done():
					return nil
				}
			})
			return f.echoPlugin("worker", "w:").Start(nil, s, struct{}{})
		}}))
	f.open()

	// Declared but not provided.
	silent := f.enable(contract.Entry{ID: "silent", Package: "test.silent"})
	err := silent.Wait(f.ctx())
	if err == nil || !strings.Contains(err.Error(), "test.echo@1") || f.state("silent") != contract.Failed {
		t.Fatalf("wait = %v", err)
	}
	if _, err := echoKey.Get(f.host.Scope()); err == nil {
		t.Fatal("a service was published for a failed entry")
	}
	// Removing an entry that failed reports that failure.
	if err := f.host.Disable(f.ctx(), "silent"); err == nil || !strings.Contains(err.Error(), "test.echo@1") {
		t.Fatalf("disable of a failed entry = %v", err)
	}
	if _, err := f.host.Status("silent"); err == nil {
		t.Fatal("the failed entry is still registered")
	}

	// A failure while running.
	worker := f.enable(contract.Entry{ID: "worker", Package: "test.worker"})
	must(t, worker.Wait(f.ctx()))
	user := f.userPlugin("user").Load(f.host.Scope(), struct{}{})
	eventually(t, "the consumer", func() bool { return user.Status().State == cordis.Active })
	close(fail)
	eventually(t, "the entry to fail", func() bool { return f.state("worker") == contract.Failed })
	// The code of the plugin's error is reported once, not again in its text.
	if status := worker.Status(); !strings.Contains(status.Error, "worker lost its connection") || strings.Count(status.Error, string(contract.Invalid)) != 1 {
		t.Fatalf("status = %+v", status)
	}
	eventually(t, "the consumer to wait", func() bool { return user.Status().State == cordis.Waiting })
}
