package main

import (
	"expvar"
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func (app *application) routes() http.Handler {
	router := httprouter.New()

	// override httprouter's default plain-text error responses with JSON
	router.NotFound = http.HandlerFunc(app.notFoundResponse)
	router.MethodNotAllowed = http.HandlerFunc(app.methodNotAllowedResponse)

	router.HandlerFunc(http.MethodGet, "/v1/healthcheck", app.healthCheckHandler)

	router.HandlerFunc(http.MethodPost, "/v1/collections", app.requireActivatedUser(app.createCollectionHandler))
	router.HandlerFunc(http.MethodGet, "/v1/collections", app.requireActivatedUser(app.listCollectionsHandler))
	router.HandlerFunc(http.MethodGet, "/v1/collections/:id", app.requireActivatedUser(app.showCollectionHandler))
	router.HandlerFunc(http.MethodPatch, "/v1/collections/:id", app.requireActivatedUser(app.updateCollectionHandler))
	router.HandlerFunc(http.MethodDelete, "/v1/collections/:id", app.requireActivatedUser(app.deleteCollectionHandler))
	router.HandlerFunc(http.MethodPost, "/v1/collections/:id/search", app.requireActivatedUser(app.searchCollectionHandler))
	router.HandlerFunc(http.MethodPost, "/v1/collections/:id/ask", app.requireActivatedUser(app.askCollectionHandler))

	// source routes
	router.HandlerFunc(http.MethodPost, "/v1/collections/:id/sources", app.requireActivatedUser(app.createSourceHandler))
	router.HandlerFunc(http.MethodGet, "/v1/collections/:id/sources", app.requireActivatedUser(app.listSourceHandler))
	router.HandlerFunc(http.MethodGet, "/v1/sources/:id", app.requireActivatedUser(app.showSourceHandler))
	router.HandlerFunc(http.MethodDelete, "/v1/sources/:id", app.requireActivatedUser(app.deleteSourceHandler))

	// user routes
	router.HandlerFunc(http.MethodPost, "/v1/users", app.registerUserHandler)
	router.HandlerFunc(http.MethodPut, "/v1/users/activated", app.activateUserHandler)

	// token routes
	router.HandlerFunc(http.MethodPost, "/v1/tokens/authentication", app.createAuthenticationTokenHandler)

	// metrics route
	router.Handler(http.MethodGet, "/debug/vars", expvar.Handler())

	return app.metrics(app.recoverPanic(app.enableCORS(app.rateLimit(app.authenticate(router)))))
}
