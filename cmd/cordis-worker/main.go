// cordis-worker is a single-activation WASI preview 1 command runner.
// Standard output belongs exclusively to the plugin session protocol.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

func run() (result error) {
	flags := flag.NewFlagSet("cordis-worker", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	modulePath := flags.String("module", "", "exact WASI command file")
	memoryPages := flags.Uint("memory-pages", 4096, "maximum 64KiB memory pages")
	artifactBytes := flags.Int64("artifact-bytes", 64<<20, "maximum module bytes")
	compileTimeout := flags.Duration("compile-timeout", 30*time.Second, "compilation deadline")
	startTimeout := flags.Duration("start-timeout", 30*time.Second, "deadline until command reads session stdin")
	cacheDirectory := flags.String("cache-directory", "", "trusted host-owned compiled-module cache")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return errors.New("invalid worker arguments")
	}
	if *modulePath == "" || flags.NArg() != 0 || *memoryPages == 0 || *memoryPages > 65536 || *artifactBytes < 8 || *compileTimeout <= 0 || *startTimeout <= 0 {
		return errors.New("invalid worker limits or module path")
	}
	file, err := os.Open(*modulePath)
	if err != nil {
		return errors.New("cannot open wasm command")
	}
	info, err := file.Stat()
	if err != nil {
		return errors.Join(errors.New("cannot stat wasm command"), file.Close())
	}
	if !info.Mode().IsRegular() || info.Size() > *artifactBytes {
		return errors.Join(errors.New("wasm command is not a bounded regular file"), file.Close())
	}
	binary, err := io.ReadAll(io.LimitReader(file, *artifactBytes+1))
	err = errors.Join(err, file.Close())
	if err != nil {
		return errors.New("cannot read wasm command")
	}
	if int64(len(binary)) > *artifactBytes {
		return errors.New("wasm command exceeds artifact byte limit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Closing stdio unblocks WASI host reads/writes. The owning host also kills
	// this worker after grace, including host functions that ignore cancellation.
	closeIO := context.AfterFunc(ctx, func() {
		if err := os.Stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			fmt.Fprintln(os.Stderr, "worker stdin close failed")
		}
		if err := os.Stdout.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			fmt.Fprintln(os.Stderr, "worker stdout close failed")
		}
	})
	defer closeIO()
	configurationRuntime := wazero.NewRuntimeConfig().WithMemoryLimitPages(uint32(*memoryPages)).WithCloseOnContextDone(true)
	if *cacheDirectory != "" {
		cache, err := wazero.NewCompilationCacheWithDir(*cacheDirectory)
		if err != nil {
			return errors.New("cannot open trusted compilation cache")
		}
		defer func() { result = errors.Join(result, cache.Close(context.WithoutCancel(ctx))) }()
		configurationRuntime = configurationRuntime.WithCompilationCache(cache)
	}
	runtime := wazero.NewRuntimeWithConfig(ctx, configurationRuntime)
	defer func() { result = errors.Join(result, runtime.Close(context.WithoutCancel(ctx))) }()
	compileCtx, compileCancel := context.WithTimeout(ctx, *compileTimeout)
	defer compileCancel()
	// Compiler cancellation is cooperative. A process-local watchdog gives even
	// a stuck compiler a hard wall-clock bound without harming other instances.
	compileWatchdog := time.AfterFunc(*compileTimeout, func() { os.Exit(124) })
	compiled, err := runtime.CompileModule(compileCtx, binary)
	compileWatchdog.Stop()
	if err != nil {
		return errors.New("wasm compilation failed")
	}
	if compileCtx.Err() != nil {
		return errors.New("wasm compilation deadline exceeded")
	}
	compileCancel()
	if len(compiled.ImportedMemories()) != 0 {
		return errors.New("imported wasm memory is forbidden")
	}
	for _, function := range compiled.ImportedFunctions() {
		module, name, imported := function.Import()
		if !imported || module != wasi_snapshot_preview1.ModuleName || strings.HasPrefix(name, "sock_") {
			return errors.New("wasm import is not an allowed WASI function")
		}
	}
	start, exists := compiled.ExportedFunctions()["_start"]
	if !exists || len(start.ParamTypes()) != 0 || len(start.ResultTypes()) != 0 {
		return errors.New("wasm module must export a WASI command _start")
	}
	startupTimer := time.AfterFunc(*startTimeout, func() { cancel(); os.Exit(124) })
	defer startupTimer.Stop()
	// Preserve *os.File so wazero exposes nonblocking read/poll_oneoff. A reader
	// wrapper loses that type and can freeze the guest's cooperative scheduler.
	wasiCtx := experimental.WithFunctionListenerFactory(ctx, experimental.FunctionListenerFactoryFunc(func(def api.FunctionDefinition) experimental.FunctionListener {
		if def.Name() != "fd_read" {
			return nil
		}
		return experimental.FunctionListenerFunc(func(_ context.Context, _ api.Module, _ api.FunctionDefinition, params []uint64, _ experimental.StackIterator) {
			if len(params) > 0 && params[0] == 0 {
				startupTimer.Stop()
			}
		})
	}))
	if err := instantiateWASI(wasiCtx, runtime); err != nil {
		return errors.New("WASI instantiation failed")
	}
	// Intentionally no WithFSConfig, WithEnv, listener, socket or process host
	// module. The guest receives no host path or parent command-line arguments.
	configuration := wazero.NewModuleConfig().WithName("plugin").WithArgs("cordis-plugin").
		WithStdin(os.Stdin).WithStdout(os.Stdout).WithStderr(os.Stderr).
		WithSysWalltime().WithSysNanotime().WithSysNanosleep().WithRandSource(rand.Reader)
	_, err = runtime.InstantiateModule(ctx, compiled, configuration)
	if err != nil {
		var exited *sys.ExitError
		if errors.As(err, &exited) {
			if exited.ExitCode() == 0 {
				return nil
			}
			return fmt.Errorf("wasm command exited with status %d", exited.ExitCode())
		}
		return errors.New("wasm command trapped or was canceled")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		// Never print guest stack traces, artifact paths or raw module contents.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
