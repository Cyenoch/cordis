// Package plugintest runs a plugin definition the way a host runs it,
// without building a package: the real SDK runtime talks to a real Host
// over an in-memory session. The plugin's Start, its codecs, its
// requirements and its declared services are all exercised.
package plugintest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/hosting"
	"github.com/Cyenoch/cordis/packagehost"
	"github.com/Cyenoch/cordis/runtime/memory"
	"github.com/Cyenoch/cordis/sdk"
)

// Options configures a harness.
type Options struct {
	// Config is the entry's configuration; empty means {}.
	Config json.RawMessage
	// Setup runs on the host's root scope before the plugin starts. Use it
	// to load the in-process plugins that provide what the plugin requires.
	Setup func(root *cordis.Scope) error
	// Children are further definitions of the same package, for plugins
	// that create child instances.
	Children []Child
}

// Child is a definition a plugin may create as a child instance.
type Child struct {
	Spec       contract.DefinitionSpec
	Definition sdk.Definition
}

// Harness is one host running one entry of the plugin under test.
type Harness struct {
	host      *hosting.Host
	instance  contract.Instance
	directory string
}

// Start runs definition as an entry whose ID is spec.ID and waits until it
// is Active. spec is what the generated <Name>DefinitionSpec returns. When
// the plugin does not become Active, Start returns why and cleans up; one
// still waiting for a requirement after ctx's deadline, or 30 seconds when
// ctx has none, is reported with what it waits for.
// Always Close a harness that Start returned.
func Start(ctx context.Context, spec contract.DefinitionSpec, definition sdk.Definition, options Options) (h *Harness, err error) {
	directory, err := os.MkdirTemp("", "cordis-plugintest-")
	if err != nil {
		return nil, err
	}
	h = &Harness{directory: directory}
	defer func() {
		if err != nil {
			err = errors.Join(err, h.Close(context.WithoutCancel(ctx)))
			h = nil
		}
	}()
	manifest := contract.Manifest{Schema: contract.PackageSchema, ID: "plugintest.plugin", Version: "0.0.0", Publisher: "plugintest",
		Protocol: contract.Protocol{Major: contract.ProtocolMajor}, DefaultDefinition: spec.ID,
		Definitions: []contract.DefinitionSpec{spec}, Dependencies: []contract.Dependency{}, OptionalDependencies: []contract.Dependency{},
		Runtime: contract.RuntimeSpec{Kind: "native", Artifacts: []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: "plugin"}}}}
	definitions := []sdk.Definition{definition}
	for _, child := range options.Children {
		manifest.Definitions = append(manifest.Definitions, child.Spec)
		definitions = append(definitions, child.Definition)
	}
	source := filepath.Join(directory, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		return h, err
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return h, err
	}
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), raw, 0o600); err != nil {
		return h, err
	}
	if err := os.WriteFile(filepath.Join(source, "plugin"), []byte("runs in memory"), 0o600); err != nil {
		return h, err
	}
	archive := filepath.Join(directory, "plugin.zip")
	packed, err := packagehost.Pack(ctx, source, archive)
	if err != nil {
		return h, err
	}
	plugins := memory.New()
	plugins.Register(manifest.ID, definitions...)
	h.host, err = hosting.Open(hosting.Options{Directory: filepath.Join(directory, "store"), Runtime: plugins,
		Trust: packagehost.TrustPolicy{Digests: map[string]bool{packed.Ref.Digest: true},
			Publishers: map[string][]string{manifest.Publisher: {manifest.ID}}}})
	if err != nil {
		return h, err
	}
	installed, err := h.host.Install(ctx, archive)
	if err != nil {
		return h, err
	}
	if err := h.host.Select(ctx, installed.Ref); err != nil {
		return h, err
	}
	if options.Setup != nil {
		if err := options.Setup(h.host.Scope()); err != nil {
			return h, err
		}
	}
	h.instance, err = h.host.Enable(ctx, contract.Entry{ID: spec.ID, Package: manifest.ID, Definition: spec.ID, Config: options.Config})
	if err != nil {
		return h, err
	}
	// A requirement nothing provides keeps the entry waiting for good. Told
	// what it waits for, a test fails with that instead of hanging until go
	// test gives up.
	wait, cancel := ctx, context.CancelFunc(func() {})
	if _, bounded := ctx.Deadline(); !bounded {
		wait, cancel = context.WithTimeout(ctx, startLimit)
	}
	defer cancel()
	if err := h.instance.Wait(wait); err != nil {
		if status := h.instance.Status(); status.State == contract.Waiting {
			return h, fmt.Errorf("definition %q is still waiting for %s; provide it in Options.Setup: %w", spec.ID, waitingFor(status.Waiting), err)
		}
		return h, fmt.Errorf("start definition %q: %w", spec.ID, err)
	}
	if status := h.instance.Status(); status.State != contract.Active {
		return h, fmt.Errorf("definition %q is %s, waiting for %+v", spec.ID, status.State, status.Waiting)
	}
	return h, nil
}

// startLimit bounds how long Start waits when ctx has no deadline.
const startLimit = 30 * time.Second

func waitingFor(reasons []contract.WaitReason) string {
	names := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if r.Requirement == nil {
			names = append(names, r.Reason)
			continue
		}
		names = append(names, fmt.Sprintf("%s@%d.%d-%d (%s)", r.Requirement.Name, r.Requirement.Major, r.Requirement.MinMinor, r.Requirement.MaxMinor, r.Reason))
	}
	return strings.Join(names, ", ")
}

// Scope is the root scope of the host. Get the plugin's endpoints from it,
// emit the events it listens to, and load more in-process plugins on it.
func (h *Harness) Scope() *cordis.Scope { return h.host.Scope() }

// Host is the host that runs the plugin, for tests of updates, children
// and status.
func (h *Harness) Host() *hosting.Host { return h.host }

// Instance is the entry of the plugin under test.
func (h *Harness) Instance() contract.Instance { return h.instance }

// Close stops the plugin, running its cleanups, and removes the harness's
// files. It returns the errors of the cleanups.
func (h *Harness) Close(ctx context.Context) error {
	var err error
	if h.host != nil {
		err = h.host.Close(ctx)
	}
	return errors.Join(err, os.RemoveAll(h.directory))
}
