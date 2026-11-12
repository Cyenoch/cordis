// Package loader builds a tree of plugin instances from a configuration
// file, lets the running application edit that tree, and writes the edits
// back to the file.
package loader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

// ErrConflict reports a second registration under one name.
var ErrConflict = errors.New("loader: already registered")

// Loadable is something an entry can run: a registered plugin, or whatever a
// Resolver returns.
type Loadable interface {
	// Load decodes config and starts an instance on s. A decoding failure is
	// returned; when it wraps a *contract.PathError the loader points at the
	// offending key in the file.
	Load(s *cordis.Scope, config json.RawMessage) (Handle, error)
}

// Handle controls an instance started by a Loadable.
type Handle interface {
	Status() cordis.Status
	Wait(ctx context.Context) error
	// Update validates and applies a new configuration.
	Update(ctx context.Context, config json.RawMessage) error
	Stop(ctx context.Context) error
}

// Resolver maps the plugin name of an entry to a Loadable.
type Resolver interface {
	Resolve(name string) (Loadable, bool)
}

// Registry is the set of plugins and settings an application makes
// available to its configuration file. The application builds it explicitly;
// there is no global registry. The zero value is an empty registry. Register
// everything before the loader starts: a Registry is not safe for concurrent
// modification.
type Registry struct {
	plugins  map[string]Loadable
	settings map[string]func(*cordis.Scope, json.RawMessage) (*cordis.Scope, error)
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins:  map[string]Loadable{},
		settings: map[string]func(*cordis.Scope, json.RawMessage) (*cordis.Scope, error){},
	}
}

// Resolve implements Resolver.
func (r *Registry) Resolve(name string) (Loadable, bool) {
	l, ok := r.plugins[name]
	return l, ok
}

// Option adjusts how a plugin is registered.
type Option[C any] func(*registered[C])

// Default sets the configuration an entry's config is decoded onto, so that
// keys the entry omits keep these values. It takes a function because every
// entry needs its own copy: maps and slices in a shared value would be
// modified by decoding.
func Default[C any](make func() C) Option[C] {
	return func(r *registered[C]) { r.defaults = make }
}

// Register makes plugin available to entries as name. An entry's config is
// strictly decoded as JSON onto the default configuration: unknown keys and
// mismatched types are errors.
func Register[C any](r *Registry, name string, plugin cordis.Plugin[C], options ...Option[C]) error {
	if name == "" {
		return errors.New("loader: plugin name is required")
	}
	if _, exists := r.plugins[name]; exists {
		return fmt.Errorf("register plugin %q: %w", name, ErrConflict)
	}
	if r.plugins == nil {
		r.plugins = map[string]Loadable{}
	}
	entry := &registered[C]{plugin: plugin}
	for _, option := range options {
		option(entry)
	}
	r.plugins[name] = entry
	return nil
}

// RegisterSetting lets entries set key through the "settings" of the service
// it belongs to.
func RegisterSetting[O any](r *Registry, key cordis.Setting[O]) error {
	service := key.Service()
	if _, exists := r.settings[service]; exists {
		return fmt.Errorf("register setting for %q: %w", service, ErrConflict)
	}
	if r.settings == nil {
		r.settings = map[string]func(*cordis.Scope, json.RawMessage) (*cordis.Scope, error){}
	}
	r.settings[service] = func(s *cordis.Scope, raw json.RawMessage) (*cordis.Scope, error) {
		var value O
		if err := contract.Decode(raw, &value); err != nil {
			return nil, err
		}
		return key.With(s, value), nil
	}
	return nil
}

type registered[C any] struct {
	plugin   cordis.Plugin[C]
	defaults func() C
}

func (r *registered[C]) decode(raw json.RawMessage) (C, error) {
	var config C
	if r.defaults != nil {
		config = r.defaults()
	}
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	err := contract.Decode(raw, &config)
	return config, err
}

// Load decodes raw over the defaults, empty as {}, and loads the plugin on s.
func (r *registered[C]) Load(s *cordis.Scope, raw json.RawMessage) (Handle, error) {
	config, err := r.decode(raw)
	if err != nil {
		return nil, err
	}
	return &handle[C]{registered: r, instance: r.plugin.Load(s, config)}, nil
}

type handle[C any] struct {
	registered *registered[C]
	instance   *cordis.Instance[C]
}

// Status is the instance's Status.
func (h *handle[C]) Status() cordis.Status { return h.instance.Status() }

// Wait is the instance's Wait.
func (h *handle[C]) Wait(ctx context.Context) error { return h.instance.Wait(ctx) }

// Stop is the instance's Stop.
func (h *handle[C]) Stop(ctx context.Context) error { return h.instance.Stop(ctx) }

// Update decodes raw as Load does and applies it to the instance.
func (h *handle[C]) Update(ctx context.Context, raw json.RawMessage) error {
	config, err := h.registered.decode(raw)
	if err != nil {
		return err
	}
	return h.instance.Update(ctx, config)
}
