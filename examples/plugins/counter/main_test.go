package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/plugintest"
	"github.com/Cyenoch/cordis/sdk"
)

func TestCounter(t *testing.T) {
	for _, initial := range []string{"10", "10.0", "1e1"} {
		t.Run(initial, func(t *testing.T) {
			h, err := plugintest.Start(t.Context(), CounterDefinitionSpec(), sdk.Define(CounterPlugin(start)),
				plugintest.Options{Config: json.RawMessage(`{"initial":` + initial + `}`)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := h.Close(ctx); err != nil {
					t.Error(err)
				}
			})
			endpoint, err := ExampleCounterEndpoint.Get(h.Scope())
			if err != nil {
				t.Fatal(err)
			}
			client, err := NewExampleCounterClient(endpoint)
			if err != nil {
				t.Fatal(err)
			}
			output, err := client.Execute(t.Context(), ExampleCounterExecuteRequest{Operation: "add", Delta: json.Number("2.0")})
			if err != nil || output.Value != "12" {
				t.Fatalf("configured counter add: %+v %v", output, err)
			}
			if err := changed.Emit(t.Context(), h.Scope(), struct{}{}); err != nil {
				t.Fatal(err)
			}
			output, err = client.Execute(t.Context(), ExampleCounterExecuteRequest{Operation: "get", Delta: json.Number("0")})
			if err != nil || output.Value != "12" || output.Events != "1" {
				t.Fatalf("counter retained state after event: %+v %v", output, err)
			}
		})
	}
}
