package config_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/config"
)

func TestParseExactDataAndLocations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, source, data string
		format             config.Format
	}{
		{
			name: "json", source: "settings.json", format: config.JSON,
			data: "{\n  \"a/b~c\": {\"value\": 0.10000000000000001},\n  \"large\": 123456789012345678901234567890, \"list\": [true, \"\"]\n}",
		},
		{
			name: "yaml", source: "settings.yaml", format: config.YAML,
			data: "# settings\na/b~c:\n  value: 0.10000000000000001\nlarge: 123456789012345678901234567890\nlist: [true, '']\n",
		},
		{
			name: "toml", source: "settings.toml", format: config.TOML,
			data: "# settings\n'a/b~c'.value = 0.10000000000000001\nlarge = 123456789012345678901234567890\nlist = [true, '']\n",
		},
	}
	want := `{"a/b~c":{"value":0.10000000000000001},"large":123456789012345678901234567890,"list":[true,""]}`
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := config.Parse(tt.source, []byte(tt.data), tt.format)
			if err != nil {
				t.Fatal(err)
			}
			if string(doc.JSON) != want || doc.Source != tt.source {
				t.Fatalf("got %s from %q, want %s", doc.JSON, doc.Source, want)
			}
			for _, path := range []string{"", "/a~1b~0c", "/a~1b~0c/value", "/list/0", "/large"} {
				if pos, ok := doc.Positions[path]; !ok || pos.Line < 1 || pos.Column < 1 {
					t.Fatalf("missing location for %s: %+v", path, doc.Positions)
				}
			}
			var decoded struct {
				Nested struct {
					Value json.Number `json:"value"`
				} `json:"a/b~c"`
				Large json.RawMessage `json:"large"`
				List  json.RawMessage `json:"list"`
			}
			if err := doc.Decode(&decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Nested.Value != "0.10000000000000001" || string(decoded.Large) != "123456789012345678901234567890" {
				t.Fatalf("numeric precision lost: %+v", decoded)
			}
			var portable struct {
				Large  json.Number     `json:"large"`
				Nested json.RawMessage `json:"a/b~c"`
				List   json.RawMessage `json:"list"`
			}
			if err := doc.Decode(&portable); err == nil {
				t.Fatal("lossless parsing bypassed typed safe-integer boundary")
			}
		})
	}
}

