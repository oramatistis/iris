package main

import (
	"time"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/accesslog"
)

func main() {
	app := iris.New()
	ac := accesslog.File("access_log.csv")
	ac.SetFormatter(&accesslog.CSV{
		AutoFlush: true,
		Header:    true,
		// DateScript:   "FROM_UNIX",
		LatencyRound: time.Second,
	})

	app.UseRouter(ac.Handler)
	app.Get("/", index)

	app.Listen(":8080")
}

func index(ctx iris.Context) {
	if sleepDur := ctx.URLParam("sleep"); sleepDur != "" {
		if d, err := time.ParseDuration(sleepDur); err == nil {
			time.Sleep(d)
		}
	}

	ctx.WriteString("Index")
}
