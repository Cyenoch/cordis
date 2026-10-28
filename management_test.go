package cordis

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
	"github.com/Cyenoch/cordis/schema"
)

func TestResumeWaitDoesNotBlockLateProviderOrUnrelatedManagement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, err := Open(Options{Directory: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := h.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
		req := contract.Requirement{Name: "late", Major: 1, MinMinor: 1, MaxMinor: 3}
		consumer, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "consumer"}, contract.Definition{Requires: []contract.Requirement{req}, Start: func(context.Context, contract.Scope) error { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		if err := consumer.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- h.Resume(t.Context(), "consumer") }()
		synctest.Wait()
		if consumer.Status().State != contract.Waiting {
			t.Fatal(consumer.Status())
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		provider, err := h.AddBuiltin(ctx, contract.Entry{ID: "provider"}, contract.Definition{Provides: []contract.Service{{Name: "late", Major: 1, Minor: 2}}, Start: func(_ context.Context, s contract.Scope) error {
			return s.Provide(contract.Service{Name: "late", Major: 1, Minor: 2}, func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
		}})
		if err != nil {
			t.Fatal(err)
		}
		if err := provider.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("Resume blocked provider admission")
		}
	})
}

func TestRestoreIsPartialRepeatableAndDoesNotUndoTemporaryStop(t *testing.T) {
	h, pkg, _ := lifecycleHost(t)
	for _, entry := range []contract.Entry{{ID: "a", Package: pkg.ID, Definition: "main", Enabled: true}, {ID: "b", Package: pkg.ID, Definition: "main", Realm: "later", Enabled: true}} {
		if err := h.store.SaveEntry(t.Context(), entry); err != nil {
			t.Fatal(err)
		}
	}
	report, err := h.Restore(t.Context())
	if err == nil || len(report.Entries) != 2 {
		t.Fatal(report, err)
	}
	a, err := h.Instance("a")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	identity := a.Status().Identity
	if err := h.ConfigureRealm("later", "", nil); err != nil {
		t.Fatal(err)
	}
	report, err = h.Restore(t.Context())
	if err != nil {
		t.Fatal(report, err)
	}
	current, _ := h.Instance("a")
	if current != a || a.Status().State != contract.Stopped || a.Status().Identity != identity {
		t.Fatal("Restore restarted a temporarily stopped instance")
	}
	b, err := h.Instance("b")
	if err != nil || b.Status().State != contract.Active {
		t.Fatal(b, err)
	}
	report, err = h.Restore(t.Context())
	if err != nil || report.Entries[0].Action != "existing" {
		t.Fatal(report, err)
	}
	list := h.Instances()
	list[0].Entry.Config[0] = '!'
	again := h.Instances()
	if string(again[0].Entry.Config) != "{}" {
		t.Fatal("mutable instance list")
	}
}

func TestBlockedDisableAllowsUnrelatedAdmissionAndCloseCancellation(t *testing.T) {
	h, err := Open(Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	i, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "slow"}, contract.Definition{Start: func(_ context.Context, s contract.Scope) error {
		return s.Defer(func(context.Context) error { close(entered); <-release; return nil })
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- h.Disable(t.Context(), "slow") }()
	<-entered
	status, err := h.Status("slow")
	if err != nil || status.State != contract.Quiescing || status.Enabled || status.TemporaryStopped || i.Status().Enabled {
		close(release)
		t.Fatal("persistent disable disagrees with core status", status, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	other, err := h.AddBuiltin(ctx, contract.Entry{ID: "other"}, contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }})
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	if err := other.Wait(ctx); err != nil {
		close(release)
		t.Fatal(err)
	}
	if err := h.Resume(ctx, "slow"); err == nil {
		close(release)
		t.Fatal("conflicting same-tree resume admitted")
	}
	closeCtx, stop := context.WithCancel(t.Context())
	stop()
	if err := h.Close(closeCtx); !errors.Is(err, context.Canceled) {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestTemporaryStopStatusMatchesCoreDuringCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, err := Open(Options{Directory: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := h.Close(context.Background()); err != nil {
				t.Error(err)
			}
		}()
		entered, release := make(chan struct{}), make(chan struct{})
		defer close(release)
		i, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "temporary"}, contract.Definition{Start: func(_ context.Context, s contract.Scope) error {
			return s.Defer(func(context.Context) error { close(entered); <-release; return nil })
		}})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- i.Stop(ctx) }()
		<-entered
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		status, err := h.Status("temporary")
		if err != nil {
			t.Fatal(err)
		}
		for _, status := range []contract.Status{status, i.Status(), h.Instances()[0].Status} {
			if status.State != contract.Quiescing || !status.Enabled || !status.TemporaryStopped {
				t.Fatalf("temporary stop disagrees with core status: %+v", status)
			}
		}
	})
}

