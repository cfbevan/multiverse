package main

import (
	"expvar"
	"log/slog"
	"os"
	"runtime/debug"
	"time"

	"github.com/caarlos0/env"
	"github.com/cfbevan/multiverse/internal/ui"
)

func main() {
	var cfg ui.Config
	if err := env.Parse(&cfg); err != nil {
		panic(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	bi, ok := debug.ReadBuildInfo()
	if ok {
		expvar.NewString("version").Set(bi.Main.Version)
	}

	expvar.Publish("timestamp", expvar.Func(func() any {
		return time.Now().Unix()
	}))

	app := ui.NewApplication(cfg, logger)

	if err := app.Serve(); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}
