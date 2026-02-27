package main

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func (app *application) routes() http.Handler {
	router := httprouter.New()

	// override httprouter's default plain-text error responses with JSON
	router.NotFound = http.HandlerFunc(app.notFoundResponse)
	router.MethodNotAllowed = http.HandlerFunc(app.methodNotAllowedResponse)

	router.HandlerFunc(http.MethodGet, "/v1/healthcheck", app.healthCheckHandler)

	// collection routes — will be wrapped with requireAuthenticatedUser once auth is wired (Step 5)
	router.HandlerFunc(http.MethodGet, "/v1/collections/:id", app.showCollectionHandler)
	router.HandlerFunc(http.MethodPost, "/v1/collections", app.createCollectionHandler)

	// source routes

	// router.HandlerFunc(http.MethodGet, "/v1/sources/:id", app.showSourceHandler)


	return router
}
