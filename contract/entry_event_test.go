package contract_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func TestEntryGraphRejectsSelfAndDeepCycles(t *testing.T) {
	deep := make([]contract.Entry, 128)
	for i := range deep {
		deep[i].ID = fmt.Sprintf("entry-%d", i)
		if i+1 < len(deep) {
			deep[i].Parent = fmt.Sprintf("entry-%d", i+1)
		}
	}
	acyclic := append([]contract.Entry(nil), deep...)
	deep[len(deep)-1].Parent = "entry-64"
	for _, test := range []struct {
		name    string
		entries []contract.Entry
		code    contract.Code
	}{
		{name: "self cycle", entries: []contract.Entry{{ID: "self", Parent: "self"}}, code: contract.Reentrant},
		{name: "deep cycle behind an acyclic prefix", entries: deep, code: contract.Reentrant},
		{name: "missing parent", entries: []contract.Entry{{ID: "child", Parent: "missing"}}, code: contract.Invalid},
		{name: "deep valid chain", entries: acyclic},
		{name: "root and siblings", entries: []contract.Entry{{ID: "root"}, {ID: "left", Parent: "root"}, {ID: "right", Parent: "root"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := contract.CheckEntryGraph(test.entries)
			if test.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var coded *contract.Error
			if !errors.As(err, &coded) || coded.Code != test.code {
				t.Fatalf("graph error = %v, want %s", err, test.code)
			}
		})
	}
}

func TestDecodeRejectsInvalidUTF8RatherThanReplacingIdentity(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "invalid entry identity", data: []byte("{\"id\":\"entry-\xff\"}")},
		{name: "invalid config value", data: []byte("{\"id\":\"valid\",\"config\":{\"value\":\"\xff\"}}")},
	} {
		t.Run(test.name, func(t *testing.T) {
			var entry contract.Entry
			err := contract.Decode(test.data, &entry)
			var coded *contract.Error
			if !errors.As(err, &coded) || coded.Code != contract.Invalid {
				t.Fatalf("invalid UTF-8 decode = %#v, %v", entry, err)
			}
			if entry.ID != "" || entry.Config != nil {
				t.Fatalf("invalid input partially populated entry: %+v", entry)
			}
		})
	}
	var valid contract.Entry
	if err := contract.Decode([]byte(`{"id":"有效条目","config":{"value":"有效"}}`), &valid); err != nil || valid.ID != "有效条目" {
		t.Fatalf("valid UTF-8 decode = %+v, %v", valid, err)
	}
}

func TestTypedEventStrictDecodeAndFirstHandledZero(t *testing.T) {
	for _, test := range []struct {
		name string
		data json.RawMessage
	}{
		{name: "unknown field", data: json.RawMessage(`{"count":0,"extra":true}`)},
		{name: "wrong field type", data: json.RawMessage(`{"count":"0"}`)},
		{name: "wrong object type", data: json.RawMessage(`[]`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				type message struct {
					Count uint32 `json:"count"`
				}
				event := contract.EventKey[message]{Name: "typed.zero", Mode: contract.First}
				engine := core.New()
				defer func() {
					if err := engine.Close(t.Context()); err != nil {
						t.Error(err)
					}
				}()
				var firstCalls, laterCalls atomic.Int32
				add := func(id string, handler func(context.Context, message) (contract.EventResult, error)) {
					t.Helper()
					instance, err := engine.Add(contract.Entry{ID: id, Enabled: true}, contract.Definition{
						Start: func(_ context.Context, scope contract.Scope) error {
							return contract.Subscribe(scope, event, handler)
						},
					}, contract.Identity{})
					if err != nil {
						t.Fatal(err)
					}
					if err := instance.Wait(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				add("first", func(_ context.Context, input message) (contract.EventResult, error) {
					firstCalls.Add(1)
					value, err := json.Marshal(input.Count)
					return contract.EventResult{Handled: true, Value: value}, err
				})
				add("later", func(context.Context, message) (contract.EventResult, error) {
					laterCalls.Add(1)
					return contract.EventResult{}, nil
				})
				if _, err := engine.Emit(t.Context(), event.Name, test.data, event.Mode); err == nil {
					t.Fatalf("typed event accepted %s", test.data)
				}
				if firstCalls.Load() != 0 || laterCalls.Load() != 0 {
					t.Fatal("typed event invoked consumer before successful decode")
				}
				results, err := contract.Publish(t.Context(), typedEngineEmitter{engine: engine}, event, message{})
				if err != nil {
					t.Fatal(err)
				}
				if len(results) != 1 || !results[0].Handled || string(results[0].Value) != `0` {
					t.Fatalf("First results for valid zero value = %+v", results)
				}
				if firstCalls.Load() != 1 || laterCalls.Load() != 0 {
					t.Fatalf("First consumer counts = %d, %d", firstCalls.Load(), laterCalls.Load())
				}
				// The second consumer is real and subscribed: non-short-circuiting
				// dispatch reaches it, proving First stopped because of Handled, not 0.
				if _, err := engine.Emit(t.Context(), event.Name, json.RawMessage(`{"count":0}`), contract.Sequential); err != nil {
					t.Fatal(err)
				}
				if firstCalls.Load() != 2 || laterCalls.Load() != 1 {
					t.Fatalf("sequential consumer counts = %d, %d", firstCalls.Load(), laterCalls.Load())
				}
			})
		})
	}
}

type typedEngineEmitter struct {
	engine *core.Engine
}

func (e typedEngineEmitter) Emit(ctx context.Context, name string, data json.RawMessage, mode contract.Dispatch) ([]contract.EventResult, error) {
	return e.engine.Emit(ctx, name, data, mode)
}
