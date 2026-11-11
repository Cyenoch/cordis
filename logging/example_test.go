package logging_test

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/logging"
)

// printer is an output target that prints the level, logger name and message.
type printer struct{ name string }

func (printer) Enabled(context.Context, slog.Level) bool { return true }
func (p printer) Handle(_ context.Context, r slog.Record) error {
	fmt.Printf("%s %s: %s\n", r.Level, p.name, r.Message)
	return nil
}
func (p printer) WithAttrs(attrs []slog.Attr) slog.Handler {
	for _, attr := range attrs {
		if attr.Key == "logger" {
			p.name = attr.Value.String()
		}
	}
	return p
}
func (p printer) WithGroup(string) slog.Handler { return p }

// A plugin requires the logging service and gets a logger named after
// itself. Levels come from the configuration, by name, or from the scope.
func Example() {
	ctx := context.Background()
	app := cordis.New(cordis.Options{})
	defer app.Close(ctx)
	root := app.Scope()

	logs := logging.Plugin.Load(root, logging.Config{
		Level:  slog.LevelWarn,
		Levels: map[string]slog.Level{"db": slog.LevelInfo},
		Output: discard{},
	})
	logs.Wait(ctx)
	provider, _ := logging.Service.Get(root)
	provider.AddHandler(root, printer{})

	worker := func(name string) cordis.Plugin[struct{}] {
		return cordis.Plugin[struct{}]{
			Name:     name,
			Requires: []cordis.Requirement{logging.Service},
			Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
				logger, err := logging.Get(s)
				if err != nil {
					return err
				}
				logger.Info("started")
				return nil
			},
		}
	}
	worker("db").Load(root, struct{}{}).Wait(ctx)   // info is enabled for "db"
	worker("http").Load(root, struct{}{}).Wait(ctx) // filtered by the default level

	verbose := logging.Level.With(root, slog.LevelInfo)
	worker("http").Load(verbose, struct{}{}).Wait(ctx) // enabled by the scope
	// Output:
	// INFO db: started
	// INFO http: started
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
