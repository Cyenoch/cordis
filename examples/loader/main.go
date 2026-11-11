// Command loader assembles an application from a configuration file, edits
// it while it runs and shows what is written back.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/loader"
	"github.com/Cyenoch/cordis/logging"
)

//go:embed app.yml
var initial []byte

type Greeter interface {
	Greet(name string) string
}

var GreeterService = cordis.NewService[Greeter]("greeter")

type GreeterConfig struct {
	Greeting string `json:"greeting"`
	Mark     string `json:"mark"`
}

type greeter GreeterConfig

func (g greeter) Greet(name string) string { return g.Greeting + ", " + name + g.Mark }

var GreeterPlugin = cordis.Plugin[GreeterConfig]{
	Name: "greeter",
	Validate: func(c GreeterConfig) error {
		if c.Greeting == "" {
			return fmt.Errorf("greeting must not be empty")
		}
		return nil
	},
	Start: func(_ context.Context, s *cordis.Scope, c GreeterConfig) error {
		return GreeterService.Provide(s, greeter(c))
	},
}

// lines collects what the shout plugins say, so that the example can print
// it in a stable order.
type lines struct {
	mu   sync.Mutex
	said []string
}

func (l *lines) add(line string) {
	l.mu.Lock()
	l.said = append(l.said, line)
	l.mu.Unlock()
}

func (l *lines) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	slices.Sort(l.said)
	for _, line := range l.said {
		fmt.Println(line)
	}
	l.said = nil
}

// shout greets its own entry and reports whether debug logging is on for it.
func shout(out *lines) cordis.Plugin[struct{}] {
	return cordis.Plugin[struct{}]{
		Name:     "shout",
		Requires: []cordis.Requirement{GreeterService, logging.Service},
		Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
			g, err := GreeterService.Get(s)
			if err != nil {
				return err
			}
			logger, err := logging.Get(s)
			if err != nil {
				return err
			}
			id, _ := loader.EntryOf(s)
			out.add(fmt.Sprintf("%s: %s (debug=%v)", id, g.Greet(id), logger.Enabled(ctx, slog.LevelDebug)))
			return nil
		},
	}
}

func run(ctx context.Context, dir string) error {
	file := filepath.Join(dir, "app.yml")
	if err := os.WriteFile(file, initial, 0o644); err != nil {
		return err
	}

	out := &lines{}
	registry := loader.NewRegistry()
	if err := loader.Register(registry, "greeter", GreeterPlugin,
		loader.Default(func() GreeterConfig { return GreeterConfig{Mark: "!"} })); err != nil {
		return err
	}
	if err := loader.Register(registry, "shout", shout(out)); err != nil {
		return err
	}
	if err := loader.RegisterSetting(registry, logging.Level); err != nil {
		return err
	}

	app := cordis.New(cordis.Options{})
	defer app.Close(ctx)
	root := app.Scope()
	logging.Plugin.Load(root, logging.Config{Output: io.Discard})
	instance := loader.Plugin.Load(root, loader.Config{File: file, Registry: registry})
	if err := instance.Wait(ctx); err != nil {
		return err
	}

	// The application reaches the loader like any other service.
	found := make(chan *loader.Loader, 1)
	cordis.Plugin[struct{}]{
		Name:     "admin",
		Requires: []cordis.Requirement{loader.Ready},
		Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			l, err := loader.Service.Get(s)
			found <- l
			return err
		},
	}.Load(root, struct{}{})
	l := <-found
	out.flush()

	fmt.Println("-- update greeter")
	config := json.RawMessage(`{"greeting": "goodbye"}`)
	if err := l.Update(ctx, "greeter", loader.EntryPatch{Config: &config}); err != nil {
		return err
	}
	if err := l.Wait(ctx); err != nil {
		return err
	}
	out.flush()

	fmt.Println("-- reject an invalid entry")
	if _, err := l.Create(ctx, "", loader.EntryOptions{ID: "broken"}); err != nil {
		fmt.Println(err)
	}

	fmt.Println("-- create and disable")
	if _, err := l.Create(ctx, "tenant", loader.EntryOptions{ID: "late", Plugin: "shout"}); err != nil {
		return err
	}
	disabled := true
	if err := l.Update(ctx, "shout", loader.EntryPatch{Disabled: &disabled}); err != nil {
		return err
	}
	if err := l.Wait(ctx); err != nil {
		return err
	}
	out.flush()
	for _, entry := range l.Entries() {
		fmt.Printf("%s: %s\n", entry.ID, entry.State)
	}

	fmt.Println("-- app.yml")
	written, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	fmt.Print(string(written))
	return app.Close(ctx)
}

func main() {
	if err := inTemp(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func inTemp(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "cordis-loader-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	return run(ctx, dir)
}
