package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/validator"
)

func (app *application) registerUserHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Fullname string `json:"fullname"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	user := &data.User{
		Email: input.Email,
		Fullname: input.Fullname,
		Activated: false,
	}

	// generate password hash
	err = user.Password.Set(input.Password)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	v := validator.New()

	if data.ValidateUser(v, user); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	err =  app.models.Users.Insert(user)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrDuplicateEmail):
			v.AddError("email", "a user with this email address already exists")
			app.failedValidationResponse(w, r, v.Errors)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	token , err := app.models.Tokens.New(user.ID , 24 * time.Hour , data.ScopeActivation)

	templateData := struct {
		User             *data.User
		RegistrationDate string
		CurrentYear      int
		ActivationToken  string
		UserID           int64
	}{
		User:             user,
		RegistrationDate: user.CreatedAt.Format("January 2, 2006"),
		CurrentYear:      time.Now().Year(),
		ActivationToken:  token.Plaintext,
		UserID:           user.ID,
	}


	app.SpawnBackgroundTask(
		func() {
			err := app.mailer.Send(input.Email , "user_welcome.tmpl.html" , templateData)
			if err != nil {
				app.logger.Error(err.Error())
			}
		},
	)

	err = app.writeJSON(w, http.StatusCreated, envelope{"user": user}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

}

