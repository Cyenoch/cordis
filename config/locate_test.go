package config_test

import (
	"errors"
	"testing"

	"github.com/Cyenoch/cordis/config"
	"github.com/Cyenoch/cordis/contract"
)

// Two fields share a name; a failure in either must point at its own line.
func TestDecodeLocatesFailuresByPath(t *testing.T) {
	type server struct {
		Port int `json:"port"`
	}
	type document struct {
		Primary server   `json:"primary"`
		Backup  server   `json:"backup"`
		Peers   []server `json:"peers"`
	}
	cases := []struct {
		name, source string
		path         string
		line         int
		unknown      string
	}{
		{"unknown field in the first of two same-named objects",
			"primary:\n  port: 1\n  extra: true\nbackup:\n  port: 2\npeers: []\n", "/primary/extra", 3, "extra"},
		{"unknown field in the second",
			"primary:\n  port: 1\nbackup:\n  port: 2\n  extra: true\npeers: []\n", "/backup/extra", 5, "extra"},
		{"unknown field inside an array item",
			"primary:\n  port: 1\nbackup:\n  port: 2\npeers:\n  - port: 3\n  - port: 4\n    extra: true\n", "/peers/1/extra", 8, "extra"},
		{"wrong type in the second of two same-named fields",
			"primary:\n  port: 1\nbackup:\n  port: nope\npeers: []\n", "/backup/port", 4, ""},
		{"null where a value is required",
			"primary:\n  port: 1\nbackup:\n  port: null\npeers: []\n", "/backup/port", 4, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, err := config.Parse("servers.yaml", []byte(c.source), "")
			if err != nil {
				t.Fatal(err)
			}
			var target document
			err = doc.Decode(&target)
			var located *config.Error
			if !errors.As(err, &located) {
				t.Fatalf("decode = %v, want a located error", err)
			}
			if located.Path != c.path || located.Line != c.line {
				t.Fatalf("located at %s line %d, want %s line %d (%v)", located.Path, located.Line, c.path, c.line, err)
			}
			var unknown *contract.UnknownFieldError
			if c.unknown != "" && (!errors.As(err, &unknown) || unknown.Field != c.unknown) {
				t.Fatalf("unknown field = %v", unknown)
			}
		})
	}
}
