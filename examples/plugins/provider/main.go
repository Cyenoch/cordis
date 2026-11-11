package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/sdk"
)

func main() {
	if err := sdk.Run(context.Background(), sdk.Define(MainPlugin)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Message is the service another package requires.
//
//cordis:service example.message 1
type Message interface {
	Read(ctx context.Context) (string, error)
}

//cordis:plugin main
//cordis:provides Message
func start(_ context.Context, s *cordis.Scope, _ struct{}) error {
	return ProvideMessage(s, provider{})
}

type provider struct{}

func (provider) Read(context.Context) (string, error) { return "hello", nil }
