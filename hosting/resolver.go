package hosting

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/loader"
)

// Resolver lets a configuration loader run installed native packages: an
// entry whose plugin is "package:<id>" runs the default definition of the
// selected version of that package, and "package:<id>#<definition>" another
// one. Such an entry is not saved in the host's catalog; the loader's file
// is what describes it. The host lists it among its instances with the
// origin "dynamic", and its package cannot be uninstalled or replaced while
// it runs.
func (h *Host) Resolver() loader.Resolver { return packages{host: h} }

type packages struct{ host *Host }

// Resolve accepts "package:<id>" and "package:<id>#<definition>".
func (p packages) Resolve(name string) (loader.Loadable, bool) {
	rest, ok := strings.CutPrefix(name, "package:")
	if !ok || rest == "" {
		return nil, false
	}
	id, definition, _ := strings.Cut(rest, "#")
	return packageDefinition{host: p.host, id: id, definition: definition}, true
}

type packageDefinition struct {
	host           *Host
	id, definition string
}

// Load runs the definition as the dynamic entry "loader/<owner>"; its record
// goes when s stops.
func (p packageDefinition) Load(s *cordis.Scope, config json.RawMessage) (loader.Handle, error) {
	owner, _ := loader.EntryOf(s)
	entry := contract.Entry{ID: "loader/" + owner, Parent: "loader", Package: p.id, Definition: p.definition, Config: config}
	instance, err := p.host.enableEntry(context.Background(), entry, nil, s)
	if err != nil {
		return nil, err
	}
	handle := loaded{instance.(*hostInstance)}
	// The kernel stops the entry with the scope that loaded it; the host's
	// record goes with it.
	s.Defer(func(context.Context) error {
		handle.forget()
		return nil
	})
	return handle, nil
}

// loaded adapts an entry to the handle a loader controls it with.
type loaded struct{ instance *hostInstance }

// Status is the kernel status of the entry.
func (l loaded) Status() cordis.Status { return l.instance.instance.Status() }

// Wait is the kernel instance's Wait.
func (l loaded) Wait(ctx context.Context) error { return l.instance.instance.Wait(ctx) }

// Update validates config against the definition, empty as {}, and applies it.
func (l loaded) Update(ctx context.Context, config json.RawMessage) error {
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	if err := validConfig(l.instance.spec, config); err != nil {
		return err
	}
	return l.instance.instance.Update(ctx, config)
}

// Stop stops the entry; once it is stopped the host drops its record.
func (l loaded) Stop(ctx context.Context) error {
	err := l.instance.instance.Stop(ctx)
	if l.instance.gone() {
		l.forget()
	}
	return err
}

func (l loaded) forget() {
	h := l.instance.host
	h.mu.Lock()
	if child, exists := h.dynamic[l.instance.id]; exists && child.instance == l.instance {
		delete(h.dynamic, l.instance.id)
	}
	h.mu.Unlock()
}
