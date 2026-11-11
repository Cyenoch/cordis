package gocontract_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/schema/gocontract"
	"github.com/Cyenoch/cordis/schema/gocontract/testdata/clock"
	"github.com/Cyenoch/cordis/schema/gocontract/testdata/echo"
	"github.com/Cyenoch/cordis/schema/gocontract/testdata/shadow"
)

// The committed bindings of the test packages are what the generator writes
// today, and writing them twice gives the same bytes.
func TestGeneratedFilesAreCurrent(t *testing.T) {
	for _, dir := range []string{"testdata/clock", "testdata/echo", "testdata/shadow"} {
		p, err := gocontract.Load(t.Context(), dir, gocontract.Target{})
		if err != nil {
			t.Fatal(err)
		}
		source, err := p.Source()
		if err != nil {
			t.Fatal(err)
		}
		declared, err := p.Contract()
		if err != nil {
			t.Fatal(err)
		}
		for name, want := range map[string][]byte{gocontract.GoFile: source, gocontract.ContractFile: declared} {
			got, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("%s/%s is stale; run cordis generate %s", dir, name, dir)
			}
		}
	}
}

func TestDerivedContract(t *testing.T) {
	p, err := gocontract.Load(t.Context(), "testdata/echo", gocontract.Target{})
	if err != nil {
		t.Fatal(err)
	}
	// The required service of another package is part of the contract.
	names := []string{}
	for _, service := range p.Definition.Services {
		names = append(names, service.Name)
	}
	if strings.Join(names, ",") != "example.clock,example.echo" {
		t.Fatalf("services = %v", names)
	}
	specs, err := p.Definition.Specs()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || len(specs[0].Requires) != 1 {
		t.Fatalf("specs = %+v", specs)
	}
	required := specs[0].Requires[0]
	if required.Requirement != (contract.Requirement{Name: "example.clock", Major: 1, MinMinor: 0, MaxMinor: 2}) || required != clock.ClockConsumer(0, 2) {
		t.Fatalf("requirement = %+v", required)
	}
	for _, method := range p.Definition.Services[1].Methods {
		if method.Name != "say" {
			continue
		}
		const want = `{"additionalProperties":false,"properties":{` +
			`"big":{"format":"int64","type":"string"},` +
			`"code":{"pattern":"^[a-z]{1,3}$","type":"string"},` +
			`"labels":{"additionalProperties":{"type":"string"},"nullable":true,"type":"object"},` +
			`"mode":{"enum":["loud","quiet"],"type":"string"},` +
			`"tags":{"items":{"type":"string"},"maxItems":2,"nullable":true,"type":"array"},` +
			`"text":{"maxLength":16,"minLength":1,"type":"string"},` +
			`"times":{"maximum":3,"minimum":1,"nullable":true,"type":"integer"}},` +
			`"required":["text"],"type":"object"}`
		if string(method.Request) != want {
			t.Fatalf("say request schema:\n got %s\nwant %s", method.Request, want)
		}
		return
	}
	t.Fatal("method say is missing")
}

// remote hides the implementation behind an endpoint, as a process boundary
// does, so the client has to go through JSON.
type remote struct{ contract.Endpoint }

func clients(t *testing.T, service *echo.Service) map[string]echo.Echo {
	t.Helper()
	endpoint := echo.NewEchoEndpoint(service)
	local, err := echo.NewEchoClient(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	crossing, err := echo.NewEchoClient(remote{endpoint})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]echo.Echo{"local": local, "remote": crossing}
}

func collect[T any](sequence iter.Seq2[T, error]) (items []T, err error) {
	for item, itemErr := range sequence {
		if itemErr != nil {
			return items, itemErr
		}
		items = append(items, item)
	}
	return items, nil
}

