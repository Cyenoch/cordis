package wire

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"
)

func TestFrameRequestIDBoundaries(t *testing.T) {
	for _, tc := range []struct {
		id    string
		valid bool
	}{
		{id: "h:1", valid: true},
		{id: "g:18446744073709551615", valid: true},
		{id: "h:18446744073709551616"},
		{id: "h:0"},
		{id: "h:01"},
		{id: "h:+1"},
		{id: "h:-1"},
		{id: "h:1_0"},
		{id: "h:\u0661"},
		{id: "h: 1"},
		{id: "x:1"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			data, err := json.Marshal(Frame{
				Schema: "cordis-go.session/v1", Kind: "cancel", Activation: "a", ID: tc.id,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = readFrame(bufio.NewReader(strings.NewReader(string(data)+"\n")), 1024)
			if (err == nil) != tc.valid {
				t.Fatalf("ID %q: %v; want valid=%t", tc.id, err, tc.valid)
			}
		})
	}
}

func TestFrameRejectsForbiddenPresentFields(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"response zero deadline", `{"schema":"cordis-go.session/v1","kind":"response","id":"h:1","activation":"a","method":"call","payload":{},"deadlineMs":0}`},
		{"response empty parent", `{"schema":"cordis-go.session/v1","kind":"response","id":"h:1","activation":"a","method":"call","payload":{},"parent":""}`},
		{"cancel empty method", `{"schema":"cordis-go.session/v1","kind":"cancel","id":"h:1","activation":"a","method":""}`},
		{"cancel zero deadline", `{"schema":"cordis-go.session/v1","kind":"cancel","id":"h:1","activation":"a","deadlineMs":0}`},
		{"request null error", `{"schema":"cordis-go.session/v1","kind":"request","id":"h:1","activation":"a","method":"call","payload":{},"deadlineMs":1,"error":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readFrame(bufio.NewReader(strings.NewReader(tc.input+"\n")), 1024); err == nil {
				t.Fatal("forbidden present field was treated as absent")
			}
		})
	}
}
