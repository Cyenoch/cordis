package plugintest_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/plugintest"
	"github.com/Cyenoch/cordis/sdk"
)

// A plugin that requires a service nobody provides does not leave the test
// hanging: Start says what it waits for.
func TestStartReportsAMissingProvider(t *testing.T) {
	spec := contract.DefinitionSpec{ID: "main", ConfigSchema: json.RawMessage(`{"type":"object"}`), Provides: []contract.Service{},
		Requires: []contract.Consumer{{Requirement: contract.Requirement{Name: "example.missing", Major: 1, MinMinor: 0, MaxMinor: 2}, Contract: strings.Repeat("ab", 32)}}}
	plugin := cordis.Plugin[struct{}]{Name: "main", Start: func(context.Context, *cordis.Scope, struct{}) error { return nil }}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	h, err := plugintest.Start(ctx, spec, sdk.Define(plugin), plugintest.Options{})
	if h != nil || err == nil || !strings.Contains(err.Error(), `definition "main" is still waiting for example.missing@1.0-2`) {
		t.Fatalf("Start = %v, %v", h, err)
	}
}
