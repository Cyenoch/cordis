package main

import (
	"bytes"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestForwardStderrKeepsLinesSplitAcrossReads(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	forward := forwardStderr(&out)
	a := contract.Identity{Entry: "a", Activation: "1"}
	b := contract.Identity{Entry: "b", Activation: "2"}
	for _, r := range []contract.StderrRecord{
		{Owner: a, Data: []byte("panic: ")},
		{Owner: a, Data: []byte("boom\ngoroutine 1 [")},
		{Owner: a, Data: []byte("running]:\n")},
		{Owner: b, Data: []byte("ready\n")},
		{Owner: a, Data: []byte("partial"), Truncated: true},
	} {
		if err := forward(r); err != nil {
			t.Fatal(err)
		}
	}
	want := "[a] panic: boom\n[a] goroutine 1 [running]:\n[b] ready\n[a] partial\n[a] stderr truncated\n"
	if out.String() != want {
		t.Fatalf("forwarded:\n%s\nwant:\n%s", &out, want)
	}
}

func TestForwardStderrEndsAnUnfinishedLineWithItsActivation(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	forward := forwardStderr(&out)
	first := contract.Identity{Entry: "a", Activation: "1"}
	second := contract.Identity{Entry: "a", Activation: "2"}
	for _, r := range []contract.StderrRecord{
		{Owner: first, Data: []byte("half a line")},
		{Owner: first, End: true},
		{Owner: second, Data: []byte("restarted\n")},
		{Owner: second, End: true},
	} {
		if err := forward(r); err != nil {
			t.Fatal(err)
		}
	}
	want := "[a] half a line\n[a] restarted\n"
	if out.String() != want {
		t.Fatalf("forwarded:\n%s\nwant:\n%s", &out, want)
	}
}
