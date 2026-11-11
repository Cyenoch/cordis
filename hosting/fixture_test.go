package hosting

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
	"github.com/Cyenoch/cordis/runtime/memory"
	"github.com/Cyenoch/cordis/sdk"
)

// fixture is a host whose packages run in memory through the real SDK.
type fixture struct {
	t       *testing.T
	host    *Host
	runtime *memory.Runtime
	trust   packagehost.TrustPolicy
	log     *journal

	directory string
	archives  []string // packed, not yet installed
}

// journal records what the test plugins observe.
type journal struct {
	mu   sync.Mutex
	list []string
}

func (j *journal) add(event string) {
	j.mu.Lock()
	j.list = append(j.list, event)
	j.mu.Unlock()
}

func (j *journal) get() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.list)
}

func (j *journal) count(event string) int {
	n := 0
	for _, got := range j.get() {
		if got == event {
			n++
		}
	}
	return n
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{t: t, runtime: memory.New(), log: &journal{}, directory: t.TempDir(),
		trust: packagehost.TrustPolicy{DevDigests: map[string]bool{}, Publishers: map[string][]string{}}}
}

// open opens the host and installs and selects what was packed. Packages
// are packed first because the trust policy is fixed when the host opens.
func (f *fixture) open(options ...func(*Options)) *fixture {
	f.t.Helper()
	opened := Options{FrameBytes: contract.MaxFrameBytes, Directory: f.directory, Trust: f.trust, Runtime: f.runtime}
	for _, option := range options {
		option(&opened)
	}
	host, err := Open(opened)
	must(f.t, err)
	f.host = host
	f.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		host.Close(ctx)
	})
	for _, archive := range f.archives {
		installed, err := host.Install(f.t.Context(), archive)
		must(f.t, err)
		must(f.t, host.Select(f.t.Context(), installed.Ref))
	}
	f.archives = nil
	return f
}

// pack builds a package whose definitions run in memory. The archive holds
// a placeholder artifact.
func (f *fixture) pack(manifest contract.Manifest, definitions ...sdk.Definition) contract.PackageRef {
	f.t.Helper()
	manifest.Schema = contract.PackageSchema
	if manifest.Version == "" {
		manifest.Version = "1.0.0"
	}
	manifest.Publisher = "test.publisher"
	if manifest.Protocol.Major == 0 {
		manifest.Protocol = contract.Protocol{Major: contract.ProtocolMajor}
	}
	for n := range manifest.Definitions {
		if manifest.Definitions[n].ConfigSchema == nil {
			manifest.Definitions[n].ConfigSchema = json.RawMessage(`{"type":"object"}`)
		}
		if manifest.Definitions[n].Requires == nil {
			manifest.Definitions[n].Requires = []contract.Consumer{}
		}
		if manifest.Definitions[n].Provides == nil {
			manifest.Definitions[n].Provides = []contract.Service{}
		}
	}
	if manifest.DefaultDefinition == "" {
		manifest.DefaultDefinition = manifest.Definitions[0].ID
	}
	manifest.Dependencies, manifest.OptionalDependencies = []contract.Dependency{}, []contract.Dependency{}
	manifest.Runtime = contract.RuntimeSpec{Kind: "native", Artifacts: []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: "plugin"}}}
	source := f.t.TempDir()
	raw, err := json.Marshal(manifest)
	must(f.t, err)
	must(f.t, os.WriteFile(filepath.Join(source, "plugin.json"), raw, 0o600))
	must(f.t, os.WriteFile(filepath.Join(source, "plugin"), []byte("in-memory artifact "+manifest.ID+manifest.Version), 0o600))
	archive := filepath.Join(f.t.TempDir(), manifest.ID+".cordis")
	pkg, err := packagehost.Pack(f.t.Context(), source, archive)
	must(f.t, err)
	f.trust.DevDigests[pkg.Ref.Digest] = true
	if !slices.Contains(f.trust.Publishers["test.publisher"], manifest.ID) {
		f.trust.Publishers["test.publisher"] = append(f.trust.Publishers["test.publisher"], manifest.ID)
	}
	f.archives = append(f.archives, archive)
	f.runtime.Register(manifest.ID, definitions...)
	return pkg.Ref
}

func (f *fixture) enable(entry contract.Entry) contract.Instance {
	f.t.Helper()
	instance, err := f.host.Enable(f.ctx(), entry)
	must(f.t, err)
	return instance
}

func (f *fixture) ctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	f.t.Cleanup(cancel)
	return ctx
}

func (f *fixture) state(id string) contract.State {
	f.t.Helper()
	status, err := f.host.Status(id)
	must(f.t, err)
	return status.State
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// Test contracts shared by the plugins below.

type echoRequest struct {
	Text string `json:"text"`
}

type echoResponse struct {
	Text string `json:"text"`
}

var (
	echoService = contract.Service{Name: "test.echo", Major: 1, Minor: 1}
	echoKey     = contract.EndpointKey("test.echo", 1)
)

func echoRequirement(minMinor, maxMinor uint32) contract.Consumer {
	return contract.Consumer{Requirement: contract.Requirement{Name: "test.echo", Major: 1, MinMinor: minMinor, MaxMinor: maxMinor}, Contract: strings.Repeat("ab", 32)}
}

// echoPlugin provides test.echo and answers with a prefix.
func (f *fixture) echoPlugin(name, prefix string) cordis.Plugin[struct{}] {
	return cordis.Plugin[struct{}]{Name: name, Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		f.log.add("start " + name)
		s.Defer(func(context.Context) error {
			f.log.add("stop " + name)
			return nil
		})
		return echoKey.Provide(s, contract.Methods(echoService, map[string]contract.Handler{
			"echo": contract.Typed(func(_ context.Context, in echoRequest) (echoResponse, error) {
				return echoResponse{Text: prefix + in.Text}, nil
			}),
		}))
	}}
}

// userPlugin requires test.echo, calls it when it starts and when it stops.
func (f *fixture) userPlugin(name string) cordis.Plugin[struct{}] {
	return cordis.Plugin[struct{}]{Name: name, Requires: []cordis.Requirement{contract.Require(echoKey, 0, 9)},
		Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
			call := func(ctx context.Context, text string) {
				provider, err := echoKey.Get(s)
				if err != nil {
					f.log.add(name + " get: " + err.Error())
					return
				}
				out, err := contract.Invoke[echoRequest, echoResponse](ctx, provider, "echo", echoRequest{Text: text})
				if err != nil {
					f.log.add(name + " call: " + err.Error())
					return
				}
				f.log.add(name + " got " + out.Text)
			}
			call(ctx, "start")
			s.Defer(func(ctx context.Context) error {
				call(ctx, "cleanup")
				return nil
			})
			return nil
		}}
}

func echoManifest(id string, provides bool, requires ...contract.Consumer) contract.Manifest {
	definition := contract.DefinitionSpec{ID: "main", Requires: requires}
	if provides {
		definition.Provides = []contract.Service{echoService}
	}
	return contract.Manifest{ID: id, Definitions: []contract.DefinitionSpec{definition}}
}
