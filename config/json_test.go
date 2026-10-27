package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/config"
)

func TestJSONRejectsDuplicateTrailingAndSyntaxErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, message, path string
		line                       int
	}{
		{name: "duplicate", input: "{\n\"a\": 1,\n\"a\": 2}", message: "duplicate", path: "/a", line: 3},
		{name: "escaped duplicate", input: `{"a":1,"\u0061":2}`, message: "duplicate", path: "/a", line: 1},
		{name: "nested duplicate", input: `{"a":[{"b":1,"b":2}]}`, message: "duplicate", path: "/a/0/b", line: 1},
		{name: "trailing value", input: "{}\n{}", message: "trailing", line: 2},
		{name: "trailing junk", input: "{}\nxyz", message: "trailing", line: 2},
		{name: "syntax", input: "{\n\"value\": ]}", line: 2},
		{name: "trailing comma", input: "[1,]", line: 1},
		{name: "empty", input: "", line: 1},
		{name: "nonfinite", input: "{\n\"value\": NaN}", line: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Parse("settings.json", []byte(tt.input), "")
			var located *config.Error
			if !errors.As(err, &located) || located.Source != "settings.json" || located.Line != tt.line {
				t.Fatalf("syntax diagnostic: %v", err)
			}
			if located.Path != tt.path && tt.path != "" || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("diagnostic: %v", err)
			}
		})
	}
}

func TestJSONPreservesNonObjectRoots(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"null", "[null,false,0,\"\"]", "0.10000000000000001", "true", `"text"`, "1e1000"} {
		t.Run(input, func(t *testing.T) {
			doc, err := config.Parse("input.json", []byte(input), "")
			if err != nil || string(doc.JSON) != input {
				t.Fatalf("%s => %v, %v", input, doc, err)
			}
		})
	}
}
