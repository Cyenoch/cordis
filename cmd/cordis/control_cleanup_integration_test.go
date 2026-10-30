//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/examples/pluginmeta"
)

type controlBlockedRuntime struct{ entered, release chan struct{} }
type controlBlockedSession struct {
	controlBlockedRuntime
	done chan struct{}
	once sync.Once
}

func (r controlBlockedRuntime) Start(context.Context, contract.Launch) (contract.Session, error) {
	return &controlBlockedSession{controlBlockedRuntime: r, done: make(chan struct{})}, nil
}
func (s *controlBlockedSession) Call(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
	if method == "initialize" {
		for _, definition := range pluginmeta.Chain().Definitions {
			if definition.ID == "provider" {
				return json.Marshal(contract.Registration{FrameBytes: contract.MaxFrameBytes, Minor: 0,
					Features: []string{"subscriptions"}, Services: definition.Provides})
			}
		}
	}
	if method == "shutdown" {
		close(s.entered)
		<-s.release
	}
	return json.RawMessage(`{}`), nil
}
func (s *controlBlockedSession) CallWithReceipt(ctx context.Context, method string, input json.RawMessage, receipt func(bool)) (json.RawMessage, error) {
	if receipt != nil {
		receipt(true)
	}
	return s.Call(ctx, method, input)
}
func (s *controlBlockedSession) Done() <-chan struct{} { return s.done }
func (s *controlBlockedSession) Err() error            { return nil }
func (s *controlBlockedSession) Close() error          { s.once.Do(func() { close(s.done) }); return nil }

func TestControlSlowCleanupRemainsQueryableAndCancellationDoesNotUndoStop(t *testing.T) {
	directory, trustFile, ref := controlStore(t)
	trust, err := loadTrust(trustFile)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	h, err := cordis.Open(cordis.Options{Directory: directory, Trust: trust, Runtime: controlBlockedRuntime{entered, release}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unblock(); _ = closeHost(h, 5*time.Second) })
	i, err := h.Enable(t.Context(), contract.Entry{ID: "slow", Package: ref.ID, Definition: "provider"})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	s, err := startControl(t.Context(), h, filepath.Join(t.TempDir(), "connection.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.close() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stopped := make(chan error, 1)
	go func() {
		response, err := requestControl(ctx, s.connection, "stop", "slow", time.Minute)
		if err == nil && response.Phase != "unknown" {
			err = errors.New("canceled control claimed definite completion or rejection")
		}
		stopped <- err
	}()
	<-entered
	status, err := requestControl(t.Context(), s.connection, "status", "slow", time.Second)
	if err != nil || controlState(status, "slow") != contract.Quiescing {
		t.Fatalf("slow cleanup blocked status: %+v %v", status, err)
	}
	conflict, err := requestControl(t.Context(), s.connection, "stop", "slow", time.Second)
	if err != nil || conflict.Code != contract.Conflict || conflict.Phase != "unknown" {
		t.Fatalf("concurrent control bypassed tree reservation: %+v %v", conflict, err)
	}
	cancel()
	if err := <-stopped; err != nil && !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("disconnection did not report uncertainty: %v", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- s.close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("control close retained a canceled request")
	}
	ctx, finish := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer finish()
	if err := h.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("incomplete cleanup was hidden: %v", err)
	}
	if i.Status().State != contract.Quiescing {
		t.Fatal("client cancellation undid accepted Stop")
	}
	unblock()
	if err := closeHost(h, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}