func TestClientAndEndpoint(t *testing.T) {
	for name, client := range clients(t, &echo.Service{Prefix: "> "}) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			said, err := client.Say(ctx, echo.SayRequest{Text: "hi", Big: 1 << 60, Labels: map[string]string{"any key": "v"}})
			if err != nil || said.Text != "> hi" || said.Length != 4 {
				t.Fatalf("Say = %+v, %v", said, err)
			}
			if err := client.Put(ctx, echo.Line{Line: "abc"}); err != nil {
				t.Fatal(err)
			}
			if total, err := client.Count(ctx); err != nil || total != 3 {
				t.Fatalf("Count = %d, %v", total, err)
			}
			if err := client.Reset(ctx); err != nil {
				t.Fatal(err)
			}
			if total, err := client.Count(ctx); err != nil || total != 0 {
				t.Fatalf("Count after Reset = %d, %v", total, err)
			}
			lines, err := collect(client.Tail(ctx, echo.TailRequest{Count: 3}))
			if err != nil || len(lines) != 3 || lines[2].Line != "2" {
				t.Fatalf("Tail = %v, %v", lines, err)
			}
			if ticks, err := collect(client.Ticks(ctx)); err != nil || len(ticks) != 1 {
				t.Fatalf("Ticks = %v, %v", ticks, err)
			}
		})
	}
}

func TestConstraintsHoldOnBothPaths(t *testing.T) {
	four := 4
	requests := map[string]echo.SayRequest{
		"minLength": {Text: ""},
		"maximum":   {Text: "x", Times: &four},
		"maxItems":  {Text: "x", Tags: []string{"a", "b", "c"}},
		"enum":      {Text: "x", Mode: "silent"},
		"pattern":   {Text: "x", Code: "ABCD"},
	}
	for name, client := range clients(t, &echo.Service{}) {
		for constraint, request := range requests {
			t.Run(name+"/"+constraint, func(t *testing.T) {
				if _, err := client.Say(t.Context(), request); err == nil || !strings.Contains(err.Error(), constraint) {
					t.Fatalf("Say(%+v) = %v, want a %s failure", request, err, constraint)
				}
			})
		}
		t.Run(name+"/negative count", func(t *testing.T) {
			if _, err := collect(client.Tail(t.Context(), echo.TailRequest{Count: -1})); err == nil {
				t.Fatal("Tail accepted a count below the minimum")
			}
		})
		// A value JSON cannot carry exactly fails instead of arriving rounded.
		t.Run(name+"/unsafe integer", func(t *testing.T) {
			client := clients(t, &echo.Service{Total: 1 << 60})[name]
			if _, err := client.Count(t.Context()); err == nil {
				t.Fatal("Count returned an integer beyond the safe range")
			}
		})
	}
}

func TestEndpointRejectsWhatTheContractDoesNotAllow(t *testing.T) {
	endpoint := echo.NewEchoEndpoint(&echo.Service{})
	for name, input := range map[string]string{
		"unknown field": `{"text":"x","extra":1}`,
		"missing field": `{}`,
		"wrong type":    `{"text":1}`,
		"map value":     `{"text":"x","labels":{"k":1}}`,
	} {
		// The reason reaches a caller in another process: only coded errors
		// cross the boundary with their text.
		_, err := endpoint.Call(t.Context(), "say", json.RawMessage(input))
		if coded, ok := errors.AsType[*contract.Error](err); !ok || coded.Code != contract.Invalid || !strings.Contains(coded.Message, "say request") {
			t.Errorf("%s: Call(%s) = %v, want an invalid-request error", name, input, err)
		}
	}
	if _, err := endpoint.Call(t.Context(), "tail", json.RawMessage(`{"count":1}`)); err == nil {
		t.Error("a streaming method answered a plain call")
	}
	if _, err := echo.NewEchoClient(clock.NewClockEndpoint(nil)); err == nil {
		t.Error("a client accepted the endpoint of another service")
	}
}

type fixed time.Time

func (f fixed) Now(context.Context) (time.Time, error) { return time.Time(f), nil }

