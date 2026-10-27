//go:build !windows

package main

import (
	"context"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

func instantiateWASI(ctx context.Context, runtime wazero.Runtime) error {
	_, err := wasi_snapshot_preview1.Instantiate(ctx, runtime)
	return err
}
