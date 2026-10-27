package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/config"
)

func TestTOMLTablesArrayTablesAndDottedKeys(t *testing.T) {
	t.Parallel()
	input := `title = "demo"
owner.name = 'author'
point = { x = 1, y.z = 2, y.w = 3 }
matrix = [[1, 2], [3, 4]]
[parent.child]
value = true
[parent]
name = 'parent'
[[products]]
name = 'one'
[products.meta]
enabled = true
[[products.variants]]
name = 'red'
[[products.variants]]
name = 'blue'
[[products]]
name = 'two'
[products.meta]
enabled = false
[[products.variants]]
name = 'green'
`
	doc, err := config.Parse("settings.toml", []byte(input), "")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"matrix":[[1,2],[3,4]],"owner":{"name":"author"},"parent":{"child":{"value":true},"name":"parent"},"point":{"x":1,"y":{"w":3,"z":2}},"products":[{"meta":{"enabled":true},"name":"one","variants":[{"name":"red"},{"name":"blue"}]},{"meta":{"enabled":false},"name":"two","variants":[{"name":"green"}]}],"title":"demo"}`
	if string(doc.JSON) != want {
		t.Fatalf("got %s, want %s", doc.JSON, want)
	}
	if pos := doc.Positions["/products/1/variants/0/name"]; pos.Line != 22 || pos.Column != 1 {
		t.Fatalf("array table position: %+v", pos)
	}
}

func TestTOMLDottedKeyTraversesImplicitParent(t *testing.T) {
	doc, err := config.Parse("settings.toml", []byte("[a.b.c]\nx=1\n[a]\nb.y=2\n"), "")
	if err != nil || string(doc.JSON) != `{"a":{"b":{"c":{"x":1},"y":2}}}` {
		t.Fatalf("implicit parent: %s, %v", doc.JSON, err)
	}
	for _, source := range []string{"[a.b]\nx=1\n[a]\nb.y=2", "a={b={x=1}}\na.b.y=2", "a=1\na.b=2"} {
		if _, err := config.Parse("settings.toml", []byte(source), ""); err == nil {
			t.Fatalf("accepted conflicting dotted key: %s", source)
		}
	}
}

func TestTOMLRejectsDuplicateTablesKeysAndNativeValues(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, input, message string }{
		{name: "duplicate key", input: "a=1\na=2", message: "duplicate"},
		{name: "duplicate quoted key", input: "a=1\n'a'=2", message: "duplicate"},
		{name: "duplicate table", input: "[a]\nx=1\n[a]\ny=2", message: "table"},
		{name: "duplicate empty table", input: "[a]\n[a]\n", message: "table"},
		{name: "duplicate nested table", input: "[a.b]\n[a.b]\n", message: "table"},
		{name: "dotted table redefined", input: "a.b=1\n[a]\nc=2", message: "table"},
		{name: "table scalar conflict", input: "a=1\n[a]\n", message: "table"},
		{name: "scalar table conflict", input: "a=1\n[a.b]\n", message: "table"},
		{name: "inline duplicate", input: "a={b=1,b=2}", message: "duplicate"},
		{name: "inline dotted duplicate", input: "a={b.c=1,b.c=2}", message: "duplicate"},
		{name: "inline extension", input: "a={b=1}\na.c=2", message: "redefines"},
		{name: "inline table extension", input: "a={b=1}\n[a.c]\n", message: "table"},
		{name: "array table conflict", input: "a=[]\n[[a]]\n", message: "defined"},
		{name: "table array conflict", input: "[a]\n[[a]]\n", message: "defined"},
		{name: "array regular table conflict", input: "[[a]]\n[a]\n", message: "table"},
		{name: "duplicate per array element", input: "[[a]]\n[a.b]\n[a.b]\n", message: "table"},
		{name: "date", input: "date=2026-10-27", message: "quoted strings"},
		{name: "time", input: "date=12:30:00", message: "quoted strings"},
		{name: "datetime", input: "date=2026-10-27T12:30:00Z", message: "quoted strings"},
		{name: "nan", input: "n=nan", message: "non-finite"},
		{name: "positive infinity", input: "n=+inf", message: "non-finite"},
		{name: "negative infinity", input: "n=-inf", message: "non-finite"},
		{name: "null", input: "n=null", message: ""},
		{name: "non object", input: "[1,2]", message: "use YAML or JSON"},
		{name: "syntax", input: "a=1\nvalue=\n", message: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Parse("settings.toml", []byte(tt.input), "")
			var located *config.Error
			if !errors.As(err, &located) || located.Source != "settings.toml" || located.Line < 1 {
				t.Fatalf("source/line diagnostic: %v", err)
			}
			if !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("want %q in %v", tt.message, err)
			}
		})
	}
}

func TestTOMLNumericGrammarAndPrecision(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"1__0", "1_", "0x_FF", "0xFF_", "0b2", "0o8", "+0x10", "1._0", "1_.0", "1e_2", "1e2_", "1e", "1e+", "1-2", "00", "01.0", "1.", ".5"} {
		t.Run("invalid "+text, func(t *testing.T) {
			if _, err := config.Parse("input.toml", []byte("n="+text), ""); err == nil {
				t.Fatalf("accepted invalid TOML number %q", text)
			}
		})
	}
	tests := []struct{ text, want string }{
		{text: "0xDEAD_beef", want: "3735928559"},
		{text: "0o7_55", want: "493"},
		{text: "0b1010_0101", want: "165"},
		{text: "+1_000", want: "1000"},
		{text: "-1_000", want: "-1000"},
		{text: "0xFFFFFFFFFFFFFFFFFFFFFFFF", want: "79228162514264337593543950335"},
		{text: "0.10000000000000001", want: "0.10000000000000001"},
		{text: "+1_000.000_000_000_000_01e-2", want: "1000.00000000000001e-2"},
		{text: "-0.0", want: "-0.0"},
		{text: "1e1000", want: "1e1000"},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			doc, err := config.Parse("input.toml", []byte("n="+tt.text), "")
			if err != nil {
				t.Fatal(err)
			}
			if string(doc.JSON) != `{"n":`+tt.want+`}` {
				t.Fatalf("got %s, want numeric token %s", doc.JSON, tt.want)
			}
		})
	}
}

func TestTOMLDoesNotInventNull(t *testing.T) {
	t.Parallel()
	doc, err := config.Parse("input.toml", []byte("empty=''\nword='null'\ndate='2026-10-27'"), "")
	if err != nil {
		t.Fatal(err)
	}
	if string(doc.JSON) != `{"date":"2026-10-27","empty":"","word":"null"}` {
		t.Fatalf("TOML strings were reinterpreted: %s", doc.JSON)
	}
	empty, err := config.Parse("input.toml", nil, "")
	if err != nil || string(empty.JSON) != "{}" {
		t.Fatalf("empty TOML root: %v, %v", empty, err)
	}
}

func TestTOMLArrayItemLocations(t *testing.T) {
	t.Parallel()
	input := "# arrays\nitems = [\n  [], # empty\n  [1, []],\n  { name = 'x', inner = [] },\n  false,\n  \"\"\"multi\nline\"\"\",\n  []\n]\n"
	doc, err := config.Parse("arrays.toml", []byte(input), "")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path         string
		line, column int
	}{
		{path: "/items/0", line: 3, column: 3},
		{path: "/items/1", line: 4, column: 3},
		{path: "/items/1/1", line: 4, column: 7},
		{path: "/items/2", line: 5, column: 3},
		{path: "/items/3", line: 6, column: 3},
		{path: "/items/4", line: 7, column: 3},
		{path: "/items/5", line: 9, column: 3},
	}
	for _, tt := range tests {
		if pos := doc.Positions[tt.path]; pos.Line != tt.line || pos.Column != tt.column {
			t.Fatalf("%s: got %+v, want %d:%d", tt.path, pos, tt.line, tt.column)
		}
	}
}

func TestTOMLDottedDepthAndArrayTableCollectionBounds(t *testing.T) {
	t.Parallel()
	for _, depth := range []int{32, 33} {
		input := strings.Repeat("a.", depth-1) + "value=1"
		_, err := config.Parse("input.toml", []byte(input), "")
		if (err != nil) != (depth > 32) {
			t.Fatalf("dotted depth %d: %v", depth, err)
		}
	}
	for _, count := range []int{10000, 10001} {
		input := strings.Repeat("[[item]]\n", count)
		_, err := config.Parse("input.toml", []byte(input), "")
		if (err != nil) != (count > 10000) {
			t.Fatalf("array table entries %d: %v", count, err)
		}
	}
}