func TestPluginRunsInProcess(t *testing.T) {
	ctx := t.Context()
	app := cordis.New(cordis.Options{})
	t.Cleanup(func() { app.Close(context.Background()) })

	instance := echo.EchoPlugin.Load(app.Scope(), echo.Config{Prefix: "# "})
	instance.Wait(ctx)
	if state := instance.Status().State; state != cordis.Waiting {
		t.Fatalf("without the clock the plugin is %v, want waiting", state)
	}
	if err := clock.ProvideClock(app.Scope(), fixed(time.Unix(0, 0))); err != nil {
		t.Fatal(err)
	}
	if err := instance.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	client, err := echo.GetEcho(app.Scope())
	if err != nil {
		t.Fatal(err)
	}
	if said, err := client.Say(ctx, echo.SayRequest{Text: "hi"}); err != nil || said.Text != "# hi" {
		t.Fatalf("Say = %+v, %v", said, err)
	}
	// The configuration is checked against its schema before the plugin starts.
	if err := instance.Update(ctx, echo.Config{Prefix: "much too long"}); err == nil || !strings.Contains(err.Error(), "maxLength") {
		t.Fatalf("Update with an invalid configuration = %v", err)
	}
	spec := echo.EchoDefinitionSpec()
	if spec.ID != "echo" || len(spec.Provides) != 1 || spec.Provides[0] != echo.EchoService() || spec.Requires[0] != clock.ClockConsumer(0, 2) {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestRejectedSources(t *testing.T) {
	const header = "package bad\n\nimport (\n\t\"context\"\n\t\"iter\"\n\t\"time\"\n\n\t\"github.com/Cyenoch/cordis\"\n)\n\nvar _ context.Context\nvar _ iter.Seq[int]\nvar _ time.Time\nvar _ *cordis.Scope\n\n"
	service := func(methods string) string {
		return "//cordis:service example.bad 1\ntype Bad interface {\n" + methods + "\n}\n"
	}
	request := func(fields string) string {
		return service("\tDo(ctx context.Context, request Request) error") + "type Request struct {\n" + fields + "\n}\n"
	}
	cases := map[string]struct{ source, want string }{
		"map key":              {request("\tItems map[int]string `json:\"items\"`"), "field Items: map keys must be strings"},
		"map configuration":    {"//cordis:plugin bad\nfunc start(context.Context, *cordis.Scope, map[string]string) error { return nil }\n", "a configuration is a struct"},
		"interface field":      {request("\tValue any `json:\"value\"`"), "field Value: interface types"},
		"custom encoding":      {request("\tAt time.Duration `json:\"at\"`\n\tWhen Stamp `json:\"when\"`") + "type Stamp struct{}\nfunc (Stamp) MarshalJSON() ([]byte, error) { return nil, nil }\n", "has its own JSON encoding"},
		"recursive type":       {request("\tNext *Request `json:\"next\"`"), "is recursive"},
		"duplicate JSON name":  {request("\tA string `json:\"x\"`\n\tB string `json:\"x\"`"), `JSON name "x" is used twice`},
		"misplaced constraint": {request("\tA int `json:\"a\" cordis:\"minLength=1\"`"), "minLength does not apply to a integer"},
		"unknown constraint":   {request("\tA int `json:\"a\" cordis:\"min=1\"`"), `unknown constraint "min"`},
		"quoted non-integer":   {request("\tA string `json:\"a,string\"`"), `"string" option is only supported`},
		"impossible bounds":    {request("\tA int `json:\"a\" cordis:\"minimum=5,maximum=1\"`"), "minimum exceeds maximum"},
		"no context":           {service("\tDo(request string) error"), "want (ctx context.Context)"},
		"two requests":         {service("\tDo(ctx context.Context, a, b string) error"), "want (ctx context.Context)"},
		"no error":             {service("\tDo(ctx context.Context) string"), "want error, (T, error) or iter.Seq2[T, error]"},
		"bad sequence":         {service("\tDo(ctx context.Context) iter.Seq2[string, int]"), "a streaming method returns iter.Seq2[T, error]"},
		"empty service":        {service(""), "has no methods"},
		"bad version":          {"//cordis:service example.bad 1.x\ntype Bad interface{ Do(ctx context.Context) error }\n", "service version"},
		"not an interface":     {"//cordis:service example.bad 1\ntype Bad struct{}\n", "needs an interface type"},
		"unknown directive":    {"//cordis:plugin bad\n//cordis:needs Bad\nfunc start(context.Context, *cordis.Scope, struct{}) error { return nil }\n", "unknown directive //cordis:needs"},
		"detached directive":   {"//cordis:service example.bad 1\n\ntype Bad interface{ Do(ctx context.Context) error }\n", "must be in the doc comment"},
		"plugin signature":     {"//cordis:plugin bad\nfunc start(context.Context, struct{}) error { return nil }\n", "want func(context.Context, *cordis.Scope, Config) error"},
		"scalar configuration": {"//cordis:plugin bad\nfunc start(context.Context, *cordis.Scope, string) error { return nil }\n", "a configuration is a struct"},
		"undeclared service":   {"//cordis:plugin bad\n//cordis:provides Missing\nfunc start(context.Context, *cordis.Scope, struct{}) error { return nil }\n", "Missing is not a declared type"},
		"plain interface":      {"type Plain interface{}\n//cordis:plugin bad\n//cordis:provides Plain\nfunc start(context.Context, *cordis.Scope, struct{}) error { return nil }\n", "Plain has no //cordis:service directive"},
		"range excludes minor": {service("\tDo(ctx context.Context) error") + "//cordis:plugin bad\n//cordis:requires Bad 1-2\nfunc start(context.Context, *cordis.Scope, struct{}) error { return nil }\n", "minor range 1-2 does not contain 0"},
		"generated name taken": {service("\tDo(ctx context.Context) error") + "func GetBad() {}\n", "GetBad is declared here and is also a generated name"},
		"nothing declared":     {"type Plain struct{}\n", "no //cordis:service or //cordis:plugin directive"},
		"does not compile":     {request("\tA Missing `json:\"a\"`"), "undefined: Missing"},
		"misspelled embedding": {"type Extra interface{ More(ctx context.Context) error }\n" + service("\tExtraa\n\tDo(ctx context.Context) error"), "undefined: Extraa"},
		"spaced directive":     {"// cordis:service example.bad 1\ntype Bad interface{ Do(ctx context.Context) error }\n", "without a space"},
		"encoded bytes":        {request("\tA []Octet `json:\"a\"`") + "type Octet uint8\nfunc (Octet) MarshalJSON() ([]byte, error) { return nil, nil }\n", "has its own JSON encoding"},
		"encoded embedding":    {request("\tA struct{ time.Time; X int } `json:\"a\"`"), "has its own JSON encoding through an embedded field"},
		"enum of numbers":      {request("\tA int `json:\"a\" cordis:\"enum=x|y\"`"), `enum: "x" is not a number`},
		"minimum not a number": {request("\tA int `json:\"a\" cordis:\"minimum=abc\"`"), `minimum: "abc" is not a number`},
		"quoted JSON name":     {request("\tA string `json:\"it's\"`"), `JSON name "it's"`},
		"plugin id":            {"//cordis:plugin 9lives\nfunc start(context.Context, *cordis.Scope, struct{}) error { return nil }\n", `plugin id "9lives" does not form a Go name`},
		"twice promoted":       {request("\tLeft\n\tRight") + "type Left struct{ X int `json:\"x\"` }\ntype Right struct{ X int `json:\"x\"` }\n", `JSON name "x" is promoted from two embedded structs`},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir, err := os.MkdirTemp("testdata", "rejected-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(header+tt.source), 0644); err != nil {
				t.Fatal(err)
			}
			_, err = gocontract.Generate(t.Context(), dir, gocontract.Target{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Generate = %v, want an error containing %q", err, tt.want)
			}
			// Problems in the source say where: a tool can take it from there.
			if located, ok := errors.AsType[*gocontract.Error](err); tt.want != "no //cordis:service or //cordis:plugin directive" && (!ok || located.Position.Filename != "bad.go" || located.Position.Line == 0) {
				t.Errorf("no source position in %v", err)
			}
			// A rejected source leaves no partial output behind.
			for _, name := range []string{gocontract.GoFile, gocontract.ContractFile} {
				if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					t.Errorf("%s was written: %v", name, err)
				}
			}
		})
	}
}

