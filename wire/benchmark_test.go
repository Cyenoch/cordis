package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

// BenchmarkEndpointNetPipe measures framed request/response transport in memory.
// It does not include an OS pipe, process boundary, SDK, or service registry.
func BenchmarkEndpointNetPipe(b *testing.B) {
	for _, size := range []struct {
		name  string
		bytes int
	}{
		{name: "32B", bytes: 32},
		{name: "1KiB", bytes: 1024},
	} {
		b.Run(size.name, func(b *testing.B) {
			left, right := net.Pipe()
			handler := func(_ context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
				switch method {
				case "initialize":
					return json.RawMessage(`{}`), nil
				case "call":
					return input, nil
				default:
					return nil, contract.Fail(contract.Invalid, "unknown benchmark method")
				}
			}
			host, err := New(Options{Transport: left, Identity: contract.Identity{Activation: "benchmark"}, Handler: handler})
			if err != nil {
				b.Fatal(errors.Join(err, left.Close(), right.Close()))
			}
			b.Cleanup(func() {
				if err := host.Close(); err != nil {
					b.Error(err)
				}
				host.Wait()
			})
			guest, err := New(Options{Transport: right, Guest: true, Handler: handler})
			if err != nil {
				b.Fatal(errors.Join(err, right.Close()))
			}
			b.Cleanup(func() {
				if err := guest.Close(); err != nil {
					b.Error(err)
				}
				guest.Wait()
			})
			if _, err := host.Call(b.Context(), "initialize", json.RawMessage(`{}`)); err != nil {
				b.Fatal(err)
			}
			input := json.RawMessage(`"` + strings.Repeat("x", size.bytes-2) + `"`)
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))
			for b.Loop() {
				output, err := host.Call(b.Context(), "call", input)
				if err != nil {
					b.Fatal(err)
				}
				if !bytes.Equal(output, input) {
					b.Fatal("transport returned an unexpected payload")
				}
			}
		})
	}
}
