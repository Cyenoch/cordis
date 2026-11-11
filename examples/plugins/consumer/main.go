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
	if err := sdk.Run(context.Background(), sdk.Define(MainPlugin)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Message is the service this plugin needs. A consumer declares the methods
// it calls; the host matches them against what the provider declares.
//
//cordis:service example.message 1
type Message interface {
	Read(ctx context.Context) (string, error)
}

type Config struct {
	Output string `json:"output"`
}

//cordis:plugin main
//cordis:requires Message
func start(_ context.Context, s *cordis.Scope, config Config) error {
	if !filepath.IsAbs(config.Output) {
		return fmt.Errorf("output must be an absolute path outside the watched source")
	}
	// The plugin requires the message service, so it is here before Start runs.
	client, err := GetMessage(s)
	if err != nil {
		return err
	}
	s.Go(func(ctx context.Context) error {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			message, err := client.Read(ctx)
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
