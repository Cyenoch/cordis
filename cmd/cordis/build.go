package main

import (
	"context"
	"flag"
	"io"

	"github.com/Cyenoch/cordis/packagehost"
)

func buildCommand(ctx context.Context, flags *flag.FlagSet, parse func() (bool, error), out io.Writer) error {
	var options packagehost.BuildOptions
	flags.StringVar(&options.GoPackage, "go-package", "", "explicit Go package to build")
	flags.StringVar(&options.GOOS, "goos", "", "explicit Go target OS (wasip1 for WASM)")
	flags.StringVar(&options.GOARCH, "goarch", "", "explicit Go target architecture (wasm for WASM)")
	flags.StringVar(&options.Output, "output", "", "artifact path relative to source; must match declaration")
	flags.StringVar(&options.GeneratedPackage, "generated-package", "main", "generated Go package")
	flags.StringVar(&options.GeneratedOutput, "generated-output", "cordis_gen.go", "generated Go path relative to source")
	if done, err := parse(); done {
		return err
	}
	if flags.NArg() != 1 || options.GoPackage == "" || options.GOOS == "" || options.GOARCH == "" {
		return &usageError{message: commandUsage["build"]}
	}
	descriptor, err := packagehost.Build(ctx, flags.Arg(0), options)
	if err != nil {
		return err
	}
	return writeJSON(out, descriptor)
}
