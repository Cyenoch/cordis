// Package wasm starts one isolated cordis-worker for each activation.
package wasm

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/runtime/internal/process"
	"github.com/Cyenoch/cordis/wire"
)

type Options struct {
	Lifetime       context.Context
	Limits         wire.Limits
	Grace          time.Duration
	StderrBytes    int64
	MemoryPages    uint32
	ArtifactBytes  int64
	CompileTimeout time.Duration
	StartTimeout   time.Duration
	// CacheDirectory must be writable only by the trusted host user.
	CacheDirectory string
}

type Runtime struct {
	worker  string
	options Options
}

// New requires an exact, absolute trusted worker path (no PATH lookup). Launch's
// Worker overrides it. Start ctx covers startup, not the returned session lifetime.
func New(workerPath string, options ...Options) *Runtime {
	var selected Options
	if len(options) > 0 {
		selected = options[0]
	}
	return &Runtime{worker: workerPath, options: selected}
}

func (r *Runtime) Start(ctx context.Context, launch contract.Launch) (contract.Session, error) {
	artifact, err := process.Artifact(launch.Directory, launch.Artifact)
	if err != nil {
		return nil, fmt.Errorf("wasm artifact: %w", err)
	}
	worker := r.worker
	if launch.Worker != "" {
		worker = launch.Worker
	}
	options := r.options
	if options.MemoryPages == 0 {
		options.MemoryPages = 4096
	}
	if options.ArtifactBytes == 0 {
		options.ArtifactBytes = 64 << 20
	}
	if options.CompileTimeout == 0 {
		options.CompileTimeout = 30 * time.Second
	}
	if options.StartTimeout == 0 {
		options.StartTimeout = 30 * time.Second
	}
	if options.MemoryPages > 65536 || options.ArtifactBytes < 8 || options.CompileTimeout < 0 || options.StartTimeout < 0 {
		return nil, errors.New("invalid wasm memory, artifact or execution limits")
	}
	args := []string{
		"-module", artifact,
		"-memory-pages", strconv.FormatUint(uint64(options.MemoryPages), 10),
		"-artifact-bytes", strconv.FormatInt(options.ArtifactBytes, 10),
		"-compile-timeout", options.CompileTimeout.String(),
		"-start-timeout", options.StartTimeout.String(),
	}
	if options.CacheDirectory != "" {
		args = append(args, "-cache-directory", options.CacheDirectory)
	}
	return process.Start(ctx, worker, args, launch, process.Options{
		Lifetime: options.Lifetime, Limits: options.Limits, Grace: options.Grace, StderrBytes: options.StderrBytes,
	})
}

var _ contract.Runtime = (*Runtime)(nil)
