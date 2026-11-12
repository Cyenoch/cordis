package gocontract

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Cyenoch/cordis/schema"
)

const prefix = "//cordis:"

// Package is an analyzed Go package: the contract derived from it and what
// is needed to generate its bindings.
type Package struct {
	// Definition is the derived contract in canonical form.
	Definition schema.Definition

	source   *source
	services []*service // every service of the contract, by name
	plugins  []*plugin
}

// service is an interface with a //cordis:service directive.
type service struct {
	object       *types.TypeName
	name         string
	major, minor uint32
	methods      []method
	local        bool // declared in the analyzed package
}

type method struct {
	name     string     // Go method name
	wire     string     // name on the wire
	request  types.Type // nil when the method takes only a context
	response types.Type // nil when the method returns only an error
	stream   bool       // the method returns a sequence of responses
	schemas  [2]shape   // request, response
	position token.Pos
}

// plugin is a function with a //cordis:plugin directive.
type plugin struct {
	id       string
	function *types.Func
	config   types.Type
	schema   shape
	provides []*service
	requires []requirement
	position token.Pos
}

type requirement struct {
	service  *service
	min, max uint32
}

type directive struct {
	verb     string
	args     []string
	position token.Pos
}

// Load analyzes the Go package in dir. It needs the go command and a
// package whose dependencies compile; the package itself may still refer to
// names that have not been generated yet.
func Load(ctx context.Context, dir string, target Target) (*Package, error) {
	src, err := load(ctx, dir, target)
	if err != nil {
		return nil, err
	}
	a := &analysis{source: src, byObject: map[*types.TypeName]*service{}, byName: map[string]*service{}}
	if err := a.run(); err != nil {
		return nil, err
	}
	p := &Package{source: src, plugins: a.plugins}
	definition := schema.Definition{Schema: schema.DefinitionSchema}
	for _, s := range a.byName {
		declared := schema.Service{Name: s.name, Major: s.major, Minor: s.minor}
		for _, m := range s.methods {
			declared.Methods = append(declared.Methods, schema.Method{Name: m.wire, Request: m.schemas[0].raw(), Response: m.schemas[1].raw(), Stream: m.stream})
		}
		definition.Services = append(definition.Services, declared)
		p.services = append(p.services, s)
	}
	for _, plugin := range a.plugins {
		declared := schema.PluginDefinition{ID: plugin.id, Config: plugin.schema.raw()}
		for _, s := range plugin.provides {
			declared.Provides = append(declared.Provides, s.name)
		}
		for _, r := range plugin.requires {
			declared.Requires = append(declared.Requires, schema.ConsumerDeclaration{Name: r.service.object.Name(), Service: r.service.name, MinMinor: r.min, MaxMinor: r.max})
		}
		definition.Definitions = append(definition.Definitions, declared)
	}
	if len(definition.Services) == 0 && len(definition.Definitions) == 0 {
		return nil, fmt.Errorf("%s: no %sservice or %splugin directive", dir, prefix, prefix)
	}
	if p.Definition, err = definition.Normalized(); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	if err := p.current(); err != nil {
		return nil, err
	}
	return p, nil
}

// current checks that the bindings of services declared in other packages
// were generated from the source this analysis read. Those bindings carry
// the schemas the binary validates with; this contract goes to the host.
func (p *Package) current() error {
	for _, declared := range p.Definition.Services {
		var s *service
		for _, candidate := range p.services {
			if candidate.name == declared.Name {
				s = candidate
			}
		}
		if s.local {
			continue
		}
		dir := p.source.packages[s.object.Pkg().Path()].Dir
		stale := fmt.Errorf("%s: the bindings of %s in %s are not current; run cordis generate there first", p.source.root.Dir, s.object.Name(), dir)
		data, err := os.ReadFile(filepath.Join(dir, ContractFile))
		if err != nil {
			return stale
		}
		generated, err := schema.Parse(data)
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Join(dir, ContractFile), err)
		}
		found := false
		for _, other := range generated.Services {
			if other.Name == declared.Name {
				found = other.Major == declared.Major && other.Minor == declared.Minor && other.Consumer(0, 0) == declared.Consumer(0, 0)
			}
		}
		if !found {
			return stale
		}
	}
	return nil
}

type analysis struct {
	source   *source
	byObject map[*types.TypeName]*service
	byName   map[string]*service
	plugins  []*plugin
}