func TestParseSelectsOnlyDeclaredFormat(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"a.json", "a.JSON", "a.yaml", "a.yml", "a.toml"} {
		t.Run(source, func(t *testing.T) {
			data := "{}"
			if filepath.Ext(source) == ".toml" {
				data = ""
			}
			if _, err := config.Parse(source, []byte(data), ""); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, source := range []string{"-", "stdin", "settings", "settings.txt", ""} {
		if _, err := config.Parse(source, []byte("{}"), ""); err == nil {
			t.Fatalf("guessed format for %q", source)
		}
		if _, err := config.Parse(source, []byte("{}"), config.JSON); err != nil {
			t.Fatalf("explicit stdin format: %v", err)
		}
	}
	if _, err := config.Parse("a.json", []byte("answer: 42"), ""); err == nil {
		t.Fatal("fell back from JSON to YAML")
	}
	if _, err := config.Parse("a.json", []byte("answer: 42"), config.YAML); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Parse("a.json", []byte("{}"), "xml"); err == nil {
		t.Fatal("accepted unknown format")
	}
}

func TestParseInputBounds(t *testing.T) {
	t.Parallel()
	for _, format := range []config.Format{config.JSON, config.YAML, config.TOML} {
		t.Run(string(format), func(t *testing.T) {
			if _, err := config.Parse("input", []byte{0xff}, format); err == nil || !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("invalid UTF-8: %v", err)
			}
			valid := "{}"
			if format == config.TOML {
				valid = ""
			}
			data := append([]byte(valid), bytes.Repeat([]byte(" "), (1<<20)-len(valid))...)
			if _, err := config.Parse("input", data, format); err != nil {
				t.Fatalf("1 MiB boundary: %v", err)
			}
			if _, err := config.Parse("input", append(data, ' '), format); err == nil || !strings.Contains(err.Error(), "bytes") {
				t.Fatalf("oversized source: %v", err)
			}
		})
	}
}

func TestParseCollectionAndDepthBounds(t *testing.T) {
	t.Parallel()
	for _, format := range []config.Format{config.JSON, config.YAML, config.TOML} {
		t.Run(string(format), func(t *testing.T) {
			for _, count := range []int{10000, 10001} {
				data := "[" + strings.Repeat("0,", count-1) + "0]"
				if format == config.TOML {
					data = "list=" + data
				}
				_, err := config.Parse("input", []byte(data), format)
				if (err != nil) != (count > 10000) {
					t.Fatalf("collection %d: %v", count, err)
				}
			}
			for _, depth := range []int{32, 33} {
				arrays := depth
				prefix := ""
				if format == config.TOML {
					arrays-- // The TOML root is already an object.
					prefix = "list="
				}
				data := prefix + strings.Repeat("[", arrays) + "0" + strings.Repeat("]", arrays)
				_, err := config.Parse("input", []byte(data), format)
				if (err != nil) != (depth > 32) {
					t.Fatalf("depth %d: %v", depth, err)
				}
			}
		})
	}
}

func TestParseObjectCollectionBounds(t *testing.T) {
	t.Parallel()
	for _, format := range []config.Format{config.JSON, config.YAML, config.TOML} {
		t.Run(string(format), func(t *testing.T) {
			for _, count := range []int{10000, 10001} {
				var data strings.Builder
				if format == config.JSON {
					data.WriteByte('{')
				}
				for i := range count {
					key := strconv.Itoa(i)
					switch format {
					case config.JSON:
						if i > 0 {
							data.WriteByte(',')
						}
						data.WriteString(`"k` + key + `":0`)
					case config.YAML:
						data.WriteString("k" + key + ": 0\n")
					case config.TOML:
						data.WriteString("k" + key + "=0\n")
					}
				}
				if format == config.JSON {
					data.WriteByte('}')
				}
				_, err := config.Parse("input", []byte(data.String()), format)
				if (err != nil) != (count > 10000) {
					t.Fatalf("object members %d: %v", count, err)
				}
			}
		})
	}
}

func TestLoadBoundedFileAndErrors(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte("name='ok'"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := config.Load(path, "")
	if err != nil || doc.Source != path || string(doc.JSON) != `{"name":"ok"}` {
		t.Fatalf("load: %v, %v", doc, err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{' '}, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path, ""); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("oversized file: %v", err)
	}
	_, err = config.Load(path+".missing", "")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read error lost its cause: %v", err)
	}
}

func TestDecodeStrictUnknownFieldLocations(t *testing.T) {
	t.Parallel()
	for _, format := range []config.Format{config.JSON, config.YAML, config.TOML} {
		t.Run(string(format), func(t *testing.T) {
			input := "{\n\"nested\": {\n\"surprise\": true}}"
			if format == config.YAML {
				input = "nested:\n  known: false\n  surprise: true\n"
			} else if format == config.TOML {
				input = "[nested]\nknown=false\nsurprise=true\n"
			}
			doc, err := config.Parse("settings", []byte(input), format)
			if err != nil {
				t.Fatal(err)
			}
			var target struct {
				Nested struct {
					Known bool `json:"known"`
				} `json:"nested"`
			}
			err = doc.Decode(&target)
			var located *config.Error
			if !errors.As(err, &located) || located.Source != "settings" || located.Path != "/nested/surprise" || located.Line != 3 {
				t.Fatalf("unknown field location: %v", err)
			}
			if err := doc.Decode(nil); err == nil {
				t.Fatal("nil decode target accepted")
			}
		})
	}
}

func TestLocatePreservesPathAndCause(t *testing.T) {
	t.Parallel()
	doc, err := config.Parse("my.yaml", []byte("outer:\n  'a/b~c': true\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("invalid business value")
	err = doc.Locate("/outer/a~1b~0c/missing", cause)
	var located *config.Error
	if !errors.Is(err, cause) || !errors.As(err, &located) || located.Line != 2 || located.Column != 3 {
		t.Fatalf("location and cause: %v", err)
	}
	if located.Path != "/outer/a~1b~0c/missing" || !strings.Contains(err.Error(), "my.yaml:2:3") {
		t.Fatalf("diagnostic: %v", err)
	}
	if doc.Locate("/outer", nil) != nil {
		t.Fatal("nil cause became an error")
	}
}

func TestUnicodeColumns(t *testing.T) {
	t.Parallel()
	tests := []struct {
		format config.Format
		input  string
		column int
	}{
		{format: config.JSON, input: "[\"\u914d\u7f6e\", []]", column: 8},
		{format: config.YAML, input: "[\"\u914d\u7f6e\", []]", column: 8},
		{format: config.TOML, input: "items = [\"\u914d\u7f6e\", []]", column: 16},
	}
	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			doc, err := config.Parse("unicode", []byte(tt.input), tt.format)
			if err != nil {
				t.Fatal(err)
			}
			path := "/1"
			if tt.format == config.TOML {
				path = "/items/1"
			}
			if pos := doc.Positions[path]; pos.Line != 1 || pos.Column != tt.column {
				t.Fatalf("got %+v, want column %d", pos, tt.column)
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{"", "null", "{}", "a: [1, null]", "a={b=1}", "[[a]]\nx=1", "a='x'\n[a]", "{\"a\":1,\"a\":2}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			t.Skip()
		}
		for _, format := range []config.Format{config.JSON, config.YAML, config.TOML} {
			_, err := config.Parse("fuzz-input", []byte(input), format)
			if err == nil {
				continue
			}
			var located *config.Error
			if !errors.As(err, &located) || located.Source != "fuzz-input" || located.Line < 1 {
				t.Fatalf("unlocated %s failure: %v", format, err)
			}
		}
	})
}
