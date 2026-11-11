package main

import "github.com/Cyenoch/cordis"

type database interface{ Name() string }

type sqlite string

func (s sqlite) Name() string { return string(s) }

var db = cordis.NewService[database]("db")

func main() {
	app := cordis.New(cordis.Options{})
	_ = db.Provide(app.Scope(), sqlite("file.db"))
}
