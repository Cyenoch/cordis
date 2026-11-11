package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/sdk"
)

// Config is the configuration of an entry. Its fields are the keys an entry
// may set; a cordis tag constrains a value, as in `cordis:"minLength=1"`.
type Config struct{}

// start runs when the entry starts. Register cleanups, background work and
// the services the plugin provides on s; they end with the entry.
//
//cordis:plugin main
func start(ctx context.Context, s *cordis.Scope, config Config) error {
	return nil
}

func main() {
	if err := sdk.Run(context.Background(), sdk.Define(MainPlugin)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
