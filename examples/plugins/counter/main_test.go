package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/plugintest"
)

func TestCounter(t *testing.T) {
	for _, initial := range []string{"10", "10.0", "1e1"} {
		t.Run(initial, func(t *testing.T) {
			h, err := plugintest.New(t.Context(), NewCounterDefinition(start), plugintest.Options{Config: json.RawMessage(`{"initial":` + initial + `}`)})
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
			client, err := NewExampleCounterClient(h, ExampleCounterConsumer(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			output, err := client.Execute(t.Context(), ExampleCounterExecuteRequest{Operation: "add", Delta: json.Number("2.0")})
			if err != nil || output.Value != "12" {
				t.Fatalf("configured counter add: %+v %v", output, err)
			}
			if _, err := h.Deliver(t.Context(), contract.Event{Name: "counter.changed", Input: json.RawMessage(`{}`), Mode: contract.Sequential}); err != nil {
				t.Fatal(err)
			}
			output, err = client.Execute(t.Context(), ExampleCounterExecuteRequest{Operation: "get", Delta: json.Number("0")})
			if err != nil || output.Value != "12" || output.Events != "1" {
				t.Fatalf("counter retained state after event: %+v %v", output, err)
			}
		})
	}
}