func (a *analysis) run() error {
	type pending struct {
		file       *ast.File
		decl       *ast.FuncDecl
		directives []directive
	}
	var plugins []pending
	for _, file := range a.source.files {
		attached := map[*ast.CommentGroup]bool{}
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				if decl.Tok != token.TYPE {
					continue
				}
				for _, spec := range decl.Specs {
					spec := spec.(*ast.TypeSpec)
					doc := typeDoc(decl, spec.Name.Name)
					found := directives(doc)
					if len(found) == 0 {
						continue
					}
					attached[doc] = true
					object, _ := a.source.info.Defs[spec.Name].(*types.TypeName)
					if _, err := a.service(object, found, true); err != nil {
						return err
					}
				}
			case *ast.FuncDecl:
				if found := directives(decl.Doc); len(found) > 0 {
					attached[decl.Doc] = true
					plugins = append(plugins, pending{file, decl, found})
				}
			}
		}
		for _, group := range file.Comments {
			if found := directives(group); len(found) > 0 && !attached[group] {
				return a.source.errorf(found[0].position, "%s%s must be in the doc comment of an interface or a function", prefix, found[0].verb)
			}
		}
	}
	// Plugins refer to services, wherever in the package those are declared.
	ids := map[string]bool{}
	for _, item := range plugins {
		plugin, err := a.plugin(item.file, item.decl, item.directives)
		if err != nil {
			return err
		}
		if ids[plugin.id] {
			return a.source.errorf(plugin.position, "plugin %q is declared twice", plugin.id)
		}
		ids[plugin.id] = true
		a.plugins = append(a.plugins, plugin)
	}
	return nil
}

func directives(doc *ast.CommentGroup) []directive {
	if doc == nil {
		return nil
	}
	var found []directive
	for _, comment := range doc.List {
		text, ok := strings.CutPrefix(comment.Text, prefix)
		if spaced, isSpaced := strings.CutPrefix(comment.Text, "// cordis:"); isSpaced {
			// Go directives have no space after the slashes; one written
			// with it would otherwise be skipped without a word.
			text, ok = "misspelled "+spaced, true
		}
		if !ok {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) == 0 {
			fields = []string{""}
		}
		found = append(found, directive{verb: fields[0], args: fields[1:], position: comment.Pos()})
	}
	return found
}

// service reads the service an interface declares. local marks interfaces of
// the analyzed package, whose bindings are generated here.
func (a *analysis) service(object *types.TypeName, found []directive, local bool) (*service, error) {
	d := found[0]
	if d.verb == "misspelled" {
		return nil, a.source.errorf(d.position, "write %s%s without a space after the slashes", prefix, strings.Join(d.args, " "))
	}
	if len(found) != 1 || d.verb != "service" {
		return nil, a.source.errorf(d.position, "a type takes exactly one %sservice directive", prefix)
	}
	if len(d.args) != 2 {
		return nil, a.source.errorf(d.position, "usage: %sservice <name> <major>[.<minor>]", prefix)
	}
	if object == nil {
		return nil, a.source.errorf(d.position, "%v", a.source.invalid())
	}
	iface, ok := object.Type().Underlying().(*types.Interface)
	if !ok || object.IsAlias() {
		return nil, a.source.errorf(d.position, "%sservice needs an interface type, %s is not one", prefix, object.Name())
	}
	if named, ok := object.Type().(*types.Named); ok && named.TypeParams().Len() > 0 {
		return nil, a.source.errorf(d.position, "a service interface cannot have type parameters")
	}
	s := &service{object: object, name: d.args[0], local: local}
	var err error
	if s.major, s.minor, err = version(d.args[1]); err != nil {
		return nil, a.source.errorf(d.position, "%v", err)
	}
	if iface.NumMethods() == 0 {
		return nil, a.source.errorf(d.position, "service %s has no methods", s.name)
	}
	for index := range iface.NumMethods() {
		m, err := a.method(iface.Method(index))
		if err != nil {
			return nil, err
		}
		s.methods = append(s.methods, m)
	}
	if other, exists := a.byName[s.name]; exists {
		return nil, a.source.errorf(d.position, "service %s is also declared by %s", s.name, other.object.Name())
	}
	a.byName[s.name], a.byObject[object] = s, s
	return s, nil
}

func version(text string) (major, minor uint32, err error) {
	first, second, hasMinor := strings.Cut(text, ".")
	parse := func(part string) (uint32, error) {
		value, err := strconv.ParseUint(part, 10, 32)
		if err != nil || strconv.FormatUint(value, 10) != part {
			return 0, fmt.Errorf("service version %q must be <major> or <major>.<minor>", text)
		}
		return uint32(value), nil
	}
	if major, err = parse(first); err != nil {
		return 0, 0, err
	}
	if hasMinor {
		if minor, err = parse(second); err != nil {
			return 0, 0, err
		}
	}
	if major == 0 {
		return 0, 0, fmt.Errorf("service major must be positive")
	}
	return major, minor, nil
}

