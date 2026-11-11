package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/sdk"
)

func main() {
	if err := sdk.Run(context.Background(), sdk.Define(MainPlugin(start))); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func start(_ context.Context, s *cordis.Scope, config MainConfig) error {
	if !filepath.IsAbs(config.Output) {
		return fmt.Errorf("output must be an absolute path outside the watched source")
	}
	// The plugin requires the message service, so it is here before Start runs.
	endpoint, err := ExampleMessageEndpoint.Get(s)
	if err != nil {
		return err
	}
	client, err := NewExampleMessageClient(endpoint)
	if err != nil {
		return err
	}
	s.Go(func(ctx context.Context) error {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			message, err := client.Read(ctx, ExampleMessageReadRequest{})
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				return err
			}
			if err := os.WriteFile(config.Output, []byte(message), 0600); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	})
	return nil
}
