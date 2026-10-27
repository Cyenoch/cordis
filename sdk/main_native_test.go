//go:build !wasip1

package sdk_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
)

func TestSDKRunProcess(t *testing.T) {
	if os.Getenv("CORDIS_SDK_RUN_HELPER") == "1" {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := sdk.Run(ctx, contract.Definition{ID: "process", Start: func(_ context.Context, scope contract.Scope) error {
			if err := scope.Defer(func(context.Context) error { _, err := io.WriteString(os.Stderr, "cleaned\n"); return err }); err != nil {
				return err
			}
			switch os.Getenv("CORDIS_SDK_RUN_MODE") {
			case "fail":
				return errors.New("process initialization cause")
			case "cancel":
				return contract.Go(scope, func(ctx context.Context) error { cancel(); <-ctx.Done(); return nil })
			default:
				return nil
			}
		}})
		if err != nil {
			_, _ = io.WriteString(os.Stderr, err.Error()+"\n")
			os.Exit(1)
		}
		os.Exit(0)
	}
	for _, mode := range []string{"shutdown", "cancel", "fail", "signal"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "signal" && runtime.GOOS == "windows" {
				t.Skip("Windows does not send SIGTERM through os.Process.Signal")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSDKRunProcess$")
			command.Env = append(os.Environ(), "CORDIS_SDK_RUN_HELPER=1", "CORDIS_SDK_RUN_MODE="+mode)
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
			t.Cleanup(func() { _ = command.Process.Kill() })
			identity := contract.Identity{Activation: "run-process"}
			encoder := json.NewEncoder(stdin)
			reader := bufio.NewReader(stdout)
			call := func(id, method string, input json.RawMessage) wire.Frame {
				t.Helper()
				if err := encoder.Encode(wire.Frame{Schema: contract.SessionSchema, Kind: "request", Activation: identity.Activation,
					ID: id, Method: method, DeadlineMS: 10000, Payload: input}); err != nil {
					t.Fatal(err)
				}
				line, err := reader.ReadBytes('\n')
				if err != nil {
					t.Fatalf("protocol response: %v", err)
				}
				var frame wire.Frame
				if err := contract.Decode(line, &frame); err != nil || frame.Kind != "response" || frame.ID != id {
					t.Fatalf("stdout was not a protocol response: %s (%v)", line, err)
				}
				return frame
			}
			input, _ := json.Marshal(contract.Initialize{FrameBytes: contract.MaxFrameBytes, Identity: identity,
				Protocol: contract.Protocol{Major: 1}, Definition: "process", Config: json.RawMessage(`{}`)})
			initialized := call("h:1", "initialize", input)
			if mode == "fail" {
				if initialized.Error == nil {
					t.Fatal("failing Start initialized successfully")
				}
			} else if initialized.Error != nil {
				t.Fatal(initialized.Error)
			}
			switch mode {
			case "shutdown", "fail":
				if response := call("h:2", "shutdown", json.RawMessage(`{}`)); response.Error != nil {
					t.Fatal(response.Error)
				}
			case "cancel":
				// Managed work starts on publish, then cancels the Run context.
				_ = encoder.Encode(wire.Frame{Schema: contract.SessionSchema, Kind: "request", Activation: identity.Activation,
					ID: "h:2", Method: "activation.publish", DeadlineMS: 10000, Payload: json.RawMessage(`{}`)})
			case "signal":
				if err := command.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			remaining, readErr := io.ReadAll(reader)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, line := range bytes.Split(bytes.TrimSpace(remaining), []byte{'\n'}) {
				if len(line) == 0 {
					continue
				}
				var frame wire.Frame
				if err := contract.Decode(line, &frame); err != nil || frame.Schema != contract.SessionSchema {
					t.Fatalf("non-protocol stdout: %q (%v)", line, err)
				}
			}
			waitErr := command.Wait()
			if mode == "fail" {
				var exit *exec.ExitError
				if !errors.As(waitErr, &exit) || exit.ExitCode() != 1 || !strings.Contains(stderr.String(), "process initialization cause") {
					t.Fatalf("failed Run: exit=%v stderr=%s", waitErr, &stderr)
				}
			} else if waitErr != nil {
				t.Fatalf("Run exit=%v stderr=%s", waitErr, &stderr)
			}
			if strings.Count(stderr.String(), "cleaned\n") != 1 {
				t.Fatalf("process cleanup was skipped or repeated: %s", &stderr)
			}
		})
	}
}
