// Command kernel shows the in-process kernel: two plugins, a service
// dependency, a middleware chain and a configuration update.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Cyenoch/cordis"
)

// Greeter is the service one plugin provides and the other requires.
type Greeter interface {
	Greet(name string) string
}

var (
	GreeterService = cordis.NewService[Greeter]("greeter")
	// Render lets any plugin rewrite a greeting on its way out.
	Render = cordis.NewChain[string, string]("render")
)

type GreeterConfig struct{ Greeting string }

type greeter struct{ greeting string }

func (g greeter) Greet(name string) string { return g.greeting + ", " + name }

var GreeterPlugin = cordis.Plugin[GreeterConfig]{
	Name: "greeter",
	Validate: func(c GreeterConfig) error {
		if c.Greeting == "" {
			return fmt.Errorf("greeting must not be empty")
		}
		return nil
	},
	Start: func(_ context.Context, s *cordis.Scope, c GreeterConfig) error {
		s.Defer(func(context.Context) error {
			fmt.Println("greeter: stopped", c.Greeting)
			return nil
		})
		return GreeterService.Provide(s, greeter{greeting: c.Greeting})
	},
}

// ShoutPlugin requires the greeter and upper-cases everything rendered.
var ShoutPlugin = cordis.Plugin[struct{}]{
	Name:     "shout",
	Requires: []cordis.Requirement{GreeterService},
	Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		g, err := GreeterService.Get(s)
		if err != nil {
			return err
		}
		fmt.Println("shout: started with", g.Greet("plugin"))
		Render.Use(s, func(ctx context.Context, text string, next cordis.Handler[string, string]) (string, error) {
			out, err := next(ctx, text)
			return strings.ToUpper(out), err
		})
		return nil
	},
}

func run(ctx context.Context) error {
	app := cordis.New(cordis.Options{})
	defer app.Close(ctx)
	root := app.Scope()

	shout := ShoutPlugin.Load(root, struct{}{})
	if err := shout.Wait(ctx); err != nil {
		return err
	}
	fmt.Println("shout:", shout.Status().State, "until a greeter exists")

	greeter := GreeterPlugin.Load(root, GreeterConfig{Greeting: "hello"})
	if err := greeter.Wait(ctx); err != nil {
		return err
	}
	if err := shout.Wait(ctx); err != nil {
		return err
	}

	say := func(name string) error {
		g, err := GreeterService.Get(root)
		if err != nil {
			return err
		}
		out, err := Render.Do(ctx, root, g.Greet(name), func(_ context.Context, text string) (string, error) {
			return text + "!", nil
		})
		fmt.Println(out)
		return err
	}
	if err := say("world"); err != nil {
		return err
	}

	// The greeter restarts with the new configuration; shout is stopped
	// first and started again against the new greeter.
	if err := greeter.Update(ctx, GreeterConfig{Greeting: "goodbye"}); err != nil {
		return err
	}
	if err := shout.Wait(ctx); err != nil {
		return err
	}
	if err := say("world"); err != nil {
		return err
	}
	if err := greeter.Update(ctx, GreeterConfig{}); err != nil {
		fmt.Println("rejected:", err)
	}
	return app.Close(ctx)
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
