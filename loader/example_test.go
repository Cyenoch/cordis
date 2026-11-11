package loader_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/loader"
)

type helloConfig struct {
	Name string `json:"name"`
	Mark string `json:"mark"`
}

func Example() {
	dir, _ := os.MkdirTemp("", "loader-example-")
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "app.yml")
	os.WriteFile(file, []byte("plugins:\n  - id: hello\n    plugin: hello\n    config: {name: world}\n"), 0o644)

	hello := cordis.Plugin[helloConfig]{
		Name: "hello",
		Start: func(_ context.Context, _ *cordis.Scope, c helloConfig) error {
			fmt.Println("hello, " + c.Name + c.Mark)
			return nil
		},
	}
	registry := loader.NewRegistry()
	loader.Register(registry, "hello", hello, loader.Default(func() helloConfig { return helloConfig{Mark: "!"} }))

	ctx := context.Background()
	app := cordis.New(cordis.Options{})
	defer app.Close(ctx)
	loader.Plugin.Load(app.Scope(), loader.Config{File: file, Registry: registry})

	// Whatever requires loader.Ready starts once the entries have settled.
	done := make(chan struct{})
	cordis.Plugin[struct{}]{
		Name:     "main",
		Requires: []cordis.Requirement{loader.Ready},
		Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
			l, err := loader.Service.Get(s)
			if err != nil {
				return err
			}
			for _, entry := range l.Entries() {
				fmt.Println(entry.ID, entry.Plugin, entry.State)
			}
			close(done)
			return nil
		},
	}.Load(app.Scope(), struct{}{})
	<-done
	// Output:
	// hello, world!
	// hello hello active
}
