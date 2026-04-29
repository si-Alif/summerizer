package main

import (
	"context"
	"net/http"
	"time"
)

func (app *application) healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	status := "available"
	statusCode := http.StatusOK

	if app.db == nil {
		status = "unavailable"
		statusCode = http.StatusServiceUnavailable
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := app.db.PingContext(ctx); err != nil {
			app.logger.Error("healthcheck: db ping failed", "error", err)
			status = "unavailable"
			statusCode = http.StatusServiceUnavailable
		}
	}

	data := envelope{
		"status": status,
		"system_info": map[string]any{
			"environment": app.config.env,
			"version":     version,
		},
	}

	err := app.writeJSON(w, statusCode, data, nil)
	if err != nil {
		app.logger.Error("Server Error")
	}
}
