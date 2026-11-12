package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestServiceSelectionUsesDeclaredVersion(t *testing.T) {
	t.Parallel()
	m := contract.Manifest{Definitions: []contract.DefinitionSpec{{ID: "main", ConfigSchema: json.RawMessage(`{"type":"object"}`), Requires: []contract.Consumer{}, Provides: []contract.Service{{Name: "echo", Major: 1, Minor: 1}, {Name: "echo", Major: 2, Minor: 4}}}}}
	if _, err := selectService(m, "main", "echo", 0, 0, uint(^uint32(0))); err == nil {
		t.Fatal("ambiguous major accepted")
	}
	// A mistyped service shows what the definition offers instead.
	if _, err := selectService(m, "main", "ecoh", 0, 0, uint(^uint32(0))); err == nil || !strings.Contains(err.Error(), "[echo 1.1, echo 2.4]") {
		t.Fatalf("unknown service = %v", err)
	}
	req, err := selectService(m, "main", "echo", 2, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Accepts(m.Definitions[0].Provides[1]) || req.Accepts(m.Definitions[0].Provides[0]) {
		t.Fatal("wrong version selected")
	}
	m.Definitions[0].Provides = m.Definitions[0].Provides[:1]
	req, err = selectService(m, "main", "echo", 0, 0, uint(^uint32(0)))
	if err != nil || !req.Accepts(m.Definitions[0].Provides[0]) {
		t.Fatal(req, err)
	}
}

// A call without input is rejected before the archive is opened or a
// plugin started.
func TestRunRejectsAMissingInputBeforeStartup(t *testing.T) {
	t.Parallel()
	err := execute(t.Context(), []string{"run", "-dev-unsigned", "-service", "echo", "missing.zip"}, strings.NewReader(""), io.Discard, io.Discard)
	if exitCode(err, io.Discard) != 2 || !strings.Contains(err.Error(), "-service needs -input") {
		t.Fatalf("run without -input = %v", err)
	}
}

// A flag after the arguments is named, rather than counted as an argument.
func TestFlagAfterArgumentsIsNamed(t *testing.T) {
	t.Parallel()
	err := execute(t.Context(), []string{"run", "plugin.zip", "-dev-unsigned"}, strings.NewReader(""), io.Discard, io.Discard)
	if exitCode(err, io.Discard) != 2 || !strings.Contains(err.Error(), "flag -dev-unsigned comes after the arguments") {
		t.Fatalf("flag after the archive = %v", err)
	}
	// After "--" a dash starts a name, not a flag.
	err = execute(t.Context(), []string{"pack", "--", "-version", "out.zip"}, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || strings.Contains(err.Error(), "comes after the arguments") {
		t.Fatalf("argument after -- = %v", err)
	}
}

// A mistyped command suggests the one it most likely meant.
func TestUnknownCommandSuggestsTheClosest(t *testing.T) {
	t.Parallel()
	for typed, want := range map[string]string{
		"instal":  "unknown command: instal; did you mean install?",
		"stauts":  "unknown command: stauts; did you mean status?",
		"gnerate": "unknown command: gnerate; did you mean generate?",
		"bogus":   "unknown command: bogus",
	} {
		if err := execute(t.Context(), []string{typed}, strings.NewReader(""), io.Discard, io.Discard); err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %q", typed, err, want)
		}
	}
}
