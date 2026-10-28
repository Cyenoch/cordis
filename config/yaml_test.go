package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/config"
)

func TestYAMLRejectsAmbiguousAndNativeValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, message string
	}{
		{name: "duplicate", input: "a: 1\na: 2\n", message: "duplicate"},
		{name: "quoted duplicate", input: "a: 1\n'a': 2\n", message: "duplicate"},
		{name: "anchor", input: "a: &value 1\n", message: "anchors"},
		{name: "alias", input: "a: *missing\n", message: "anchor"},
		{name: "merge", input: "a:\n  <<: {x: 1}\n", message: "merge"},
		{name: "custom scalar tag", input: "a: !custom value\n", message: "tag"},
		{name: "custom container tag", input: "a: !custom [1,2]\n", message: "tag"},
		{name: "native binary", input: "a: !!binary YQ==\n", message: "tag"},
		{name: "native set", input: "a: !!set {x: null}\n", message: "tag"},
		{name: "integer key", input: "1: value\n", message: "keys must be strings"},
		{name: "huge numeric key", input: "1e1000: value\n", message: "keys must be strings"},
		{name: "null key", input: "null: value\n", message: "keys must be strings"},
		{name: "collection key", input: "? [a, b]\n: value\n", message: "keys must be strings"},
		{name: "date", input: "a: 2026-10-27\n", message: "quoted strings"},
		{name: "timestamp", input: "a: 2026-10-27T00:00:00Z\n", message: "quoted strings"},
		{name: "tagged timestamp", input: "a: !!timestamp '2026-10-27'\n", message: "quoted strings"},
		{name: "nan", input: "a: .nan\n", message: "non-finite"},
		{name: "inf", input: "a: -.Inf\n", message: "non-finite"},
		{name: "invalid explicit float", input: "a: !!float 'garbage'\n", message: "decimal"},
		{name: "double signs explicit integer", input: "a: !!int '+-0x10'\n", message: "integer"},
		{name: "invalid explicit null", input: "a: !!null 'garbage'\n", message: "invalid"},
		{name: "invalid explicit bool", input: "a: !!bool 'yes'\n", message: "invalid"},
		{name: "trailing document", input: "a: 1\n---\na: 2\n", message: "trailing"},
		{name: "trailing empty document", input: "a: 1\n---\n", message: "trailing"},
		{name: "syntax", input: "a:\n b: [\n", message: "yaml"},
		{name: "empty", input: "", message: "EOF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Parse("settings.yaml", []byte(tt.input), "")
			var located *config.Error
			if !errors.As(err, &located) || located.Source != "settings.yaml" || located.Line < 1 {
				t.Fatalf("source/line diagnostic: %v", err)
			}
			if !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("want %q in %v", tt.message, err)
			}
		})
	}
}

func TestYAMLJSONModelAndNumericTokens(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, input, want string }{
		{name: "null", input: "null", want: "null"},
		{name: "null forms", input: "[null, Null, NULL, ~, 'null', '']", want: `[null,null,null,null,"null",""]`},
		{name: "missing scalar", input: "empty:\n", want: `{"empty":null}`},
		{name: "root sequence", input: "- true\n- false\n- 0\n- ''\n", want: `[true,false,0,""]`},
		{name: "string root", input: "'text'", want: `"text"`},
		{name: "quoted date", input: "a: '2026-10-27'\n", want: `{"a":"2026-10-27"}`},
		{name: "quoted merge key", input: "'<<': 1", want: `{"\u003c\u003c":1}`},
		{name: "json not yaml11 booleans", input: "[yes, no, on, off]", want: `["yes","no","on","off"]`},
		{name: "quoted number", input: "'0.10000000000000001'", want: `"0.10000000000000001"`},
		{name: "explicit string", input: "!!str 0.10000000000000001", want: `"0.10000000000000001"`},
		{name: "explicit float", input: "!!float '0.10000000000000001'", want: `0.10000000000000001`},
		{name: "float forms", input: "[+.5, 1., 01.20, -0.00, 1_000.0_1e+2]", want: `[0.5,1.0,1.20,-0.00,1000.01e+2]`},
		{name: "radix", input: "[0xFF, 0o10, 0b11, 0777, -0xFF, +1_000]", want: `[255,8,3,511,-255,1000]`},
		{name: "signed radix", input: "[+0x10, -0x10, +0o7, -0o7, +0b11, -0b11]", want: `[16,-16,7,-7,3,-3]`},
		{name: "double signs plus-minus hex", input: "+-0x10", want: `"+-0x10"`},
		{name: "double signs minus-plus hex", input: "-+0x10", want: `"-+0x10"`},
		{name: "double signs plus-minus octal", input: "+-0o7", want: `"+-0o7"`},
		{name: "beyond resolver float", input: "1e1000", want: `1e1000`},
		{name: "beyond resolver radix", input: "0xffffffffffffffffffffffff", want: `79228162514264337593543950335`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := config.Parse("settings.yml", []byte(tt.input), "")
			if err != nil {
				t.Fatal(err)
			}
			if string(doc.JSON) != tt.want {
				t.Fatalf("got %s, want %s", doc.JSON, tt.want)
			}
		})
	}
}
