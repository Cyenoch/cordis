package main

import (
	"context"

	"github.com/Cyenoch/cordis"
)

var ping = cordis.NewEvent[string]("ping")

func main() {
	app := cordis.New(cordis.Options{})
	ping.On(app.Scope(), func(context.Context, int) error { return nil })
}
