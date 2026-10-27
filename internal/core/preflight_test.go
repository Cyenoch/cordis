package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func TestConfigurationNormalizationMatchesEveryCoreBoundary(t *testing.T) {
	for _, input := range []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{name: "omitted", want: `{}`},
		{name: "empty", raw: json.RawMessage{}, want: `{}`},
		{name: "null", raw: json.RawMessage(`null`), want: `null`},
	} {
		t.Run(input.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := core.New()
				defer closeEngine(t, e)
				def := contract.Definition{Validate: func(raw json.RawMessage) error {
					if string(raw) != input.want {
						return errors.New("configuration not normalized before validation")
					}
					return nil
				}, Start: func(_ context.Context, s contract.Scope) error {
					if string(s.Config()) != input.want {
						return errors.New("Start received a different configuration")
					}
					return nil
				}}
				entry := contract.Entry{ID: "config", Enabled: true, Config: input.raw}
				if err := e.Preflight(entry, def); err != nil {
					t.Fatal(err)
				}
				i, err := e.Add(entry, def, contract.Identity{})
				if err != nil {
					t.Fatal(err)
				}
				if err := i.Wait(t.Context()); err != nil {
					t.Fatal(err)
				}
				committed := false
				wait, err := e.Configure(entry.ID, input.raw, func() error { committed = true; return nil })
				if err != nil || !committed {
					t.Fatalf("configuration submission: committed=%v err=%v", committed, err)
				}
				if result, err := wait(t.Context()); err != nil || !result.Applied {
					t.Fatalf("configuration result: %+v %v", result, err)
				}
				if result, err := i.Update(t.Context(), input.raw); err != nil || !result.Applied {
					t.Fatalf("update result: %+v %v", result, err)
				}
			})
		})
	}
}

func TestOmittedConfigurationStillRequiresFullRootValidation(t *testing.T) {
	for _, root := range []string{`{"required":1}`, `[]`} {
		t.Run(root, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := core.New()
				defer closeEngine(t, e)
				invalid := errors.New("root contract rejected configuration")
				def := contract.Definition{Validate: func(raw json.RawMessage) error {
					if string(raw) != root {
						return invalid
					}
					return nil
				}, Start: definitionNoop().Start}
				entry := contract.Entry{ID: "config", Enabled: true}
				if err := e.Preflight(entry, def); !errors.Is(err, invalid) {
					t.Fatalf("preflight skipped root validation: %v", err)
				}
				if _, err := e.Add(entry, def, contract.Identity{}); !errors.Is(err, invalid) {
					t.Fatalf("add skipped root validation: %v", err)
				}
				entry.Config = json.RawMessage(root)
				i, err := e.Add(entry, def, contract.Identity{})
				if err != nil {
					t.Fatal(err)
				}
				if err := i.Wait(t.Context()); err != nil {
					t.Fatal(err)
				}
				before := i.Status()
				committed := false
				if _, err := e.Configure(entry.ID, nil, func() error { committed = true; return nil }); !errors.Is(err, invalid) || committed {
					t.Fatalf("invalid root committed: %v %v", committed, err)
				}
				after := i.Status()
				if after.Identity != before.Identity || after.Desired != before.Desired || after.State != contract.Active {
					t.Fatal("invalid root changed activation")
				}
			})
		})
	}
}
