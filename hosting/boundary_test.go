package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

// scripted is a runtime whose guest answers initialize with a fixed
// registration, for what a well-behaved SDK never does.
type scripted struct{ registration string }

type scriptedSession struct {
	registration string
	done         chan struct{}
	once         sync.Once
}

func (r scripted) Start(context.Context, contract.Launch) (contract.Session, error) {
	return &scriptedSession{registration: r.registration, done: make(chan struct{})}, nil
}

func (s *scriptedSession) Call(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
	if method == "initialize" {
		return json.RawMessage(s.registration), nil
	}
	return json.RawMessage(`{}`), nil
}

func (s *scriptedSession) CallWithReceipt(ctx context.Context, method string, input json.RawMessage, receipt func(bool)) (json.RawMessage, error) {
	if receipt != nil {
		receipt(true)
	}
	return s.Call(ctx, method, input)
}

func (s *scriptedSession) Stream(context.Context, string, json.RawMessage) iter.Seq2[json.RawMessage, error] {
	return contract.FailedStream(contract.Fail(contract.Invalid, "no streams"))
}
func (s *scriptedSession) Done() <-chan struct{} { return s.done }
func (s *scriptedSession) Err() error            { return nil }
func (s *scriptedSession) Close() error          { s.once.Do(func() { close(s.done) }); return nil }

