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
	if err := sdk.Run(context.Background(), sdk.Define(FeedPlugin)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Render is a chain any process of the host can wrap or run.
var Render = contract.NewChainKey[string, string]("example.render")

// Feed has a streaming method: it returns a sequence instead of one value.
//
//cordis:service example.feed 1
type Feed interface {
	Tail(ctx context.Context, request TailRequest) iter.Seq2[Line, error]
}

type TailRequest struct {
	Count int `json:"count" cordis:"minimum=0"`
}

type Line struct {
	Line string `json:"line"`
}

//cordis:plugin feed
//cordis:provides Feed
func start(ctx context.Context, s *cordis.Scope, _ struct{}) error {
	if _, err := Render.Use(ctx, s, func(ctx context.Context, text string, next func(context.Context, string) (string, error)) (string, error) {
		out, err := next(ctx, text)
		return "[" + out + "]", err
	}); err != nil {
		return err
	}
	return ProvideFeed(s, feed{})
}

type feed struct{}

// Tail yields one line at a time. The consumer's pace holds it back, and it
// stops as soon as the consumer does.
func (feed) Tail(_ context.Context, request TailRequest) iter.Seq2[Line, error] {
	return func(yield func(Line, error) bool) {
		for n := range request.Count {
			if !yield(Line{Line: fmt.Sprintf("line %d", n)}, nil) {
				return
			}
		}
	}
}
