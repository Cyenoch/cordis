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
	"slices"
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

// Target is the platform a package is analyzed for; build constraints decide
// which files, and so which declarations, belong to it. The zero value is
// the platform of the go command.
type Target struct{ GOOS, GOARCH string }

func load(ctx context.Context, dir string, target Target) (*source, error) {
	command := exec.Command("go", "list", "-e", "-deps", "-export",
		"-json=ImportPath,Name,Dir,Export,GoFiles,DepOnly,ImportMap,Error", ".")
	command.Dir = dir
	command.Env = os.Environ()
	if target.GOOS != "" {
		command.Env = append(command.Env, "GOOS="+target.GOOS)
	}
	if target.GOARCH != "" {
		command.Env = append(command.Env, "GOARCH="+target.GOARCH)
	}
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
	s.pkg, _ = config.Check(s.root.ImportPath, s.fset, s.files, s.info)
	if err := s.declarationErrors(); err != nil {
		return nil, err
	}
	return s, nil
}

// declarationErrors reports type errors outside function bodies and
// variable initializers. Code may use names that are only generated later;
// the declarations a contract is read from have to be right as they stand.
func (s *source) declarationErrors() error {
	var bodies [][2]token.Pos
	for _, file := range s.files {
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Body != nil {
					bodies = append(bodies, [2]token.Pos{decl.Body.Pos(), decl.Body.End()})
				}
			case *ast.GenDecl:
				if decl.Tok == token.VAR || decl.Tok == token.CONST {
					bodies = append(bodies, [2]token.Pos{decl.Pos(), decl.End()})
				}
			}
		}
	}
	var failed []error
	for _, err := range s.typeErrors {
		typeError, ok := err.(types.Error)
		tolerated := ok && slices.ContainsFunc(bodies, func(body [2]token.Pos) bool {
			return body[0] <= typeError.Pos && typeError.Pos < body[1]
		})
		if !tolerated && len(failed) < 5 {
			failed = append(failed, s.errorf(typeErrorPos(err), "%s", typeErrorText(err)))
		}
	}
	return errors.Join(failed...)
}

func typeErrorPos(err error) token.Pos {
	if typeError, ok := err.(types.Error); ok {
		return typeError.Pos
	}
	return token.NoPos
}

func typeErrorText(err error) string {
	if typeError, ok := err.(types.Error); ok {
		return typeError.Msg
	}
	return err.Error()
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

// Error is a problem in the analyzed source, at the place it has to be fixed.
// Tools reach the position with errors.As.
type Error struct {
	Position token.Position // Filename is relative to the analyzed directory
	Message  string
}

func (e *Error) Error() string {
	if !e.Position.IsValid() {
		return e.Message
	}
	return e.Position.String() + ": " + e.Message
}

// position resolves pos, with the file name relative to the analyzed
// directory.
func (s *source) position(pos token.Pos) token.Position {
	position := s.fset.Position(pos)
	if relative, err := filepath.Rel(s.root.Dir, position.Filename); err == nil && !strings.HasPrefix(relative, "..") {
		position.Filename = relative
	}
	return position
}

func (s *source) errorf(pos token.Pos, format string, args ...any) error {
	return &Error{Position: s.position(pos), Message: fmt.Sprintf(format, args...)}
}

// within adds what was being read to err. The most specific position wins:
// a field that cannot be described is reported where the field is, not at
// the method that uses its type.
func (s *source) within(pos token.Pos, what string, err error) error {
	if located, ok := errors.AsType[*Error](err); ok {
		return &Error{Position: located.Position, Message: what + ": " + located.Message}
	}
	return s.errorf(pos, "%s: %v", what, err)
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
