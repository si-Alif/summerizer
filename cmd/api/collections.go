package main

import (
	"errors"
	"fmt"
	"net/http"
	// "time"

	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/validator"
)

func (app *application) showCollectionHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	collection, err := app.models.Collections.GetByID(id , app.GetUserFromSubsequentRequestContext(r).ID)
	if err != nil {
		switch {
		case errors.Is(err , data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	// TODO: replace with app.models.Sources.GetStatusSummary(collection.ID)
	// Source status summary gives the user a quick overview of processing state
	// e.g. { "pending": 2, "ingesting": 1, "completed": 5, "failed": 0 }
	sourceStatusSummary , err := app.models.Sources.GetStatusSummary(collection.ID)

	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{
		"collection":     collection,
		"source_summary": sourceStatusSummary,
	}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *application) createCollectionHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}

	err := app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	collection := &data.Collection{
		UserID:      app.GetUserFromSubsequentRequestContext(r).ID,
		Title:       input.Title,
		Description: input.Description,
		Max_Sources: 15,
	}

	v := validator.New()
	if data.ValidateCollection(v, collection); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	err = app.models.Collections.Insert(collection)
	if err != nil {
		switch {
			case errors.Is(err , data.ErrDuplicateRecord):
				app.duplicateResourceResponse(w , r , "collection with this title already exists")
			default:
				app.serverErrorResponse(w, r, err)
		}
		return
	}

	headers := make(http.Header)
	headers.Set("Location", fmt.Sprintf("/v1/collections/%d", collection.ID))

	err = app.writeJSON(w, http.StatusCreated, envelope{"collection": collection}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}


}

// updateCollectionHandler handles PATCH /v1/collections/:id
// Supports partial updates — only provided fields are updated.
func (app *application) updateCollectionHandler(w http.ResponseWriter, r *http.Request) {

	collection_id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}


	collection, err := app.models.Collections.GetByID(collection_id , app.GetUserFromSubsequentRequestContext(r).ID)

	if err != nil {
		switch {
		case errors.Is(err , data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	var input struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Max_sources *int    `json:"max_sources"`
	}

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	if input.Title != nil {
		collection.Title = *input.Title
	}

	if input.Description != nil {
		collection.Description = *input.Description
	}

	if input.Max_sources != nil {
		collection.Max_Sources = *input.Max_sources
	}

	v := validator.New()
	if data.ValidateCollection(v, collection); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	err = app.models.Collections.Update(collection)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
				app.editConflictResponse(w, r)
		case errors.Is(err, data.ErrDuplicateRecord):
				app.duplicateResourceResponse(w, r, "collection with this title already exists")
		default:
				app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"collection": collection}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}

}

// deleteCollectionHandler handles DELETE /v1/collections/:id
func (app *application) deleteCollectionHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	err = app.models.Collections.Delete(id , app.GetUserFromSubsequentRequestContext(r).ID)
	if err != nil {
		switch {
		case errors.Is(err , data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{"message": "collection successfully deleted"}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}

}

// listCollectionsHandler handles GET /v1/collections?page=1&page_size=20
func (app *application) listCollectionsHandler(w http.ResponseWriter, r *http.Request) {

	var input struct {
		Title string
		data.Filters
	}

	v := validator.New()

	qrs := r.URL.Query()

	input.Title = app.readString(qrs , "title" , "")
	input.Filters.Page = app.readInt(qrs , "page" , 1 , v)
	input.Filters.PageSize = app.readInt(qrs , "page_size" , 20 , v)
	input.Filters.Sort = app.readString(qrs , "sort" , "id")
	input.Filters.SortSafeList = []string{"id", "title", "created_at", "-id", "-title", "-created_at"}



	if data.ValidateFilters(v, input.Filters) ; !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	user := app.GetUserFromSubsequentRequestContext(r)
	collections, metadata, err := app.models.Collections.GetAll(user.ID , input.Title , input.Filters)

	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{
		"metadata":    metadata,
		"collections": collections,
	}, nil)

	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

}
