package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func (app *application) serve() error {
	srv := &http.Server{
		Addr:        fmt.Sprintf(":%d", app.config.port),
		Handler:     app.routes(),
		IdleTimeout: time.Minute,
		ReadTimeout: 5 * time.Second,
		// WriteTimeout: 10 * time.Second, // Add later when LLM generation is implemented and we have a better idea of how long it takes to generate a response
		ErrorLog: slog.NewLogLogger(app.logger.Handler(), slog.LevelError),
	}

	// to handle shutdown signals gracefully, we create a channel to receive any errors that occur during the shutdown process
	shutDownError := make(chan error)

	// start a goroutine to check for catchable shutdown signal during the server's lifetime
	go func() {
		quit := make(chan os.Signal, 1)

		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		s := <-quit
		app.logger.Info("shutting down server...", "signal", s.String())

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err := srv.Shutdown(ctx)

		if err != nil {
			shutDownError <- err
			return
		}

		app.logger.Info("completing background tasks...")
		app.wg.Wait()

		shutDownError <- nil

	}()

	app.logger.Info("starting server", "addr", srv.Addr, "env", app.config.env)

	err := srv.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	err = <-shutDownError
	if err != nil {
		return err
	}

	app.logger.Info("stopped server", "addr", srv.Addr, "env", app.config.env)

	return nil

}
