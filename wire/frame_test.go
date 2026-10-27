package wire

import (
	"bufio"
	"strings"
	"testing"
)

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