// Names and shapes generated code could trip over still give bindings that
// compile and carry values as encoding/json does.
func TestAwkwardDeclarations(t *testing.T) {
	a := "a"
	mode, name, err := shadow.RoundTrip(t.Context(), &a, "outer")
	if err != nil || mode == nil || *mode != "a" || name != "outer" {
		t.Fatalf("RoundTrip = %v, %q, %v", mode, name, err)
	}
	// A nil pointer is null on the wire, which a constrained field allows.
	if mode, _, err := shadow.RoundTrip(t.Context(), nil, "outer"); err != nil || mode != nil {
		t.Fatalf("RoundTrip with a nil pointer = %v, %v", mode, err)
	}
	b := "other"
	if _, _, err := shadow.RoundTrip(t.Context(), &b, "outer"); err == nil || !strings.Contains(err.Error(), "enum") {
		t.Fatalf("a value outside the enum: %v", err)
	}
	// The constraint of the field that hides a promoted one applies.
	if _, _, err := shadow.RoundTrip(t.Context(), nil, ""); err == nil || !strings.Contains(err.Error(), "minLength") {
		t.Fatalf("an empty name: %v", err)
	}
}

// A service of another package is used through that package's bindings, so
// those have to match its source.
func TestStaleBindingsOfAnImportedService(t *testing.T) {
	root, err := os.MkdirTemp("testdata", "stale-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	api, plug := filepath.Join(root, "api"), filepath.Join(root, "plug")
	write := func(path, source string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	const service = "package api\n\nimport \"context\"\n\n//cordis:service example.stale 1\ntype Clock interface {\n\tNow(ctx context.Context, request Request) error\n}\n\n"
	write(filepath.Join(api, "api.go"), service+"type Request struct{}\n")
	write(filepath.Join(plug, "plug.go"), "package plug\n\nimport (\n\t\"context\"\n\n\t\"github.com/Cyenoch/cordis\"\n\tclk \"github.com/Cyenoch/cordis/schema/gocontract/"+filepath.ToSlash(api)+"\"\n)\n\nvar _ clk.Clock\n\n//cordis:plugin plug\n//cordis:requires clk.Clock\nfunc start(context.Context, *cordis.Scope, struct{}) error { return nil }\n")
	if _, err := gocontract.Generate(t.Context(), plug, gocontract.Target{}); err == nil || !strings.Contains(err.Error(), "run cordis generate there first") {
		t.Fatalf("before the service was generated: %v", err)
	}
	if _, err := gocontract.Generate(t.Context(), api, gocontract.Target{}); err != nil {
		t.Fatal(err)
	}
	if _, err := gocontract.Generate(t.Context(), plug, gocontract.Target{}); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(api, "api.go"), service+"type Request struct{ Zone string `json:\"zone\"` }\n")
	if _, err := gocontract.Generate(t.Context(), plug, gocontract.Target{}); err == nil || !strings.Contains(err.Error(), "are not current") {
		t.Fatalf("after the service changed without being generated again: %v", err)
	}
}

// Build constraints decide which declarations a target has.
func TestContractOfTheBuildTarget(t *testing.T) {
	dir, err := os.MkdirTemp("testdata", "target-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	files := map[string]string{
		"plugin.go":         "package target\n\nimport (\n\t\"context\"\n\n\t\"github.com/Cyenoch/cordis\"\n)\n\n//cordis:plugin target\nfunc start(context.Context, *cordis.Scope, Config) error { return nil }\n",
		"config_windows.go": "package target\n\ntype Config struct{ Pipe string `json:\"pipe\"` }\n",
		"config_other.go":   "//go:build !windows\n\npackage target\n\ntype Config struct{ Socket string `json:\"socket\"` }\n",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for goos, field := range map[string]string{"windows": "pipe", "linux": "socket"} {
		p, err := gocontract.Load(t.Context(), dir, gocontract.Target{GOOS: goos, GOARCH: "amd64"})
		if err != nil {
			t.Fatal(err)
		}
		if config := string(p.Definition.Definitions[0].Config); !strings.Contains(config, `"`+field+`"`) {
			t.Errorf("%s: configuration schema %s lacks %q", goos, config, field)
		}
	}
}

// Code that already uses the generated names does not compile before they
// exist; the analysis reads past that.
func TestGeneratesForSourceThatUsesGeneratedNames(t *testing.T) {
	dir, err := os.MkdirTemp("testdata", "first-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	const source = `package first

import (
	"context"

	"github.com/Cyenoch/cordis"
)

//cordis:service example.first 2.1
type First interface {
	Do(ctx context.Context) error
}

type impl struct{}

func (impl) Do(context.Context) error { return nil }

//cordis:plugin first
//cordis:provides First
func start(_ context.Context, s *cordis.Scope, _ struct{}) error {
	return ProvideFirst(s, impl{})
}

var Plugin = FirstPlugin
`
	if err := os.WriteFile(filepath.Join(dir, "first.go"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := gocontract.Generate(t.Context(), dir, gocontract.Target{})
	if err != nil {
		t.Fatal(err)
	}
	if service := p.Definition.Services[0]; service.Major != 2 || service.Minor != 1 {
		t.Fatalf("version = %d.%d", service.Major, service.Minor)
	}
	// Generating again, now with the generated file present, changes nothing.
	before, err := os.ReadFile(filepath.Join(dir, gocontract.GoFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gocontract.Generate(t.Context(), dir, gocontract.Target{}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(dir, gocontract.GoFile))
	if err != nil || string(before) != string(after) {
		t.Fatalf("second generation changed the output: %v", err)
	}
}
