package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

func main() {
	if err := sdk.Run(context.Background(), NewMainDefinition(start)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func start(_ context.Context, scope contract.Scope, _ MainConfig) error {
	directory := os.Getenv("CORDIS_PACKAGE_DIR")
	if directory == "" {
		return fmt.Errorf("missing package resource directory")
	}
	return RegisterExampleResourceServer(scope, &resource{directory: directory})
}

type resource struct {
	directory string
}

func (r *resource) Read(context.Context, ExampleResourceReadRequest) (ExampleResourceReadResponse, error) {
	content, err := os.ReadFile(filepath.Join(r.directory, "assets", "message.txt"))
	return ExampleResourceReadResponse(content), err
}
