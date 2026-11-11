// Package sdk runs kernel plugins as a native plugin process.
//
// The process hosts its own kernel. The services the host bound to the
// definition's requirements are available at the root scope before the
// plugin starts, and the endpoints the definition declares are registered
// with the host once it has started. Everything else the plugin provides
// stays inside the process.
//
// What a plugin asks of the host while its Start runs is part of starting
// it and ends when Start returns. Work that goes on after that belongs in
// Scope.Go: its requests wait until the host has taken the plugin's start.
// A stream of a required service cannot be consumed inside Start.
package sdk

import (
	"context"
	"encoding/json"
	"io"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

// Definition is a plugin that a package offers under an identifier.
type Definition struct {
	ID   string
	load func(s *cordis.Scope, config json.RawMessage) (running, error)
}

// running is the loaded plugin, whatever its configuration type.
type running interface {
	Info() cordis.Info
	Status() cordis.Status
	Wait(context.Context) error
}

// Define makes plugin available as the definition named plugin.Name. The
// configuration the host sends is strictly decoded into C; the plugin's own
// Validate runs after that.
func Define[C any](plugin cordis.Plugin[C]) Definition {
	start := plugin.Start
	if start != nil {
		plugin.Start = func(ctx context.Context, s *cordis.Scope, config C) error {
			err := start(ctx, s, config)
			// What the plugin asks of the host from here on is no longer
			// part of starting it. After a failed start its cleanups still
			// are: the host has not been answered yet.
			if g, ok := guestOf(s); ok && err == nil {
				g.started()
			}
			return err
		}
	}
	return Definition{ID: plugin.Name, load: func(s *cordis.Scope, raw json.RawMessage) (running, error) {
		var config C
		if err := contract.Decode(raw, &config); err != nil {
			// The host needs the field the configuration fails on.
			return nil, &contract.Error{Code: contract.Invalid, Message: "config: " + err.Error()}
		}
		return plugin.Load(s, config), nil
	}}
}

// Run serves the definitions on standard input and output until the host
// shuts the plugin down, the context is canceled or a termination signal
// arrives. Registered cleanups have run when it returns; the caller decides
// what to do with the error.
func Run(ctx context.Context, definitions ...Definition) error {
	ctx, stop := processContext(ctx)
	defer stop()
	transport, err := stdioTransport()
	if err != nil {
		return err
	}
	return RunTransport(ctx, transport, definitions...)
}

// RunTransport is Run over a stream the caller owns, for tests.
func RunTransport(ctx context.Context, transport io.ReadWriteCloser, definitions ...Definition) error {
	byID := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		if _, duplicate := byID[definition.ID]; definition.ID == "" || definition.load == nil || duplicate {
			return contract.Fail(contract.Invalid, "invalid or duplicate SDK definition")
		}
		byID[definition.ID] = definition
	}
	if len(byID) == 0 {
		return contract.Fail(contract.Invalid, "at least one SDK definition required")
	}
	return serve(ctx, byID, transport)
}

// Identity returns the identity of the activation that s runs in. It
// reports false in a process that is not a native plugin.
func Identity(s *cordis.Scope) (contract.Identity, bool) {
	g, ok := guestOf(s)
	if !ok {
		return contract.Identity{}, false
	}
	return g.identity, true
}

func guestOf(s *cordis.Scope) (*guest, bool) {
	up, err := contract.UplinkKey.Get(s)
	if err != nil {
		return nil, false
	}
	g, ok := up.(*guest)
	return g, ok
}
