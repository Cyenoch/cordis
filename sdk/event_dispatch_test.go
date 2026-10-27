package sdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
	"net"
	"sync/atomic"
	"testing"
)

func TestParallelEventContinuesAfterLocalError(t *testing.T) {
	left, right := net.Pipe()
	var later atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- sdk.ServeTransport(t.Context(), contract.Definition{ID: "audit", Start: func(_ context.Context, s contract.Scope) error {
			if err := s.On("audit", func(context.Context, json.RawMessage) (contract.EventResult, error) {
				return contract.EventResult{}, contract.Fail(contract.Invalid, "first failed")
			}); err != nil {
				return err
			}
			return s.On("audit", func(context.Context, json.RawMessage) (contract.EventResult, error) {
				later.Add(1)
				return contract.EventResult{}, nil
			})
		}}, right)
	}()
	host, err := wire.New(wire.Options{Transport: left, Identity: contract.Identity{Activation: "audit"}, Handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, contract.Fail(contract.Invalid, "unexpected")
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		host.Close()
		host.Wait()
		if err := <-done; err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	}()
	raw, _ := json.Marshal(contract.Initialize{Identity: contract.Identity{Activation: "audit"}, Protocol: contract.Protocol{Major: 1}, Definition: "audit", Config: json.RawMessage(`{}`)})
	if _, err := host.Call(t.Context(), "initialize", raw); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(contract.Event{Name: "audit", Input: json.RawMessage(`{}`), Mode: contract.Parallel})
	_, err = host.Call(t.Context(), "event", raw)
	t.Logf("event returned %v; second listener calls=%d", err, later.Load())
	if later.Load() != 1 {
		t.Errorf("Parallel skipped a registered listener after an earlier error")
	}
	if _, err := host.Call(t.Context(), "shutdown", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}
