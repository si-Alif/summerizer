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

	// collection routes — will be wrapped with requireAuthenticatedUser once auth is wired up
	router.HandlerFunc(http.MethodPost, "/v1/collections", app.createCollectionHandler)
	router.HandlerFunc(http.MethodGet, "/v1/collections", app.listCollectionsHandler)
	router.HandlerFunc(http.MethodGet, "/v1/collections/:id", app.showCollectionHandler)
	router.HandlerFunc(http.MethodPatch, "/v1/collections/:id", app.updateCollectionHandler)
	router.HandlerFunc(http.MethodDelete, "/v1/collections/:id", app.deleteCollectionHandler)
	router.HandlerFunc(http.MethodPost, "/v1/collections/:id/search", app.searchCollectionHandler)
	router.HandlerFunc(http.MethodPost, "/v1/collections/:id/ask", app.askCollectionHandler)

	// source routes
	router.HandlerFunc(http.MethodPost, "/v1/collections/:id/sources", app.createSourceHandler)
	router.HandlerFunc(http.MethodGet, "/v1/collections/:id/sources", app.listSourceHandler)
	router.HandlerFunc(http.MethodGet, "/v1/sources/:id", app.showSourceHandler)
	router.HandlerFunc(http.MethodDelete, "/v1/sources/:id", app.deleteSourceHandler)

	// user routes
	router.HandlerFunc(http.MethodPost, "/v1/users", app.registerUserHandler)
	router.HandlerFunc(http.MethodPut, "/v1/users/activated", app.activateUserHandler)

	// token routes
	router.HandlerFunc(http.MethodPost, "/v1/tokens/authentication", app.createAuthenticationTokenHandler)

	return app.recoverPanic(app.rateLimit(app.authenticate(router)))
}
