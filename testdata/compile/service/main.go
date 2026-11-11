package main

import "github.com/Cyenoch/cordis"

type database interface{ Name() string }

type notADatabase struct{}

var db = cordis.NewService[database]("db")

func main() {
	app := cordis.New(cordis.Options{})
	_ = db.Provide(app.Scope(), notADatabase{})
}
