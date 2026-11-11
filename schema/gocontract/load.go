// Package gocontract derives contracts from annotated Go source and
// generates the code that binds the author's own types to them.
//
// An interface with a //cordis:service directive is a service; a function
// with a //cordis:plugin directive is a plugin. Request, response and
// configuration schemas come from the Go types, following encoding/json.
package gocontract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Cyenoch/cordis/internal/processtree"
)

const (
	// GoFile is the generated Go source, in the analyzed package.
	GoFile = "cordis_gen.go"
	// ContractFile is the derived contract, next to GoFile.
	ContractFile = "cordis_gen.json"

	kernelPath = "github.com/Cyenoch/cordis"
)

// listed is the part of `go list -json` the analysis reads.
type listed struct {
	ImportPath string
	Name       string
	Dir        string
	Export     string
	GoFiles    []string
	DepOnly    bool
	ImportMap  map[string]string
	Error      *struct{ Err string }
}

// source is a type-checked package and what is known about its dependencies.
type source struct {
	fset     *token.FileSet
	root     listed
	files    []*ast.File
	pkg      *types.Package
	info     *types.Info
	packages map[string]listed
	// typeErrors are tolerated: code that uses generated names does not
	// compile until the names have been generated.
	typeErrors []error
}

func load(ctx context.Context, dir string) (*source, error) {
	command := exec.Command("go", "list", "-e", "-deps", "-export",
		"-json=ImportPath,Name,Dir,Export,GoFiles,DepOnly,ImportMap,Error", ".")
	command.Dir = dir
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	// go list compiles dependencies in child processes; canceling must end
	// those too, or their open pipes keep this call waiting.
	if err := processtree.Run(ctx, command); err != nil {
		return nil, fmt.Errorf("go list in %s: %w: %s", dir, err, strings.TrimSpace(stderr.String()))
	}
	s := &source{fset: token.NewFileSet(), packages: map[string]listed{}}
	found := false
	decoder := json.NewDecoder(&stdout)
	for {
		var item listed
		if err := decoder.Decode(&item); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list in %s: %w", dir, err)
		}
		if item.DepOnly {
			s.packages[item.ImportPath] = item
			continue
		}
		s.root, found = item, true
	}
	if !found || len(s.root.GoFiles) == 0 {
		reason := "no Go files"
		if found && s.root.Error != nil {
			reason = s.root.Error.Err
		}
		return nil, fmt.Errorf("%s: %s", dir, reason)
	}
	for _, name := range s.root.GoFiles {
		if name == GoFile {
			continue
		}
		file, err := parser.ParseFile(s.fset, filepath.Join(s.root.Dir, name), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		s.files = append(s.files, file)
	}
	if len(s.files) == 0 {
		return nil, fmt.Errorf("%s: no Go files besides %s", dir, GoFile)
	}
	s.info = &types.Info{Defs: map[*ast.Ident]types.Object{}, Implicits: map[ast.Node]types.Object{}}
	config := types.Config{
		Importer: &exports{source: s, gc: importer.ForCompiler(s.fset, "gc", s.export)},
		Error:    func(err error) { s.typeErrors = append(s.typeErrors, err) },
	}
	// Errors are collected above; what matters is which types came out valid.
	s.pkg, _ = config.Check(s.root.ImportPath, s.fset, s.files, s.info)
	return s, nil
}

// exports imports dependencies from the export data `go list` built.
type exports struct {
	source *source
	gc     types.Importer
}

func (e *exports) Import(path string) (*types.Package, error) {
	if mapped, ok := e.source.root.ImportMap[path]; ok {
		path = mapped
	}
	return e.gc.Import(path)
}

func (s *source) export(path string) (io.ReadCloser, error) {
	item, ok := s.packages[path]
	if !ok {
		return nil, fmt.Errorf("package %s is not a dependency", path)
	}
	if item.Export == "" {
		reason := "no export data"
		if item.Error != nil {
			reason = item.Error.Err
		}
		return nil, fmt.Errorf("package %s does not compile: %s", path, reason)
	}
	return os.Open(item.Export)
}

// position renders pos relative to the analyzed directory.
func (s *source) position(pos token.Pos) string {
	position := s.fset.Position(pos)
	if !position.IsValid() {
		return s.root.Dir
	}
	if relative, err := filepath.Rel(s.root.Dir, position.Filename); err == nil && !strings.HasPrefix(relative, "..") {
		position.Filename = relative
	}
	return position.String()
}

func (s *source) errorf(pos token.Pos, format string, args ...any) error {
	return fmt.Errorf("%s: %s", s.position(pos), fmt.Sprintf(format, args...))
}

// invalid explains a type that did not type-check with the first errors of
// the package.
func (s *source) invalid() error {
	shown := s.typeErrors
	if len(shown) > 3 {
		shown = shown[:3]
	}
	return fmt.Errorf("type does not compile: %w", errors.Join(shown...))
}

// declaration finds the type declaration of a named type in a dependency,
// where only export data is loaded, to read its directives.
func (s *source) declaration(obj *types.TypeName) (*ast.CommentGroup, error) {
	item, ok := s.packages[obj.Pkg().Path()]
	if !ok {
		return nil, fmt.Errorf("package %s is not a dependency", obj.Pkg().Path())
	}
	for _, name := range item.GoFiles {
		file, err := parser.ParseFile(s.fset, filepath.Join(item.Dir, name), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			if doc := typeDoc(decl, obj.Name()); doc != nil {
				return doc, nil
			}
		}
	}
	return nil, nil
}

// typeDoc returns the doc comment of the named type when decl declares it.
func typeDoc(decl ast.Decl, name string) *ast.CommentGroup {
	general, ok := decl.(*ast.GenDecl)
	if !ok || general.Tok != token.TYPE {
		return nil
	}
	for _, spec := range general.Specs {
		spec := spec.(*ast.TypeSpec)
		if spec.Name.Name != name {
			continue
		}
		if spec.Doc != nil {
			return spec.Doc
		}
		if len(general.Specs) == 1 {
			return general.Doc
		}
	}
	return nil
}
