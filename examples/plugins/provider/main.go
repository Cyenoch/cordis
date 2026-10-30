package main

import (
	"context"
	"fmt"
	"os"

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
	return RegisterExampleMessageServer(scope, &provider{})
}

type provider struct{}

func (*provider) Read(context.Context, ExampleMessageReadRequest) (ExampleMessageReadResponse, error) {
	return "hello", nil
}
