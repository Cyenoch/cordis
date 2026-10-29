package cordis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

type fixtureRuntime struct{}
type fixtureSession struct {
	done chan struct{}
	once sync.Once
}

func (fixtureRuntime) Start(context.Context, contract.Launch) (contract.Session, error) {
	return &fixtureSession{done: make(chan struct{})}, nil
}
func (s *fixtureSession) Call(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
	if method == "initialize" {
		return json.RawMessage(`{"frameBytes":1048576,"minor":0,"features":[],"services":[]}`), nil
	}
	return json.RawMessage(`{}`), nil
}
func (s *fixtureSession) CallWithReceipt(ctx context.Context, method string, input json.RawMessage, receipt func(bool)) (json.RawMessage, error) {
	if receipt != nil {
		receipt(true)
	}
	return s.Call(ctx, method, input)
}
func (s *fixtureSession) Done() <-chan struct{} { return s.done }
func (s *fixtureSession) Err() error            { return nil }
func (s *fixtureSession) Close() error          { s.once.Do(func() { close(s.done) }); return nil }

func lifecycleHost(t *testing.T) (*Host, contract.PackageRef, contract.PackageRef) {
	t.Helper()
	var archives []string
	trust := packagehost.TrustPolicy{DevDigests: map[string]bool{}, Publishers: map[string][]string{"test.publisher": {"test.package"}}}
	for _, version := range []string{"1.0.0", "2.0.0"} {
		source := t.TempDir()
		m := contract.Manifest{Schema: contract.PackageSchema, ID: "test.package", Version: version, Publisher: "test.publisher", Protocol: contract.Protocol{Major: 1}, Definitions: []contract.DefinitionSpec{{ID: "main", ConfigSchema: json.RawMessage(`{"type":"object","properties":{}}`)}}, DefaultDefinition: "main", Runtime: contract.RuntimeSpec{Kind: "native", Artifacts: []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: "plugin"}}}}
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(source, "plugin.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(source, "plugin"), []byte("fake runtime artifact"), 0600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "plugin.zip")
		pkg, err := packagehost.Pack(t.Context(), source, path)
		if err != nil {
			t.Fatal(err)
		}
		archives = append(archives, path)
		trust.DevDigests[pkg.Ref.Digest] = true
	}
	h, err := Open(Options{FrameBytes: contract.MaxFrameBytes, Directory: t.TempDir(), Trust: trust, Runtime: fixtureRuntime{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	p1, err := h.Install(t.Context(), archives[0])
	if err != nil {
		t.Fatal(err)
	}
	p2, err := h.Install(t.Context(), archives[1])
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Select(t.Context(), p1.Ref); err != nil {
		t.Fatal(err)
	}
	return h, p1.Ref, p2.Ref
}

func TestBuiltinUpdatedConfigSurvivesParentReplace(t *testing.T) {
	h, p1, p2 := lifecycleHost(t)
	parent, err := h.Enable(t.Context(), contract.Entry{ID: "parent", Package: p1.ID, Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err = parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	configurations := make(chan string, 4)
	child, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "child", Parent: "parent", Config: json.RawMessage(`{"version":1}`)}, contract.Definition{Start: func(_ context.Context, s contract.Scope) error { configurations <- string(s.Config()); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = child.Update(t.Context(), json.RawMessage(`{"version":2}`)); err != nil {
		t.Fatal(err)
	}
	if err = h.Replace(t.Context(), p2); err != nil {
		t.Fatal(err)
	}
	first, second, third := <-configurations, <-configurations, <-configurations
	t.Logf("Builtin child configurations initial=%s updated=%s after replacing parent=%s", first, second, third)
	if third != second {
		t.Fatal("parent replacement lost accepted builtin configuration")
	}
}

type selectionCommitFailureContext struct {
	context.Context
	host *Host
	once sync.Once
	err  error
}

func (c *selectionCommitFailureContext) Err() error {
	if c.host.engine.Missing("a") && c.host.engine.Missing("b") {
		c.once.Do(func() {
			c.err = os.Mkdir(filepath.Join(c.host.options.Directory, "catalog.next"), 0700)
		})
	}
	return errors.Join(c.Context.Err(), c.err)
}

func TestReplaceCommitFailurePreservesEnabledIntentAfterReopen(t *testing.T) {
	h, p1, p2 := lifecycleHost(t)
	for _, id := range []string{"a", "b"} {
		i, err := h.Enable(t.Context(), contract.Entry{ID: id, Package: p1.ID})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	ctx := &selectionCommitFailureContext{Context: t.Context(), host: h}
	err := h.Replace(ctx, p2)
	var committed *packagehost.CommitError
	if err == nil || errors.As(err, &committed) || ctx.err != nil {
		t.Fatalf("expected selection commit failure: %v (fixture: %v)", err, ctx.err)
	}
	if err := os.RemoveAll(filepath.Join(h.options.Directory, "catalog.next")); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(h.options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	report, err := reopened.Restore(t.Context())
	if err != nil || len(report.Entries) != 2 {
		t.Fatal(report, err)
	}
	for _, entry := range report.Entries {
		if entry.Action == "disabled" || entry.Status.State != contract.Active || !entry.Status.Enabled || entry.Status.Identity.Digest != p1.Digest {
			t.Fatalf("replacement lost enabled intent after reopening: %+v", entry)
		}
	}
}

type capturedLaunch struct {
	launch  contract.Launch
	session *fixtureSession
}

type captureRuntime struct{ launches chan capturedLaunch }

func (r captureRuntime) Start(_ context.Context, launch contract.Launch) (contract.Session, error) {
	session := &fixtureSession{done: make(chan struct{})}
	r.launches <- capturedLaunch{launch: launch, session: session}
	return session, nil
}

func TestExternalAuthorChildChecksDigestAndRetiresWithParent(t *testing.T) {
	h, p1, p2 := lifecycleHost(t)
	launches := make(chan capturedLaunch, 4)
	h.options.Runtime = captureRuntime{launches: launches}
	parent, err := h.Enable(t.Context(), contract.Entry{ID: "parent", Package: p1.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	parentLaunch := <-launches
	childRequest := json.RawMessage(`{"action":"create","child":{"id":"optional","definition":"main"}}`)
	if _, err := parentLaunch.launch.Handle(t.Context(), "child", childRequest); err != nil {
		t.Fatal(err)
	}
	childLaunch := <-launches
	if childLaunch.launch.Identity.Digest != p1.Digest || childLaunch.launch.Identity.Entry != "parent.optional" {
		t.Fatal("child did not inherit parent package identity", childLaunch.launch.Identity)
	}
	if _, err := parentLaunch.launch.Handle(t.Context(), "child", json.RawMessage(`{"action":"wait","id":"optional"}`)); err != nil {
		t.Fatal("author child was not registered for controls", err)
	}
	entries, err := h.Store().Entries()
	if err != nil || len(entries) != 1 || entries[0].ID != "parent" {
		t.Fatal("activation child leaked into durable entries", entries, err)
	}
	// Change only the store selection to model a stale parent's child request.
	if err := h.Store().Select(t.Context(), p2, packagehost.ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err = parentLaunch.launch.Handle(t.Context(), "child", json.RawMessage(`{"action":"create","child":{"id":"wrong-digest","definition":"main"}}`))
	var coded *contract.Error
	if !errors.As(err, &coded) || coded.Code != contract.Conflict {
		t.Fatalf("child used a different selected digest: %v", err)
	}
	if err := h.Disable(t.Context(), "parent"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-childLaunch.session.Done():
	default:
		t.Fatal("parent retirement did not close child session")
	}
	if !h.engine.Missing("parent.optional") {
		t.Fatal("parent retirement retained child registration")
	}
	if _, err := parentLaunch.launch.Handle(t.Context(), "child", childRequest); err == nil {
		t.Fatal("retired parent retained child creation authority")
	}
}

func TestClosedHostRejectsManagementAdmission(t *testing.T) {
	h, pkg, _ := lifecycleHost(t)
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	definition := contract.Definition{ID: "main", Start: func(context.Context, contract.Scope) error { return nil }}
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{name: "Enable", run: func() error {
			_, err := h.Enable(t.Context(), contract.Entry{ID: "external", Package: pkg.ID})
			return err
		}},
		{name: "AddBuiltin", run: func() error {
			_, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "builtin"}, definition)
			return err
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			var coded *contract.Error
			if err := operation.run(); !errors.As(err, &coded) || coded.Code != contract.NotReady {
				t.Fatalf("closed host admitted management or returned wrong code: %v", err)
			}
		})
	}
}

func TestExternalChildAcceptsBuiltinParent(t *testing.T) {
	h, p1, _ := lifecycleHost(t)
	parent, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent"}, contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err = parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err = h.Enable(t.Context(), contract.Entry{ID: "child", Parent: "parent", Package: p1.ID, Config: json.RawMessage(`{}`)})
	t.Logf("External child of active builtin parent: %v", err)
	if err != nil {
		t.Fatal(err)
	}
}

type entryAdmissionContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (c *entryAdmissionContext) Err() error {
	c.once.Do(func() { close(c.entered); <-c.release })
	return c.Context.Err()
}

func TestStaticEnableReservesIDAgainstAuthorChild(t *testing.T) {
	h, pkg, _ := lifecycleHost(t)
	scopes := make(chan contract.Scope, 1)
	parent, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent"}, contract.Definition{Children: []contract.Definition{{ID: "optional", Start: func(context.Context, contract.Scope) error { return nil }}}, Start: func(_ context.Context, s contract.Scope) error { scopes <- s; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	scope := <-scopes
	ctx := &entryAdmissionContext{Context: t.Context(), entered: make(chan struct{}), release: make(chan struct{})}
	type result struct {
		instance contract.Instance
		err      error
	}
	done := make(chan result, 1)
	// The first admission check pauses the real Enable path before persistence.
	go func() {
		instance, err := h.enable(ctx, contract.Entry{ID: "parent.optional", Parent: "parent", Package: pkg.ID, Config: json.RawMessage(`{}`)})
		done <- result{instance, err}
	}()
	<-ctx.entered
	_, childErr := contract.CreateChild(t.Context(), scope, contract.Child{ID: "optional", Definition: "optional"})
	close(ctx.release)
	got := <-done
	var conflict *contract.Error
	if !errors.As(childErr, &conflict) || conflict.Code != contract.Conflict {
		t.Fatalf("child took a statically reserved entry ID: %v", childErr)
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	if err := got.instance.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := h.Store().Entries()
	if err != nil || len(entries) != 1 || entries[0].ID != "parent.optional" || !entries[0].Enabled {
		t.Fatal(entries, err)
	}
}

func TestDisableParentSkipsDisabledUninstalledChild(t *testing.T) {
	h, pkg, _ := lifecycleHost(t)
	parent, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent"}, contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	child, err := h.Enable(t.Context(), contract.Entry{ID: "child", Parent: "parent", Package: pkg.ID, Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := h.Disable(t.Context(), "child"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Uninstall(t.Context(), pkg); err != nil {
		t.Fatal(err)
	}
	if err := h.Disable(t.Context(), "parent"); err != nil {
		t.Fatal(err)
	}
}

type typedPayload struct {
	Value string `json:"value"`
}

func BenchmarkPublicTypedCall(b *testing.B) {
	for _, size := range []int{32, 1024} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			h, err := Open(Options{FrameBytes: contract.MaxFrameBytes, Directory: b.TempDir()})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				if err := h.Close(context.Background()); err != nil {
					b.Error(err)
				}
			})
			service := Service[typedPayload, typedPayload]{Contract: contract.Service{Name: "echo", Major: 1}}
			var scope contract.Scope
			provider, err := h.AddBuiltin(b.Context(), contract.Entry{ID: "provider"}, contract.Definition{Provides: []contract.Service{service.Contract}, Start: func(_ context.Context, s contract.Scope) error {
				return Provide(s, service, func(_ context.Context, in typedPayload) (typedPayload, error) { return in, nil })
			}})
			if err != nil {
				b.Fatal(err)
			}
			if err = provider.Wait(b.Context()); err != nil {
				b.Fatal(err)
			}
			consumer, err := h.AddBuiltin(b.Context(), contract.Entry{ID: "consumer"}, contract.Definition{Requires: []contract.Requirement{{Name: "echo", Major: 1}}, Start: func(_ context.Context, s contract.Scope) error { scope = s; return nil }})
			if err != nil {
				b.Fatal(err)
			}
			if err = consumer.Wait(b.Context()); err != nil {
				b.Fatal(err)
			}
			input := typedPayload{Value: strings.Repeat("x", size)}
			b.ReportAllocs()
			for b.Loop() {
				out, err := Call(b.Context(), scope, service, input)
				if err != nil || out.Value != input.Value {
					b.Fatal(out, err)
				}
			}
		})
	}
}
