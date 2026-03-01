package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/validator"
)

func (app *application) createSourceHandler(w http.ResponseWriter, r *http.Request) {

	cid , err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}


	var input struct {
		URL string `json:"url"`
		Title string `json:"title"`
	}

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	srcType := validator.DetectSourceType(input.URL)

	if  srcType== validator.InvalidSourceType {
		app.badRequestResponse(w, r, data.ErrInvalidSourceURL)
		return
	}

	user_id := app.GetUserFromSubsequentRequestContext(r).ID

	collection , err := app.models.Collections.GetByID(cid , user_id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	count , err := app.models.Sources.CountByCollection(cid)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	if count >= collection.Max_Sources {
		app.errorResponse(w , r ,
			http.StatusConflict ,
			fmt.Sprintf("collection already has maximum number of sources (%d)", collection.Max_Sources),
		)
		return
	}

	source := &data.Source{
		CollectionID: cid,
		URL: input.URL,
		Title: input.Title,
		SourceType : srcType,
		Metadata: make(data.JsonMap),
	}

	v := validator.New()
	if data.ValidateSource(v, source); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	err = app.models.Sources.Insert(source)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrDuplicateRecord) :
			app.errorResponse(w, r, http.StatusConflict, "source already exists in collection")
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/sources/%d", source.ID))

	err = app.writeJSON(w, http.StatusCreated, envelop{"source": source}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}