func TestRegistrationThatDoesNotMatchPublishesNothing(t *testing.T) {
	for name, registration := range map[string]string{
		"another frame budget":  `{"frameBytes":4096,"minor":0,"features":["cancel","parent","events","children","subscriptions","streams","chains"],"services":[{"name":"test.echo","major":1,"minor":1}]}`,
		"an undeclared service": `{"frameBytes":1048576,"minor":0,"features":[],"services":[{"name":"test.echo","major":1,"minor":1},{"name":"test.extra","major":1,"minor":0}]}`,
		"a missing service":     `{"frameBytes":1048576,"minor":0,"features":[],"services":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.pack(echoManifest("test.provider", true), sdk.Define(f.echoPlugin("main", "")))
			f.open(func(o *Options) { o.Runtime = scripted{registration: registration} })
			instance := f.enable(contract.Entry{ID: "provider", Package: "test.provider"})
			if err := instance.Wait(f.ctx()); err == nil || f.state("provider") != contract.Failed {
				t.Fatalf("wait = %v, state %s", err, f.state("provider"))
			}
			if _, err := echoKey.Get(f.host.Scope()); err == nil {
				t.Fatal("an endpoint was published for a registration the host refused")
			}
		})
	}
}

func TestDoneFollowsACompletedClose(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	f.pack(plainManifest("test.plain"), sdk.Define(f.plain("plain")))
	f.open()
	must(t, f.enable(contract.Entry{ID: "plain", Package: "test.plain"}).Wait(f.ctx()))
	blocked := cordis.Plugin[struct{}]{Name: "blocked", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		s.Defer(func(context.Context) error {
			<-release
			return nil
		})
		return nil
	}}.Load(f.host.Scope(), struct{}{})
	must(t, blocked.Wait(f.ctx()))

	// A close that is canceled before it starts changes nothing.
	canceled, cancel := context.WithCancel(f.ctx())
	cancel()
	if err := f.host.Close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("close with a canceled context = %v", err)
	}
	if f.state("plain") != contract.Active {
		t.Fatal("a close that was never submitted stopped an entry")
	}
	// A close whose wait is given up is still under way; Done stays open
	// and the store is not released until the cleanup has finished.
	short, stop := context.WithTimeout(f.ctx(), 50*time.Millisecond)
	defer stop()
	if err := f.host.Close(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close that cannot finish in time = %v", err)
	}
	select {
	case <-f.host.Done():
		t.Fatal("Done closed while a cleanup was still running")
	default:
	}
	if _, err := f.host.Enable(f.ctx(), contract.Entry{ID: "late", Package: "test.plain"}); err == nil {
		t.Fatal("a closing host admitted an entry")
	}
	close(release)
	must(t, f.host.Close(f.ctx()))
	select {
	case <-f.host.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done did not close after the host had closed")
	}
	if f.log.count("stop plain") != 1 {
		t.Fatalf("events = %q", f.log.get())
	}
}

func TestDynamicChildIsControlledThroughItsHandleOnly(t *testing.T) {
	f := newFixture(t)
	parent := cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		_, err := sdk.CreateChild(ctx, s, contract.Child{ID: "feature", Definition: "feature"})
		return err
	}}
	manifest := contract.Manifest{ID: "test.parent", Definitions: []contract.DefinitionSpec{{ID: "main"}, {ID: "feature"}}}
	feature := f.plain("feature")
	feature.Name = "feature"
	f.pack(manifest, sdk.Define(parent), sdk.Define(feature))
	f.open()
	must(t, f.enable(contract.Entry{ID: "parent", Package: "test.parent"}).Wait(f.ctx()))
	eventually(t, "the child", func() bool { return f.state("parent.feature") == contract.Active })
	// The management operations of persistent entries refuse a child.
	for name, operation := range map[string]func() error{
		"disable": func() error { return f.host.Disable(f.ctx(), "parent.feature") },
		"remove":  func() error { return f.host.Remove(f.ctx(), "parent.feature") },
		"resume":  func() error { return f.host.Resume(f.ctx(), "parent.feature") },
		"retry":   func() error { return f.host.Retry(f.ctx(), "parent.feature") },
	} {
		if err := operation(); !errors.Is(err, contract.Fail(contract.Invalid, "")) {
			t.Fatalf("%s of a dynamic child = %v", name, err)
		}
	}
	if _, err := f.host.Enable(f.ctx(), contract.Entry{ID: "parent.feature", Package: "test.parent", Definition: "feature"}); !errors.Is(err, contract.Fail(contract.Conflict, "")) {
		t.Fatalf("enabling an entry under the identifier of a live child = %v", err)
	}
	// Its handle updates and stops it.
	child, err := f.host.Instance("parent.feature")
	must(t, err)
	if result, err := child.Update(f.ctx(), []byte(`{}`)); err != nil || !result.Applied {
		t.Fatalf("update = %+v, %v", result, err)
	}
	must(t, child.Stop(f.ctx()))
	if f.log.count("start feature") != 2 || f.log.count("stop feature") != 2 {
		t.Fatalf("events = %q", f.log.get())
	}
	if _, err := f.host.Status("parent.feature"); err == nil {
		t.Fatal("a stopped child is still registered")
	}
	if entries, _ := f.host.Store().Entries(); len(entries) != 1 {
		t.Fatalf("the child was saved: %+v", entries)
	}
}

func TestOnceListenerWhoseDeliveryIsNeverSent(t *testing.T) {
	f := newFixture(t)
	calls := make(chan string, 4)
	f.pack(plainManifest("test.once"), sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		_, err := noteEvent.On(ctx, s, func(_ context.Context, n note) error {
			calls <- n.Text
			return nil
		}, cordis.Once)
		return err
	}}))
	f.open()
	must(t, f.enable(contract.Entry{ID: "once", Package: "test.once"}).Wait(f.ctx()))
	// The emission is canceled before the delivery can go out: the kernel
	// has consumed the registration, and the guest is told to let go of it.
	canceled, cancel := context.WithCancel(f.ctx())
	cancel()
	noteEvent.Emit(canceled, f.host.Scope(), note{Text: "lost"})
	must(t, noteEvent.Emit(f.ctx(), f.host.Scope(), note{Text: "after"}))
	select {
	case text := <-calls:
		t.Fatalf("a consumed once listener ran with %q", text)
	case <-time.After(100 * time.Millisecond):
	}
	if f.state("once") != contract.Active {
		t.Fatalf("the entry is %s", f.state("once"))
	}
	must(t, f.host.Disable(f.ctx(), "once"))
}
