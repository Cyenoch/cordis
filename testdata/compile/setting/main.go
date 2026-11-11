package main

import "github.com/Cyenoch/cordis"

var db = cordis.NewService[int]("db")
var level = cordis.NewSetting[string](db)

func main() {
	app := cordis.New(cordis.Options{})
	_ = level.With(app.Scope(), 42)
}
