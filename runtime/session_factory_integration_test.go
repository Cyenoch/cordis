//go:build integration

package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/runtime/native"
)

type customBudgetSession struct {
	done chan struct{}
	once sync.Once
}

func (s *customBudgetSession) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("generic call reached custom ABI")
}
func (s *customBudgetSession) Done() <-chan struct{} { return s.done }
func (s *customBudgetSession) Err() error            { return nil }
func (s *customBudgetSession) Close() error          { s.once.Do(func() { close(s.done) }); return nil }
func (s *customBudgetSession) Initialize(context.Context, contract.Initialize) (contract.Registration, error) {
	return contract.Registration{}, nil
}
func (s *customBudgetSession) Publish(context.Context) error { return nil }
func (s *customBudgetSession) Retire(context.Context) error  { return nil }
func (s *customBudgetSession) Invoke(context.Context, contract.Invocation) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (s *customBudgetSession) Deliver(context.Context, contract.Event) ([]contract.EventResult, error) {
	return nil, nil
}
func (s *customBudgetSession) Shutdown(context.Context) error { return nil }

func TestExplicitSessionFactorySelection(t *testing.T) {
	root := t.TempDir()
	artifact := filepath.Join(root, "fixture")
	if runtime.GOOS == "windows" {
		artifact += ".exe"
	}
	build(t, artifact, "./runtime/testdata/faultplugin", false)
	for _, protocol := range []contract.SessionProtocol{contract.CustomSession, contract.GenericSession} {
		t.Run(string(protocol), func(t *testing.T) {
			budget := 0
			if protocol == contract.GenericSession {
				budget = contract.MinFrameBytes
			}
			var opened *customBudgetSession
			factory := &contract.SessionFactory{Protocol: protocol, Open: func(transport io.ReadWriteCloser, _ contract.Identity, _ contract.SessionHandler, got int) (contract.Session, error) {
				if got != budget {
					t.Errorf("factory budget=%d, want %d", got, budget)
				}
				opened = &customBudgetSession{done: make(chan struct{})}
				return opened, nil
			}}
			session, err := native.New().Start(t.Context(), contract.Launch{FrameBytes: budget, SessionFactory: factory, Identity: contract.Identity{Activation: "custom"}, Directory: root, Artifact: artifact, Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, nil }})
			if protocol == contract.GenericSession {
				if err == nil {
					session.Close()
					t.Fatal("generic factory accepted a custom ABI")
				}
				select {
				case <-opened.done:
				default:
					t.Fatal("rejected factory session not closed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// No generic initialize or budget field is sent before the custom opener.
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type pipeClosingSession struct {
	*customBudgetSession
	reader io.Reader
}

func (s *pipeClosingSession) Close() error {
	_, err := s.reader.Read(make([]byte, 1))
	return errors.Join(err, s.customBudgetSession.Close())
}

func TestRejectedFactoryCloseDoesNotBlockProcessKill(t *testing.T) {
	root := t.TempDir()
	artifact := filepath.Join(root, "fixture")
	if runtime.GOOS == "windows" {
		artifact += ".exe"
	}
	build(t, artifact, "./runtime/testdata/faultplugin", false)
	closed := make(chan struct{})
	factory := &contract.SessionFactory{Protocol: contract.GenericSession, Open: func(transport io.ReadWriteCloser, _ contract.Identity, _ contract.SessionHandler, _ int) (contract.Session, error) {
		return &pipeClosingSession{customBudgetSession: &customBudgetSession{done: closed}, reader: transport}, nil
	}}
	done := make(chan error, 1)
	go func() {
		_, err := native.New().Start(t.Context(), contract.Launch{FrameBytes: contract.MinFrameBytes, SessionFactory: factory, Identity: contract.Identity{Activation: "a"}, Directory: root, Artifact: artifact, Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, nil }})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("mismatched factory accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close prevented kill/reap on factory rejection")
	}
	select {
	case <-closed:
	default:
		t.Fatal("rejected endpoint was not closed")
	}
}
