// measure runs independently built counter plugins through the real runtimes.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/runtime/native"
	"github.com/Cyenoch/cordis/runtime/wasm"
)

type artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type sample struct {
	Runtime      string  `json:"runtime"`
	Iteration    int     `json:"iteration"`
	Phase        string  `json:"phase"`
	Operations   int     `json:"operations"`
	Nanoseconds  int64   `json:"nanoseconds"`
	NSPerOp      float64 `json:"ns_per_op"`
	HostBytes    uint64  `json:"host_allocated_bytes"`
	HostAllocs   uint64  `json:"host_allocations"`
	HostBytesOp  float64 `json:"host_bytes_per_op"`
	HostAllocsOp float64 `json:"host_allocations_per_op"`
}

type report struct {
	GoVersion         string   `json:"go_version"`
	OS                string   `json:"os"`
	Arch              string   `json:"arch"`
	GOMAXPROCS        int      `json:"gomaxprocs"`
	Iterations        int      `json:"iterations"`
	CallsPerSession   int      `json:"calls_per_session"`
	Timeout           string   `json:"timeout"`
	Worker            artifact `json:"worker"`
	Native            artifact `json:"native"`
	WASM              artifact `json:"wasm"`
	CompiledWASMCache string   `json:"compiled_wasm_cache"`
	AllocationScope   string   `json:"allocation_scope"`
	Samples           []sample `json:"samples"`
}

