package main

import (
	"fmt"
	"net/http"
)

// logs the error at level error
func (app *application) logError(r *http.Request, err error) {
	var (
		method = r.Method
		path   = r.URL
	)

	app.logger.Error(err.Error(), "method", method, "uri", path)
}

// returns the error in a structured JSON response
func (app *application) errorResponse(
	w http.ResponseWriter,
	r *http.Request,
	statusCode int,
	msg any,
) {
	env := envelope{
		"error": msg,
	}

	err := app.writeJSON(w, statusCode, env, nil)

	if err != nil {
		app.logError(r, err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (app *application) serverErrorResponse(w http.ResponseWriter, r *http.Request, err error) {
	app.logError(r, err)
	app.errorResponse(w, r, http.StatusInternalServerError, "The server encountered an error and couldn't process your request")
}

func (app *application) notFoundResponse(w http.ResponseWriter, r *http.Request) {
	app.errorResponse(w, r, http.StatusNotFound, "The requested resource couldn't be found")
}

func (app *application) badRequestResponse(w http.ResponseWriter, r *http.Request, err error) {
	app.errorResponse(w, r, http.StatusBadRequest, err.Error())
}

// when a route exists but the HTTP method doesn't match (e.g. POST to a GET-only route)
func (app *application) methodNotAllowedResponse(w http.ResponseWriter, r *http.Request) {
	msg := fmt.Sprintf("the %s method is not supported for this resource", r.Method)
	app.errorResponse(w, r, http.StatusMethodNotAllowed, msg)
}

// validation related errors , errors will be in a map structure (map[string]string)
func (app *application) failedValidationResponse(w http.ResponseWriter, r *http.Request, errors map[string]string) {
	app.errorResponse(w, r, http.StatusUnprocessableEntity, errors)
}

func (app *application) editConflictResponse(w http.ResponseWriter, r *http.Request) {
	msg := "unable to update the record due to an edit conflict, please try again"
	app.errorResponse(w, r, http.StatusConflict, msg)
}

func (app *application) rateLimitExceededResponse(w http.ResponseWriter, r *http.Request) {
	msg := "slow down😭 , rate limit exceeded "
	app.errorResponse(w, r, http.StatusTooManyRequests, msg)
}

func (app *application) invalidCredentialResponse(w http.ResponseWriter, r *http.Request) {
	msg := "invalid authentication credentials"
	app.errorResponse(w, r, http.StatusUnauthorized, msg)
}

func (app *application) invalidAuthenticationTokenResponse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", "Bearer")

	msg := "invalid or missing authentication token"
	app.errorResponse(w, r, http.StatusUnauthorized, msg)
}

func (app *application) AuthenticationRequiredResponse(w http.ResponseWriter, r *http.Request) {
	msg := "you must be authenticated to access this request"
	app.errorResponse(w, r, http.StatusUnauthorized, msg)
}

// after adding activation or subscription related layer
func (app *application) notPermittedResponse(w http.ResponseWriter, r *http.Request) {
	msg := "Your user account doesn't have the required permission to access this resource"
	app.errorResponse(w, r, http.StatusForbidden, msg)
}

func (app *application) ActivationRequiredResponse(w http.ResponseWriter, r *http.Request) {
	msg := "Your account must be activated to perform this task"
	app.errorResponse(w, r, http.StatusForbidden, msg)
}

func (app *application) duplicateResourceResponse(w http.ResponseWriter, r *http.Request, detail string) {
	app.errorResponse(w, r, http.StatusConflict, detail)
}

func (app *application) payloadTooLargeResponse(w http.ResponseWriter, r *http.Request, max_size int) {
	msg := fmt.Sprintf("request body must not be larger than %d bytes", max_size)
	app.errorResponse(w, r, http.StatusRequestEntityTooLarge, msg)
}

// when a source URL doesn't match any supported type (web, youtube, pdf)
func (app *application) unsupportedMediaTypeResponse(w http.ResponseWriter, r *http.Request) {
	msg := "the provided URL or content type is not supported"
	app.errorResponse(w, r, http.StatusUnsupportedMediaType, msg)
}

func (app *application) resourceNotReadyResponse(w http.ResponseWriter, r *http.Request) {
	msg := "the requested resource is still being processed, please try again later"
	app.errorResponse(w, r, http.StatusAccepted, msg)
}

func (app *application) serviceUnavailableResponse(w http.ResponseWriter, r *http.Request) {
	msg := "a required upstream service is currently unavailable, please try again later"
	app.errorResponse(w, r, http.StatusServiceUnavailable, msg)
}
