// Package native runs explicitly trusted plugins as managed OS processes.
// It is not an operating-system privilege sandbox.
package native

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/runtime/internal/process"
	"github.com/Cyenoch/cordis/wire"
)

// PackageDirectoryEnv names the activation's verified package snapshot.
const PackageDirectoryEnv = "CORDIS_PACKAGE_DIR"

type Options struct {
	Environment map[string]string
	Lifetime    context.Context
	Limits      wire.Limits
	Grace       time.Duration
	// StderrBytes caps bytes forwarded per activation; excess is drained.
	StderrBytes int64
}

type Runtime struct{ options Options }

// New creates an adapter with no inherited environment and no implicit restart.
// Start's context covers startup only; set Options.Lifetime for parent lifetime.
func New(options ...Options) *Runtime {
	var selected Options
	if len(options) > 0 {
		selected = options[0]
	}
	if selected.Environment != nil {
		copied := make(map[string]string, len(selected.Environment))
		for key, value := range selected.Environment {
			copied[key] = value
		}
		selected.Environment = copied
	}
	return &Runtime{options: selected}
}

func (r *Runtime) Start(ctx context.Context, launch contract.Launch) (contract.Session, error) {
	path, err := process.Artifact(launch.Directory, launch.Artifact)
	if err != nil {
		return nil, fmt.Errorf("native artifact: %w", err)
	}
	directory, err := filepath.Abs(launch.Directory)
	if err != nil {
		return nil, err
	}
	environment := make(map[string]string, len(r.options.Environment)+1)
	for key, value := range r.options.Environment {
		if strings.EqualFold(key, PackageDirectoryEnv) {
			return nil, fmt.Errorf("%s is reserved for the native package snapshot", PackageDirectoryEnv)
		}
		environment[key] = value
	}
	environment[PackageDirectoryEnv] = directory
	return process.Start(ctx, path, nil, launch, process.Options{
		Environment: environment, Lifetime: r.options.Lifetime, Limits: r.options.Limits,
		Grace: r.options.Grace, StderrBytes: r.options.StderrBytes, CopyExecutable: true,
	})
}

var _ contract.Runtime = (*Runtime)(nil)
