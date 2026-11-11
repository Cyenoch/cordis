package main

import (
	"context"

	"github.com/Cyenoch/cordis"
)

type first struct{ Port int }
type second struct{ Name string }

func main() {
	app := cordis.New(cordis.Options{})
	inst := cordis.Plugin[first]{Name: "p"}.Load(app.Scope(), first{})
	_ = inst.Update(context.Background(), second{})
}
