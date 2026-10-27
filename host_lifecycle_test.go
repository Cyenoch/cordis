package cordis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
		return json.RawMessage(`{"frameBytes":1048576,"minor":0,"features":[],"services":[],"subscriptions":[],"contributions":[]}`), nil
	}
	return json.RawMessage(`{}`), nil
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
		m := contract.Manifest{Schema: contract.PackageSchema, ID: "test.package", Version: version, Publisher: "test.publisher", Protocol: contract.Protocol{Major: 1}, Definitions: []contract.DefinitionSpec{{ID: "main", ConfigSchema: json.RawMessage(`{"type":"object","properties":{}}`)}}, DefaultDefinition: "main", Runtime: contract.RuntimeSpec{Kind: "wasm", Entry: "plugin.wasm"}}
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(source, "plugin.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(source, "plugin.wasm"), []byte("fake runtime artifact"), 0600); err != nil {
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
	h, err := Open(Options{FrameBytes: contract.MaxFrameBytes, Directory: t.TempDir(), Trust: trust, Runtimes: map[string]contract.Runtime{"wasm": fixtureRuntime{}}})
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
	if err = h.Replace(t.Context(), p2, packagehost.ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	first, second, third := <-configurations, <-configurations, <-configurations
	t.Logf("Builtin child configurations initial=%s updated=%s after replacing parent=%s", first, second, third)
	if third != second {
		t.Fatal("parent replacement lost accepted builtin configuration")
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
	if err := h.RegisterDefinition(t.Context(), "builtin", contract.Definition{ID: "optional", Start: func(context.Context, contract.Scope) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	scopes := make(chan contract.Scope, 1)
	parent, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "parent"}, contract.Definition{Start: func(_ context.Context, s contract.Scope) error { scopes <- s; return nil }})
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
	if _, err := h.Uninstall(t.Context(), pkg, false); err != nil {
		t.Fatal(err)
	}
	if err := h.Disable(t.Context(), "parent"); err != nil {
		t.Fatal(err)
	}
}

func TestCloseCancellationWhileDisableIsBlocked(t *testing.T) {
	h, err := Open(Options{FrameBytes: contract.MaxFrameBytes, Directory: t.TempDir()})
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
	if err = i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	disabled := make(chan error, 1)
	go func() { disabled <- h.Disable(context.Background(), "slow") }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	closed := make(chan error, 1)
	go func() { closed <- h.Close(ctx) }()
	select {
	case err := <-closed:
		if err == nil {
			t.Fatal("canceled close succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Close ignored cancellation")
	}
	close(release)
	<-disabled
	if err = h.Close(context.Background()); err != nil {
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

type lifecycleCustomRuntime struct{ session *lifecycleCustomSession }

func (r lifecycleCustomRuntime) Start(_ context.Context, launch contract.Launch) (contract.Session, error) {
	if !launch.SessionFactory.IsCustom() {
		return nil, errors.New("custom declaration lost")
	}
	return r.session, nil
}

type lifecycleCustomSession struct {
	done, publishing, ack, retired chan struct{}
	once                           sync.Once
	service                        contract.Service
	invokes                        atomic.Int32
}

func (s *lifecycleCustomSession) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("generic envelope reached custom ABI")
}
func (s *lifecycleCustomSession) Done() <-chan struct{} { return s.done }
func (s *lifecycleCustomSession) Err() error            { return nil }
func (s *lifecycleCustomSession) Close() error          { s.once.Do(func() { close(s.done) }); return nil }
func (s *lifecycleCustomSession) Initialize(context.Context, contract.Initialize) (contract.Registration, error) {
	return contract.Registration{Services: []contract.Service{s.service}}, nil
}
func (s *lifecycleCustomSession) Publish(context.Context) error {
	close(s.publishing)
	<-s.ack
	return nil
}
func (s *lifecycleCustomSession) Retire(context.Context) error { close(s.retired); return nil }
func (s *lifecycleCustomSession) Invoke(_ context.Context, in contract.Invocation) (json.RawMessage, error) {
	if !in.Cleanup {
		return nil, errors.New("cleanup classification lost in custom mapping")
	}
	s.invokes.Add(1)
	return json.RawMessage(`{}`), nil
}
func (s *lifecycleCustomSession) Deliver(context.Context, contract.Event) ([]contract.EventResult, error) {
	return nil, nil
}
func (s *lifecycleCustomSession) Shutdown(context.Context) error {
	select {
	case <-s.retired:
		return nil
	default:
		return errors.New("shutdown ran without retirement")
	}
}
func TestHostCustomSessionPublicationAndRetirement(t *testing.T) {
	h, pkg, _ := lifecycleHost(t)
	// Add a service through a fresh validated package; the custom ABI has no generic budget handshake.
	original, err := h.store.Get(pkg)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	m := original.Manifest
	m.ID = "custom.lifecycle"
	service := contract.Service{Name: "custom.lifecycle", Major: 1}
	m.Definitions[0].Provides = []contract.Service{service}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.wasm"), []byte("custom artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "plugin.zip")
	packed, err := packagehost.Pack(t.Context(), dir, archive)
	if err != nil {
		t.Fatal(err)
	}
	// Trust remains explicit and package-bound in the same host-owned policy.
	trust := h.options.Trust
	trust.DevDigests[packed.Ref.Digest] = true
	trust.Publishers["test.publisher"] = append(trust.Publishers["test.publisher"], m.ID)
	if err := h.store.UpdateTrust(trust); err != nil {
		t.Fatal(err)
	}
	installed, err := h.Install(t.Context(), archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Select(t.Context(), installed.Ref); err != nil {
		t.Fatal(err)
	}
	s := &lifecycleCustomSession{done: make(chan struct{}), publishing: make(chan struct{}), ack: make(chan struct{}), retired: make(chan struct{}), service: service}
	h.options.Runtimes["wasm"] = lifecycleCustomRuntime{s}
	h.options.FrameBytes = 0
	h.options.SessionFactory = func(contract.Manifest) *contract.SessionFactory {
		return &contract.SessionFactory{Protocol: contract.CustomSession, Open: func(io.ReadWriteCloser, contract.Identity, contract.SessionHandler, int) (contract.Session, error) {
			return s, nil
		}}
	}
	i, err := h.Enable(t.Context(), contract.Entry{ID: "custom", Package: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	<-s.publishing
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := i.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if status := i.Status(); status.State != contract.Starting || status.Applied != 0 {
		t.Fatalf("custom publication acknowledged early: %+v", status)
	}
	unrelated, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "unrelated"}, contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := unrelated.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	close(s.ack)
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	req := contract.Requirement{Name: service.Name, Major: 1}
	consumer, err := h.AddBuiltin(t.Context(), contract.Entry{ID: "custom-consumer"}, contract.Definition{Requires: []contract.Requirement{req}, Start: func(_ context.Context, scope contract.Scope) error {
		return scope.Defer(func(ctx context.Context) error {
			select {
			case <-s.retired:
			default:
				return errors.New("consumer cleanup preceded custom retire")
			}
			_, err := scope.Call(ctx, req, json.RawMessage(`{}`))
			return err
		})
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := h.Disable(t.Context(), "custom"); err != nil {
		t.Fatal(err)
	}
	if s.invokes.Load() != 1 {
		t.Fatalf("custom cleanup calls=%d", s.invokes.Load())
	}
}
