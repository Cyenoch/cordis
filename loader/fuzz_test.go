package loader

import "testing"

// A configuration file with any content is either read or rejected with an
// error; it never brings the application down.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"plugins:\n  - id: a\n    plugin: echo\n    config: {x: 1}\n",
		"plugins:\n  - plugins:\n      - plugin: a\n        isolate: {svc: true}\n      - plugin: b\n        isolate: {svc: shared}\n",
		"plugins:\n  - id: inc\n    include: other.yml\n    initial: {plugins: []}\n    patches:\n      - {op: remove, id: x}\n",
		"plugins:\n  - id: a\n    plugin: p\n    requires: [x]\n    settings: {svc: {k: v}}\n    disabled: true\n",
		"plugins: null\n", "plugins: [null]\n", "plugins:\n  - id: '~x'\n    plugin: p\n", "plugins: {a: 1}\n", "", "- 1\n", "plugins:\n  - &a {plugin: p}\n  - *a\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			t.Skip()
		}
		for _, name := range []string{"app.yml", "app.json", "app.toml"} {
			source, err := parse(name, []byte(input))
			if err == nil && source.document.validate() != nil {
				t.Fatalf("%s: parse returned a document that does not validate", name)
			}
		}
	})
}
