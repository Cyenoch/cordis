package wire

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"reflect"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

// The session schema accepts the response the runtime writes for a handler
// that fails with its own error code: the frame equals the accepted vector.
func TestApplicationErrorCodeMatchesSessionSchema(t *testing.T) {
	raw, err := os.ReadFile("../schema/testdata/interoperability.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Vectors []struct {
			Name     string          `json:"name"`
			Accept   bool            `json:"accept"`
			Instance json.RawMessage `json:"instance"`
		} `json:"schemaVectors"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	var want any
	for _, vector := range fixtures.Vectors {
		if vector.Name == "error-response-application-code" && vector.Accept {
			if err := json.Unmarshal(vector.Instance, &want); err != nil {
				t.Fatal(err)
			}
		}
	}
	if want == nil {
		t.Fatal("no accepted error-response-application-code vector")
	}

	a, b := net.Pipe()
	e, err := New(Options{FrameBytes: contract.MaxFrameBytes, Transport: a, Identity: contract.Identity{Activation: "activation-1"}, Handler: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, contract.Fail("game_running", "the game is running")
	}})
	if err != nil {
		t.Fatal(err)
	}
	past(e)
	defer func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
		e.Close()
		e.Wait()
	}()
	request, err := encodeFrame(Frame{Schema: contract.SessionSchema, Kind: "request", Activation: "activation-1", ID: "g:1", Method: "call", Payload: json.RawMessage(`{}`), DeadlineMS: 1000}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write(request); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(b).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var got any
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response %s, want the accepted vector %v", line, want)
	}
}