func TestDefinitionPreflightDoesNotCommitOrRetireInvalidUpdates(t *testing.T) {
	h, pkg, _ := lifecycleHost(t)
	var starts int
	validate := func(raw json.RawMessage) error {
		return schema.Validate(json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]}`), raw)
	}
	def := contract.Definition{ID: "count", Validate: validate, Start: func(context.Context, contract.Scope) error { starts++; return nil }}
	if _, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "absent"}, def); err == nil {
		t.Fatal("required config bypassed by omission")
	}
	i, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "count", Config: json.RawMessage(`{"count":1}`)}, def)
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := i.Status()
	if _, err := i.Update(t.Context(), json.RawMessage(`{"other":true}`)); err == nil {
		t.Fatal("invalid update accepted")
	}
	after := i.Status()
	if after.Identity != before.Identity || after.Desired != before.Desired || starts != 1 {
		t.Fatal("invalid update changed activation", after)
	}
	external, err := h.Enable(t.Context(), contract.Entry{ID: "external", Package: pkg.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := external.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	before = external.Status()
	if _, err := external.Update(t.Context(), json.RawMessage(`{"unexpected":true}`)); err == nil {
		t.Fatal("external schema bypass")
	}
	entries, err := h.store.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if string(entries[0].Config) != "{}" || external.Status().Identity != before.Identity {
		t.Fatal("invalid external update committed")
	}
}

func TestDefaultDispatcherDelegatesOnceAndPreservesFailure(t *testing.T) {
	h, err := Open(Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := h.RegisterDefinition(t.Context(), "builtin", contract.Definition{ID: "child", Start: func(context.Context, contract.Scope) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	h.options.Authorize = func(context.Context, contract.Identity, contract.Capability) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	if err := h.Broker().Register("delegated", func(context.Context, contract.Identity, json.RawMessage, json.RawMessage, json.RawMessage) error {
		return nil
	}, func(context.Context, contract.Identity, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`true`), nil
	}); err != nil {
		t.Fatal(err)
	}
	original := errors.New("local scope failure")
	calls := 0
	h.options.HandleGuest = func(ctx context.Context, s contract.Scope, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method == "mine" {
			calls++
			return nil, original
		}
		return h.DispatchGuest(ctx, s, method, raw)
	}
	scopes := make(chan contract.Scope, 1)
	i, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "caller"}, contract.Definition{Capabilities: []contract.Capability{{ID: "delegated", Scope: json.RawMessage(`{}`)}}, Start: func(_ context.Context, s contract.Scope) error { scopes <- s; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	scope := <-scopes
	if _, err := h.handleGuest(t.Context(), scope, "mine", json.RawMessage(`{}`)); !errors.Is(err, original) || calls != 1 {
		t.Fatal(calls, err)
	}
	data, _ := json.Marshal(contract.TaskRequest{ID: "one", Token: "token"})
	if _, err := h.handleGuest(t.Context(), scope, "task.begin", data); err != nil {
		t.Fatal(err)
	}
	tasks := h.tasks.Snapshot()
	if len(tasks) != 1 || tasks[0].ID != "one" {
		t.Fatal(tasks)
	}
	data, _ = json.Marshal(contract.HostCall{Capability: "delegated", Input: json.RawMessage(`{}`)})
	if out, err := h.handleGuest(t.Context(), scope, "host.invoke", data); err != nil || string(out) != "true" {
		t.Fatal(string(out), err)
	}
	data, _ = json.Marshal(contract.ChildRequest{Action: "create", Child: contract.Child{ID: "created", Definition: "child"}})
	if _, err := h.handleGuest(t.Context(), scope, "child", data); err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(contract.ChildRequest{Action: "wait", ID: "created"})
	if _, err := h.handleGuest(t.Context(), scope, "child", data); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("delegation replayed custom business method")
	}
}

func TestStopAndDisableDifferAfterReopeningHost(t *testing.T) {
	h, pkg, _ := lifecycleHost(t)
	for _, id := range []string{"temporary", "durable"} {
		i, err := h.Enable(t.Context(), contract.Entry{ID: id, Package: pkg.ID})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	temporary, _ := h.Instance("temporary")
	if err := temporary.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := h.Disable(t.Context(), "durable"); err != nil {
		t.Fatal(err)
	}
	options := h.options
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	report, err := reopened.Restore(t.Context())
	if err != nil {
		t.Fatal(report, err)
	}
	if status, err := reopened.Status("temporary"); err != nil || status.State != contract.Active {
		t.Fatal(status, err)
	}
	if _, err := reopened.Instance("durable"); err == nil {
		t.Fatal("durably disabled instance restored")
	}
	entries, err := reopened.store.Entries()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.ID == "durable" && entry.Enabled {
			t.Fatal("disable intent not saved")
		}
	}
}

func TestRemoveDeletesOwnedRecordsButNotSelectedCode(t *testing.T) {
	h, pkg, _ := lifecycleHost(t)
	parent, err := h.Enable(t.Context(), contract.Entry{ID: "parent-remove", Package: pkg.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	child, err := h.Enable(t.Context(), contract.Entry{ID: "child-remove", Package: pkg.ID, Parent: "parent-remove"})
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := h.Remove(t.Context(), "parent-remove"); err != nil {
		t.Fatal(err)
	}
	entries, err := h.store.Entries()
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	if p, err := h.store.Selected(pkg.ID); err != nil || p.Ref != pkg {
		t.Fatal(p, err)
	}
	if _, err := parent.Update(t.Context(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("removed handle still current")
	}
}

func TestHostUpdateCanBeSupersededAndStopped(t *testing.T) {
	h, err := Open(Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := h.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	entered := make(chan struct{}, 2)
	i, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "updated", Config: json.RawMessage(`1`)}, contract.Definition{Start: func(ctx context.Context, s contract.Scope) error {
		if string(s.Config()) == "2" || string(s.Config()) == "4" {
			entered <- struct{}{}
			<-ctx.Done()
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	type update struct {
		result contract.UpdateResult
		err    error
	}
	pending := make(chan update, 1)
	go func() { r, err := i.Update(t.Context(), json.RawMessage(`2`)); pending <- update{r, err} }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if r, err := i.Update(ctx, json.RawMessage(`3`)); err != nil || !r.Applied {
		t.Fatalf("latest revision did not apply: %+v %v", r, err)
	}
	if r := <-pending; r.err != nil || !r.result.Superseded {
		t.Fatalf("previous update not superseded: %+v", r)
	}
	go func() { r, err := i.Update(t.Context(), json.RawMessage(`4`)); pending <- update{r, err} }()
	<-entered
	if err := i.Stop(ctx); err != nil {
		t.Fatalf("Stop blocked by update waiter: %v", err)
	}
	if r := <-pending; r.err == nil || r.result.Applied {
		t.Fatalf("stopped update reported success: %+v", r)
	}
}

func TestCanceledCloseBeforeSubmissionLeavesHostOpen(t *testing.T) {
	h, err := Open(Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := h.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for range 20 {
		if err := h.Close(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	i, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "still-open"}, contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }})
	if err != nil {
		t.Fatalf("canceled Close committed shutdown: %v", err)
	}
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

type blockedShutdownRuntime struct{ entered, release chan struct{} }

func (r blockedShutdownRuntime) Start(context.Context, contract.Launch) (contract.Session, error) {
	return &blockedShutdownSession{fixtureSession: fixtureSession{done: make(chan struct{})}, entered: r.entered, release: r.release}, nil
}

type blockedShutdownSession struct {
	fixtureSession
	entered, release chan struct{}
}

func (s *blockedShutdownSession) Call(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if method == "shutdown" {
		close(s.entered)
		<-s.release
		return json.RawMessage(`{}`), nil
	}
	return s.fixtureSession.Call(ctx, method, raw)
}

func TestCanceledDisableCannotResumeDurablyDisabledEntry(t *testing.T) {
	h, pkg, _ := lifecycleHost(t)
	entered, release := make(chan struct{}), make(chan struct{})
	h.options.Runtimes["wasm"] = blockedShutdownRuntime{entered, release}
	i, err := h.Enable(t.Context(), contract.Entry{ID: "disabled", Package: pkg.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Disable(ctx, "disabled") }()
	<-entered
	cancel()
	err = <-done
	close(release)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := i.(*hostInstance).Instance.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := h.Resume(t.Context(), "disabled"); err == nil {
		t.Fatal("durably disabled entry resumed")
	}
	status, err := h.Status("disabled")
	if err != nil || status.Enabled || status.TemporaryStopped || status.State != contract.Stopped || i.Status().Enabled {
		t.Fatal(status, err)
	}
	if listed := h.Instances(); len(listed) != 1 || listed[0].Status.Enabled || listed[0].Status.TemporaryStopped {
		t.Fatal("instance list overrode persistent disable", listed)
	}
	if err := h.Disable(t.Context(), "disabled"); err != nil {
		t.Fatal(err)
	}
}

type blockFirstShutdownRuntime struct {
	calls            atomic.Int32
	entered, release chan struct{}
}

func (r *blockFirstShutdownRuntime) Start(ctx context.Context, launch contract.Launch) (contract.Session, error) {
	if r.calls.Add(1) == 1 {
		return blockedShutdownRuntime{r.entered, r.release}.Start(ctx, launch)
	}
	return fixtureRuntime{}.Start(ctx, launch)
}
func TestRestoreCannotStartOldPackageDuringReplacement(t *testing.T) {
	h, p1, p2 := lifecycleHost(t)
	entered, release := make(chan struct{}), make(chan struct{})
	adapter := &blockFirstShutdownRuntime{entered: entered, release: release}
	h.options.Runtimes["wasm"] = adapter
	i, err := h.Enable(t.Context(), contract.Entry{ID: "live", Package: p1.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := h.Store().SaveEntry(t.Context(), contract.Entry{ID: "dormant", Package: p1.ID, Definition: "main", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- h.Replace(t.Context(), p2, packagehost.ChangeOptions{}) }()
	<-entered
	report, err := h.Restore(t.Context())
	if err == nil {
		close(release)
		t.Fatalf("Restore bypassed replacement: %+v", report)
	}
	found := false
	for _, entry := range report.Entries {
		if entry.ID == "dormant" {
			found = entry.Action == "conflict"
		}
	}
	if !found || adapter.calls.Load() != 1 {
		close(release)
		t.Fatalf("Restore started old code: %+v calls=%d", report, adapter.calls.Load())
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if current, err := h.Store().Selected(p1.ID); err != nil || current.Ref != p2 {
		t.Fatal(current, err)
	}
	if report, err := h.Restore(t.Context()); err != nil {
		t.Fatal(report, err)
	}
	if info, err := h.Instance("dormant"); err != nil || info.Status().Identity.Digest != p2.Digest {
		t.Fatal("Restore did not use replacement", err)
	}
}