func identify(path string) (result artifact, err error) {
	result.Path, err = filepath.Abs(path)
	if err != nil {
		return result, fmt.Errorf("resolve artifact: %w", err)
	}
	file, err := os.Open(result.Path)
	if err != nil {
		return result, fmt.Errorf("open artifact: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return result, fmt.Errorf("hash artifact: %w", err)
	}
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

// Allocation counters cover the measuring host process only, including runtime
// goroutines active during the interval. They cannot observe child/guest heaps.
func measure(kind, phase string, iteration, operations int, fn func() error) (sample, error) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	err := fn()
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	if err != nil {
		return sample{}, err
	}
	bytes := after.TotalAlloc - before.TotalAlloc
	allocs := after.Mallocs - before.Mallocs
	return sample{
		Runtime: kind, Iteration: iteration, Phase: phase, Operations: operations,
		Nanoseconds: elapsed.Nanoseconds(), NSPerOp: float64(elapsed.Nanoseconds()) / float64(operations),
		HostBytes: bytes, HostAllocs: allocs,
		HostBytesOp: float64(bytes) / float64(operations), HostAllocsOp: float64(allocs) / float64(operations),
	}, nil
}

func activation(ctx context.Context, adapter contract.Runtime, kind, path string, iteration, calls int, timeout time.Duration) (samples []sample, result error) {
	service := contract.Service{Name: "example.counter", Major: 1}
	identity := contract.Identity{Package: "example.counter", Entry: "counter", Activation: contract.NewID()}
	manifest := contract.Manifest{
		Schema: contract.PackageSchema, ID: identity.Package, Version: "1.0.0", Publisher: "example",
		Protocol: contract.Protocol{Major: 1}, DefaultDefinition: "counter",
		Definitions: []contract.DefinitionSpec{{ID: "counter", Provides: []contract.Service{service}}},
		Runtime:     contract.RuntimeSpec{Kind: kind, Entry: filepath.Base(path)},
	}
	initialize, err := json.Marshal(contract.Initialize{
		Identity: identity, Protocol: manifest.Protocol, Definition: manifest.DefaultDefinition,
		Config: json.RawMessage(`{"initial":41}`),
	})
	if err != nil {
		return nil, fmt.Errorf("encode initialization: %w", err)
	}
	invocation, err := json.Marshal(contract.Invocation{Service: service, Input: json.RawMessage(`{"operation":"add","delta":1}`)})
	if err != nil {
		return nil, fmt.Errorf("encode invocation: %w", err)
	}
	var session contract.Session
	defer func() {
		if session != nil {
			if err := session.Close(); err != nil {
				result = errors.Join(result, fmt.Errorf("close %s activation: %w", kind, err))
			}
		}
	}()
	cold, err := measure(kind, "cold_process_start_initialize", iteration, 1, func() error {
		startup, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var err error
		session, err = adapter.Start(startup, contract.Launch{
			Identity: identity, Manifest: manifest, Directory: filepath.Dir(path), Artifact: filepath.Base(path),
			Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				return nil, contract.Fail(contract.Denied, "measurement grants no host access")
			},
		})
		if err != nil {
			return fmt.Errorf("start: %w", err)
		}
		output, err := session.Call(startup, "initialize", initialize)
		if err != nil {
			return fmt.Errorf("initialize: %w", err)
		}
		var registration contract.Registration
		if err := contract.Decode(output, &registration); err != nil {
			return fmt.Errorf("decode registration: %w", err)
		}
		if len(registration.Services) != 1 || registration.Services[0] != service ||
			len(registration.Subscriptions) != 1 || registration.Subscriptions[0] != "counter.changed" || len(registration.Contributions) != 0 {
			return errors.New("counter registration does not match the independent example")
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s activation %d: %w", kind, iteration, err)
	}
	samples = append(samples, cold)
	call := func(expected int) error {
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		output, err := session.Call(callCtx, "call", invocation)
		if err != nil {
			return fmt.Errorf("call counter: %w", err)
		}
		var response struct {
			Value  int    `json:"value"`
			Events int    `json:"events"`
			Host   string `json:"host,omitempty"`
		}
		if err := contract.Decode(output, &response); err != nil {
			return fmt.Errorf("decode counter result: %w", err)
		}
		if response.Value != expected || response.Events != 0 || response.Host != "" {
			return fmt.Errorf("counter returned %+v, want value=%d events=0 host=empty", response, expected)
		}
		return nil
	}
	first, err := measure(kind, "first_subprocess_call", iteration, 1, func() error { return call(42) })
	if err != nil {
		return nil, err
	}
	samples = append(samples, first)
	steady, err := measure(kind, "steady_subprocess_calls", iteration, calls, func() error {
		for index := range calls {
			if err := call(43 + index); err != nil {
				return fmt.Errorf("steady call %d: %w", index+1, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return append(samples, steady), nil
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("measure", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	workerPath := flags.String("worker", "", "independently built cordis-worker executable")
	nativePath := flags.String("native", "", "independently built native counter executable")
	wasmPath := flags.String("wasm", "", "independently built WASI counter command")
	iterations := flags.Int("iterations", 5, "fresh activations per runtime")
	calls := flags.Int("calls", 100, "steady sequential calls per activation after the first call")
	timeout := flags.Duration("timeout", 60*time.Second, "deadline for each startup+initialize or call")
	cache := flags.String("cache-directory", "", "optional trusted compiled WASM cache directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *workerPath == "" || *nativePath == "" || *wasmPath == "" || *iterations < 1 || *calls < 1 || *timeout <= 0 {
		return errors.New("require -worker, -native, -wasm, positive -iterations, -calls and -timeout, and no positional arguments")
	}
	worker, err := identify(*workerPath)
	if err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	nativeArtifact, err := identify(*nativePath)
	if err != nil {
		return fmt.Errorf("native: %w", err)
	}
	wasmArtifact, err := identify(*wasmPath)
	if err != nil {
		return fmt.Errorf("wasm: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	result := report{
		GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, GOMAXPROCS: runtime.GOMAXPROCS(0),
		Iterations: *iterations, CallsPerSession: *calls, Timeout: timeout.String(),
		Worker: worker, Native: nativeArtifact, WASM: wasmArtifact,
		CompiledWASMCache: "disabled; every fresh worker recompiles",
		AllocationScope:   "host process TotalAlloc/Mallocs deltas only; excludes native child, worker and WASM guest heaps; includes host background goroutines and payload validation",
	}
	if *cache != "" {
		result.CompiledWASMCache = "explicit persisted cache; first/reused labels indicate invocation order, not instrumented cache hit or miss"
	}
	cases := []struct {
		name    string
		path    string
		adapter contract.Runtime
	}{
		{name: "native", path: nativeArtifact.Path, adapter: native.New(native.Options{Lifetime: ctx})},
		{name: "wasm", path: wasmArtifact.Path, adapter: wasm.New(worker.Path, wasm.Options{Lifetime: ctx, CacheDirectory: *cache})},
	}
	for _, test := range cases {
		for iteration := 1; iteration <= *iterations; iteration++ {
			samples, err := activation(ctx, test.adapter, test.name, test.path, iteration, *calls, *timeout)
			if err != nil {
				return fmt.Errorf("%s iteration %d: %w", test.name, iteration, err)
			}
			if test.name == "wasm" && *cache != "" {
				if iteration == 1 {
					samples[0].Phase = "cache_first_start_initialize"
				} else {
					samples[0].Phase = "cache_reused_start_initialize"
				}
			}
			result.Samples = append(result.Samples, samples...)
		}
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("write measurement report: %w", err)
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