func (a *analysis) method(function *types.Func) (method, error) {
	m := method{name: function.Name(), position: function.Pos()}
	fail := func(format string, args ...any) (method, error) {
		return m, a.source.errorf(function.Pos(), "method %s: %s", function.Name(), fmt.Sprintf(format, args...))
	}
	if !function.Exported() {
		return fail("service methods must be exported")
	}
	first, size := utf8.DecodeRuneInString(m.name)
	m.wire = string(unicode.ToLower(first)) + m.name[size:]
	signature := function.Type().(*types.Signature)
	params, results := signature.Params(), signature.Results()
	if params.Len() == 0 || params.Len() > 2 || signature.Variadic() || !isNamed(params.At(0).Type(), "context", "Context") {
		return fail("want (ctx context.Context) or (ctx context.Context, request T)")
	}
	if params.Len() == 2 {
		m.request = params.At(1).Type()
	}
	isError := func(t types.Type) bool { return types.Identical(t, types.Universe.Lookup("error").Type()) }
	switch {
	case results.Len() == 1 && isError(results.At(0).Type()):
	case results.Len() == 2 && isError(results.At(1).Type()):
		m.response = results.At(0).Type()
	case results.Len() == 1 && isNamed(results.At(0).Type(), "iter", "Seq2"):
		arguments := types.Unalias(results.At(0).Type()).(*types.Named).TypeArgs()
		if !isError(arguments.At(1)) {
			return fail("a streaming method returns iter.Seq2[T, error]")
		}
		m.response, m.stream = arguments.At(0), true
	default:
		return fail("want error, (T, error) or iter.Seq2[T, error] as the result")
	}
	for index, t := range []types.Type{m.request, m.response} {
		m.schemas[index] = emptyObject
		if t == nil {
			continue
		}
		derived, err := a.source.derive(t)
		if err != nil {
			return m, a.source.within(function.Pos(), fmt.Sprintf("method %s: %s %s", function.Name(), []string{"request", "response"}[index], types.TypeString(t, nil)), err)
		}
		m.schemas[index] = derived
	}
	return m, nil
}

