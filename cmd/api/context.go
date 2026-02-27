package main

import (
	"context"
	"net/http"

	"github.com/si-Alif/summerizer/internal/data"
)

type contextKey string

const contextKeyUser = contextKey("user")

// Takes the request context as an argument and pointer to a user . Sets that user in that requests context window and returns updated *http.Request
func (app *application) SetUserInRequestContext(r *http.Request, user *data.User) *http.Request {
	ctx := context.WithValue(r.Context(), contextKeyUser, user)
	return r.WithContext(ctx)
}

func (app *application) GetUserFromSubsequentRequestContext(r *http.Request) *data.User {
	// user, ok := r.Context().Value(contextKeyUser).(*data.User)
	// if !ok {
	// 	panic("missing user value in request context")
	// }

	user := &data.User{
		ID: 1,
		Fullname: "John Doe",
		Email: "john.doe@example.com",
	}

	return user
}
