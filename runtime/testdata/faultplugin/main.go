// faultplugin is a real native/WASI command for runtime integration tests.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type frame struct {
	Schema     string          `json:"schema"`
	Kind       string          `json:"kind"`
	Activation string          `json:"activation"`
	ID         string          `json:"id"`
	Method     string          `json:"method"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	DeadlineMS int64           `json:"deadlineMs,omitempty"`
}

func main() {
	if len(os.Args) == 3 && os.Args[1] == "heartbeat-child" {
		for {
			if err := os.WriteFile(os.Args[2], []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0600); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	reader := bufio.NewReader(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	counter := 0
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		var request frame
		if err := json.Unmarshal(line, &request); err != nil {
			panic(err)
		}
		if request.Kind != "request" {
			continue
		}
		var value any
		switch request.Method {
		case "initialize":
			var input struct {
				FrameBytes int `json:"frameBytes"`
			}
			if err := json.Unmarshal(request.Payload, &input); err != nil {
				panic(err)
			}
			value = map[string]any{"frameBytes": input.FrameBytes, "minor": 0, "features": []any{}, "services": []any{}, "subscriptions": []any{}, "contributions": []any{}}
		case "health":
			value = map[string]bool{"healthy": true}
		case "counter":
			counter++
			value = counter
		case "sum":
			var operands []int
			if err := json.Unmarshal(request.Payload, &operands); err != nil {
				panic(err)
			}
			sum := 0
			for _, operand := range operands {
				sum += operand
			}
			value = sum
		case "probe":
			var paths struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(request.Payload, &paths); err != nil {
				panic(err)
			}
			_, fileErr := os.ReadFile(paths.Path)
			cwd, cwdErr := os.Getwd()
			if cwdErr != nil {
				cwd = ""
			}
			value = map[string]any{"secret": os.Getenv("CORDIS_RUNTIME_TEST_SECRET"), "fileAccessible": fileErr == nil, "cwd": filepath.Clean(cwd)}
		case "panic":
			panic("runtime-test-secret-must-not-be-returned")
		case "spawn-child":
			var input struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(request.Payload, &input); err != nil {
				panic(err)
			}
			executable, err := os.Executable()
			if err != nil {
				panic(err)
			}
			child := exec.Command(executable, "heartbeat-child", input.Path)
			if err := child.Start(); err != nil {
				panic(err)
			}
			go func() { _ = child.Wait() }()
			deadline := time.Now().Add(time.Second)
			for {
				if _, err := os.Stat(input.Path); err == nil {
					break
				}
				if time.Now().After(deadline) {
					panic("child did not start")
				}
				time.Sleep(time.Millisecond)
			}
			value = map[string]int{"pid": child.Process.Pid}
		case "exit-zero":
			os.Exit(0)
		case "exit-after-stderr":
			// Separate writes let the host observe the final line in a later read.
			fmt.Fprintln(os.Stderr, "first-diagnostic")
			time.Sleep(50 * time.Millisecond)
			fmt.Fprintln(os.Stderr, "final-diagnostic")
			os.Exit(0)
		case "loop":
			for {
			}
		case "block":
			for {
				if _, err := reader.ReadByte(); err != nil {
					return
				}
			}
		case "malformed":
			fmt.Fprintln(os.Stdout, "not-json-runtime-test-secret")
			continue
		case "stderr":
			for range 100000 {
				fmt.Fprintln(os.Stderr, "runtime-test-secret")
			}
			continue
		case "shutdown":
			return
		default:
			panic("unexpected fixture operation")
		}
		payload, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		response := frame{Schema: request.Schema, Kind: "response", Activation: request.Activation, ID: request.ID, Method: request.Method, Payload: payload}
		if err := encoder.Encode(response); err != nil {
			return
		}
	}
}