func isNamed(t types.Type, pkg, name string) bool {
	named, ok := types.Unalias(t).(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == pkg && named.Obj().Name() == name
}

func (a *analysis) plugin(file *ast.File, decl *ast.FuncDecl, found []directive) (*plugin, error) {
	p := &plugin{position: decl.Pos()}
	for _, d := range found {
		switch d.verb {
		case "plugin":
			if p.id != "" || len(d.args) != 1 {
				return nil, a.source.errorf(d.position, "usage: one %splugin <id> per function", prefix)
			}
			p.id = d.args[0]
		case "provides":
			if len(d.args) == 0 {
				return nil, a.source.errorf(d.position, "usage: %sprovides <interface>...", prefix)
			}
			for _, name := range d.args {
				s, err := a.reference(file, d, name)
				if err != nil {
					return nil, err
				}
				p.provides = append(p.provides, s)
			}
		case "requires":
			if len(d.args) == 0 || len(d.args) > 2 {
				return nil, a.source.errorf(d.position, "usage: %srequires <interface> [<min>-<max>]", prefix)
			}
			s, err := a.reference(file, d, d.args[0])
			if err != nil {
				return nil, err
			}
			r := requirement{service: s, min: s.minor, max: s.minor}
			if len(d.args) == 2 {
				low, high, ok := strings.Cut(d.args[1], "-")
				first, errLow := strconv.ParseUint(low, 10, 32)
				last, errHigh := strconv.ParseUint(high, 10, 32)
				if !ok || errLow != nil || errHigh != nil {
					return nil, a.source.errorf(d.position, "minor range %q must be <min>-<max>", d.args[1])
				}
				r.min, r.max = uint32(first), uint32(last)
				if r.min > s.minor || s.minor > r.max {
					return nil, a.source.errorf(d.position, "minor range %s does not contain %d, the minor %s declares", d.args[1], s.minor, d.args[0])
				}
			}
			p.requires = append(p.requires, r)
		case "misspelled":
			return nil, a.source.errorf(d.position, "write %s%s without a space after the slashes", prefix, strings.Join(d.args, " "))
		default:
			return nil, a.source.errorf(d.position, "unknown directive %s%s; a plugin takes %sprovides and %srequires", prefix, d.verb, prefix, prefix)
		}
	}
	if p.id == "" {
		return nil, a.source.errorf(found[0].position, "%s%s needs a %splugin directive on the same function", prefix, found[0].verb, prefix)
	}
	if name := exported(p.id); !token.IsIdentifier(name) {
		return nil, a.source.errorf(found[0].position, "plugin id %q does not form a Go name", p.id)
	}
	p.function, _ = a.source.info.Defs[decl.Name].(*types.Func)
	if decl.Recv != nil || p.function == nil {
		return nil, a.source.errorf(decl.Pos(), "%splugin needs a package-level function", prefix)
	}
	signature := p.function.Type().(*types.Signature)
	params, results := signature.Params(), signature.Results()
	scope := func(t types.Type) bool {
		pointer, ok := types.Unalias(t).(*types.Pointer)
		return ok && isNamed(pointer.Elem(), kernelPath, "Scope")
	}
	if signature.TypeParams().Len() > 0 || params.Len() != 3 || results.Len() != 1 ||
		!isNamed(params.At(0).Type(), "context", "Context") || !scope(params.At(1).Type()) ||
		!types.Identical(results.At(0).Type(), types.Universe.Lookup("error").Type()) {
		return nil, a.source.errorf(decl.Pos(), "plugin %s: want func(context.Context, *cordis.Scope, Config) error", p.id)
	}
	p.config = params.At(2).Type()
	derived, err := a.source.derive(p.config)
	if err == nil && (derived["type"] != "object" || derived["nullable"] == true || derived["additionalProperties"] != nil) {
		err = fmt.Errorf("a configuration is a struct")
		if _, pointer := types.Unalias(p.config).(*types.Pointer); pointer {
			err = fmt.Errorf("a configuration is a struct, not a pointer to one")
		}
	}
	if err != nil {
		return nil, a.source.within(decl.Pos(), fmt.Sprintf("plugin %s: configuration %s", p.id, types.TypeString(p.config, nil)), err)
	}
	p.schema = derived
	return p, nil
}

// serviceName explains a directive that named a service where its interface
// belongs.
func (a *analysis) serviceName(d directive, s *service) error {
	return a.source.errorf(d.position, "%s is a service name; name its interface, %s", s.name, s.object.Name())
}

// reference resolves an interface named in a directive, X or pkg.X, to the
// service it declares.
func (a *analysis) reference(file *ast.File, d directive, expression string) (*service, error) {
	scope := a.source.pkg.Scope()
	qualifier, name, qualifiedName := strings.Cut(expression, ".")
	if !qualifiedName {
		name = qualifier
	} else {
		scope = nil
		for _, spec := range file.Imports {
			if imported := a.source.info.PkgNameOf(spec); imported != nil && imported.Name() == qualifier {
				scope = imported.Imported().Scope()
			}
		}
		if scope == nil {
			if s, named := a.byName[expression]; named {
				return nil, a.serviceName(d, s)
			}
			return nil, a.source.errorf(d.position, "%s: this file does not import a package named %s", expression, qualifier)
		}
	}
	object, ok := scope.Lookup(name).(*types.TypeName)
	if !ok {
		if s, named := a.byName[expression]; named {
			return nil, a.serviceName(d, s)
		}
		return nil, a.source.errorf(d.position, "%s is not a declared type", expression)
	}
	if s, known := a.byObject[object]; known {
		return s, nil
	}
	if object.Pkg() == a.source.pkg {
		return nil, a.source.errorf(d.position, "%s has no %sservice directive", expression, prefix)
	}
	doc, err := a.source.declaration(object)
	if err != nil {
		return nil, a.source.errorf(d.position, "%s: %v", expression, err)
	}
	found := directives(doc)
	if len(found) == 0 {
		return nil, a.source.errorf(d.position, "%s has no %sservice directive", expression, prefix)
	}
	for _, generated := range []string{"Requirement", "Consumer", "Service"} {
		if object.Pkg().Scope().Lookup(name+generated) == nil {
			return nil, a.source.errorf(d.position, "%s: package %s has no generated bindings; run cordis generate there first", expression, object.Pkg().Path())
		}
	}
	return a.service(object, found, false)
}

// Contract returns the derived contract as written to ContractFile. It fails
// unless schema.Parse, which reads that file back, accepts the result.
func (p *Package) Contract() ([]byte, error) {
	data, err := json.MarshalIndent(p.Definition, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if _, err := schema.Parse(data); err != nil {
		return nil, fmt.Errorf("%s: the generated contract does not parse: %w", filepath.Join(p.source.root.Dir, ContractFile), err)
	}
	return data, nil
}
