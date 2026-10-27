//go:build integration

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestWorkerStdinLargeInputSchedulingAndEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	directory := t.TempDir()
	worker := filepath.Join(directory, "cordis-worker")
	if runtime.GOOS == "windows" {
		worker += ".exe"
	}
	guest := filepath.Join(directory, "stdin.wasm")
	for _, build := range []struct {
		output, source string
		wasi           bool
	}{
		{output: worker, source: "."},
		{output: guest, source: "./testdata/stdin", wasi: true},
	} {
		command := exec.CommandContext(ctx, "go", "build", "-o", build.output, build.source)
		if build.wasi {
			command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
		}
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", build.source, err, data)
		}
	}
	command := exec.CommandContext(ctx, worker, "-module", guest)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		if command.ProcessState == nil {
			command.Process.Kill()
			command.Wait()
		}
	})
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil || line != "idle\n" {
		t.Fatalf("guest scheduler blocked on empty stdin: %q / %v", line, err)
	}
	payload := bytes.Repeat([]byte("a large input need not be split by the host\n"), 12000)
	if _, err := stdin.Write(payload); err != nil {
		t.Fatalf("large worker input: %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("worker exited: %v, stderr=%q", err, stderr.String())
	}
	want := fmt.Sprintf("%d %x\n", len(payload), sha256.Sum256(payload))
	if string(output) != want || stderr.Len() != 0 {
		t.Fatalf("worker input corrupted: stdout=%q stderr=%q, want %q", output, stderr.String(), want)
	}
	t.Run("host cancellation while waiting for stdin", func(t *testing.T) {
		waiting, stop := context.WithCancel(ctx)
		defer stop()
		child := exec.CommandContext(waiting, worker, "-module", guest)
		input, err := child.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		output, err := child.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(output).ReadString('\n')
		stop()
		exit := child.Wait()
		if err != nil || line != "idle\n" || exit == nil {
			t.Fatalf("cancel waiting worker: line=%q read=%v exit=%v", line, err, exit)
		}
	})
}
