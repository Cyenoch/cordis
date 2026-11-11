package main

import (
	"context"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/plugintest"
	"github.com/Cyenoch/cordis/sdk"
)

func TestStart(t *testing.T) {
	harness, err := plugintest.Start(t.Context(), MainDefinitionSpec(), sdk.Define(MainPlugin), plugintest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := harness.Close(ctx); err != nil {
			t.Error(err)
		}
	})
}
