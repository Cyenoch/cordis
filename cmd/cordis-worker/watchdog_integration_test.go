//go:build integration

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestStartupWatchdogInterruptsLoopWithSingleProcessor(t *testing.T) {
	dir := t.TempDir()
	worker := filepath.Join(dir, "cordis-worker")
	if runtime.GOOS == "windows" {
		worker += ".exe"
	}
	build := exec.Command("go", "build", "-o", worker, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v\n%s", err, output)
	}
	module := filepath.Join(dir, "loop.wasm")
	// A WASM command whose _start never calls a host function.
	binary := []byte{
		0, 97, 115, 109, 1, 0, 0, 0,
		1, 4, 1, 96, 0, 0,
		3, 2, 1, 0,
		7, 10, 1, 6, '_', 's', 't', 'a', 'r', 't', 0, 0,
		10, 9, 1, 7, 0, 3, 64, 12, 0, 11, 11,
	}
	if err := os.WriteFile(module, binary, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, worker, "-module", module, "-start-timeout", "100ms")
	command.Env = append(os.Environ(), "GOMAXPROCS=1")
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 124 {
		t.Fatalf("startup watchdog did not stop the command: ctx=%v err=%v output=%s", ctx.Err(), err, output)
	}
}
