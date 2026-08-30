package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cfbevan/multiverse/internal/templates"
)

type Config struct {
	Debug bool   `env:"DEBUG" envDefault:"false"`
	Port  int    `env:"PORT" envDefault:"8080"`
	Env   string `env:"ENV" envDefault:"dev"`
}

type Application struct {
	config Config
	logger *slog.Logger
	tmpl   templates.TemplateCache
	wg     sync.WaitGroup
}

func NewApplication(config Config, logger *slog.Logger) *Application {
	templateCache, err := templates.NewTemplateCache()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	return &Application{
		config: config,
		logger: logger,
		tmpl:   templateCache,
	}
}

func (app *Application) Serve() error {
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", app.config.Port),
		Handler:      app.Routes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	shutdownError := make(chan error)

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		s := <-quit

		app.logger.Info("caught signal", "signal", s.String())

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err := srv.Shutdown(ctx)
		if err != nil {
			shutdownError <- err
		}

		app.logger.Info("completing background tasks", "addr", srv.Addr)

		app.wg.Wait()
		shutdownError <- nil
	}()

	app.logger.Info("starting server", "addr", srv.Addr, "env", app.config.Env)

	err := srv.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	err = <-shutdownError
	if err != nil {
		return err
	}

	app.logger.Info("stopped server", "addr", srv.Addr)

	return nil
}
