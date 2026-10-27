package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func BenchmarkServiceDispatch(b *testing.B) {
	for _, size := range []struct {
		name  string
		bytes int
	}{
		{name: "32B", bytes: 32},
		{name: "1KiB", bytes: 1024},
	} {
		b.Run(size.name, func(b *testing.B) {
			engine := core.New()
			b.Cleanup(func() {
				if err := engine.Close(context.Background()); err != nil {
					b.Error(err)
				}
			})
			service := contract.Service{Name: "benchmark.echo", Major: 1}
			requirement := contract.Requirement{Name: service.Name, Major: service.Major}
			instance, err := engine.Add(contract.Entry{ID: "echo", Enabled: true}, contract.Definition{
				ID:       "echo",
				Provides: []contract.Service{service},
				Start: func(_ context.Context, scope contract.Scope) error {
					return scope.Provide(service, func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
						return input, nil
					})
				},
			}, contract.Identity{})
			if err != nil {
				b.Fatal(err)
			}
			if err := instance.Wait(b.Context()); err != nil {
				b.Fatal(err)
			}
			input := json.RawMessage(`"` + strings.Repeat("x", size.bytes-2) + `"`)
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))
			for b.Loop() {
				output, err := engine.Call(b.Context(), "", requirement, input)
				if err != nil {
					b.Fatal(err)
				}
				if !bytes.Equal(output, input) {
					b.Fatal("dispatch returned an unexpected payload")
				}
			}
		})
	}
}
