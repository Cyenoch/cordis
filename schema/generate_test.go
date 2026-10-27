package schema_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"testing"

	"github.com/Cyenoch/cordis/schema"
)

func TestGenerate(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/echo.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	definition, err := schema.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := schema.Generate(&output, "echo", definition); err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "echo.go", output.Bytes(), parser.AllErrors); err != nil {
		t.Fatalf("invalid generated Go: %v", err)
	}
	var repeated bytes.Buffer
	if err := schema.Generate(&repeated, "echo", definition); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), repeated.Bytes()) {
		t.Fatal("generation is nondeterministic")
	}
}

func TestGenerateRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, document, packageName string }{
		{"unknown definition field", `{"schema":"cordis-go.contract/v1","services":[],"reflect":true}`, "echo"},
		{"unknown method field", `{"schema":"cordis-go.contract/v1","services":[{"name":"echo","version":1,"methods":[{"name":"echo","request":{"type":"string"},"response":{"type":"string"},"stream":true}]}]}`, "echo"},
		{"unsupported data field", `{"schema":"cordis-go.contract/v1","services":[{"name":"echo","version":1,"methods":[{"name":"echo","request":{"type":"string","goType":"any"},"response":{"type":"string"}}]}]}`, "echo"},
		{"package keyword", `{"schema":"cordis-go.contract/v1","services":[{"name":"echo","version":1,"methods":[{"name":"echo","request":{"type":"string"},"response":{"type":"string"}}]}]}`, "type"},
		{"field collision", `{"schema":"cordis-go.contract/v1","services":[{"name":"echo","version":1,"methods":[{"name":"echo","request":{"type":"object","properties":{"x-y":{"type":"string"},"x_y":{"type":"string"}}},"response":{"type":"string"}}]}]}`, "echo"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			definition, err := schema.Parse([]byte(tt.document))
			if err != nil {
				return
			}
			var output bytes.Buffer
			if err := schema.Generate(&output, tt.packageName, definition); err == nil {
				t.Fatal("invalid declaration accepted")
			}
			if output.Len() != 0 {
				t.Fatal("generation wrote partial output on invalid definition")
			}
		})
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, os.ErrPermission }

func TestGeneratePropagatesWriteError(t *testing.T) {
	t.Parallel()
	definition := schema.Definition{Schema: schema.DefinitionSchema, Services: []schema.Service{{Name: "echo", Major: 1, Methods: []schema.Method{{Name: "echo", Request: json.RawMessage(`{"type":"string"}`), Response: json.RawMessage(`{"type":"string"}`)}}}}}
	if err := schema.Generate(brokenWriter{}, "echo", definition); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("write error lost: %v", err)
	}
}

func TestGenerateManyRejectsWholePackageCollision(t *testing.T) {
	t.Parallel()
	a, err := schema.Parse([]byte(`{"schema":"cordis-go.contract/v1","types":{"Shared":{"type":"string"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := schema.Parse([]byte(`{"schema":"cordis-go.contract/v1","types":{"Shared":{"type":"boolean"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	output.WriteString("existing output")
	if err := schema.GenerateMany(&output, "combined", a, b); err == nil {
		t.Fatal("cross-input collision accepted")
	}
	if output.String() != "existing output" {
		t.Fatal("failed generation changed destination")
	}
}

func TestGenerateManyFormatsAndNormalizesInputOrder(t *testing.T) {
	t.Parallel()
	a, err := schema.Parse([]byte(`{"schema":"cordis-go.contract/v1","definitions":[{"id":"one"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := schema.Parse([]byte(`{"schema":"cordis-go.contract/v1","definitions":[{"id":"two","config":{"type":"boolean"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if err := schema.GenerateMany(&first, "combined", a, b); err != nil {
		t.Fatal(err)
	}
	if err := schema.GenerateMany(&second, "combined", b, a); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("input order changes generated package")
	}
	formatted, err := format.Source(first.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), formatted) {
		t.Fatal("output is not gofmt formatted")
	}
}
