// Command feed is a native plugin with a streaming method and a middleware
// that takes part in a chain shared with other processes.
package main

import (
	"context"
	"fmt"
	"iter"
	"os"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

func main() {
	if err := sdk.Run(context.Background(), sdk.Define(FeedPlugin(start))); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Render is a chain any process of the host can wrap or run.
var Render = contract.NewChainKey[string, string]("example.render")

func start(ctx context.Context, s *cordis.Scope, _ FeedConfig) error {
	if _, err := Render.Use(ctx, s, func(ctx context.Context, text string, next func(context.Context, string) (string, error)) (string, error) {
		out, err := next(ctx, text)
		return "[" + out + "]", err
	}); err != nil {
		return err
	}
	return ExampleFeedEndpoint.Provide(s, NewExampleFeedEndpoint(feed{}))
}

type feed struct{}

// Tail yields one line at a time. The consumer's pace holds it back, and it
// stops as soon as the consumer does.
func (feed) Tail(ctx context.Context, request ExampleFeedTailRequest) iter.Seq2[ExampleFeedTailResponse, error] {
	return func(yield func(ExampleFeedTailResponse, error) bool) {
		count, err := request.Count.Int64()
		if err != nil {
			yield(ExampleFeedTailResponse{}, err)
			return
		}
		for n := range count {
			if !yield(ExampleFeedTailResponse{Line: fmt.Sprintf("line %d", n)}, nil) {
				return
			}
		}
	}
}
