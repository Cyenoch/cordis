package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/sdk"
)

func main() {
	if err := sdk.Run(context.Background(), sdk.Define(MainPlugin)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Resource reads a file the package ships.
//
//cordis:service example.resource 1
type Resource interface {
	Read(ctx context.Context) (string, error)
}

//cordis:plugin main
//cordis:provides Resource
func start(_ context.Context, s *cordis.Scope, _ struct{}) error {
	directory := os.Getenv("CORDIS_PACKAGE_DIR")
	if directory == "" {
		return fmt.Errorf("missing package resource directory")
	}
	return ProvideResource(s, resource{directory: directory})
}

type resource struct {
	directory string
}

func (r resource) Read(context.Context) (string, error) {
	content, err := os.ReadFile(filepath.Join(r.directory, "assets", "message.txt"))
	return string(content), err
}
