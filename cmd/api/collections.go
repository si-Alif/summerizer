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
	sourceStatusSummary := map[string]int{
		"pending":   0,
		"ingesting": 0,
		"completed": 0,
		"failed":    0,
	}


	err = app.writeJSON(w, http.StatusOK, envelop{
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
		Max_Sources: 13,
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

	err = app.writeJSON(w, http.StatusCreated, envelop{"collection": collection}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}


}

// // updateCollectionHandler handles PATCH /v1/collections/:id
// // Supports partial updates — only provided fields are updated.
// func (app *application) updateCollectionHandler(w http.ResponseWriter, r *http.Request) {
// 	id, err := app.readIDParam(r)
// 	if err != nil {
// 		app.notFoundResponse(w, r)
// 		return
// 	}

// 	// TODO: replace with app.models.Collections.Get(id) once DB is wired
// 	_ = id

// 	// Use pointer fields to distinguish "not provided" (nil) from "provided empty"
// 	var input struct {
// 		Title       *string `json:"title"`
// 		Description *string `json:"description"`
// 	}

// 	err = app.readJSON(w, r, &input)
// 	if err != nil {
// 		app.badRequestResponse(w, r, err)
// 		return
// 	}

// 	v := validator.New()
// 	if data.ValidateCollectionUpdate(v, input.Title, input.Description); !v.Valid() {
// 		app.failedValidationResponse(w, r, v.Errors)
// 		return
// 	}

// 	// TODO: verify ownership — contextGetUser(r).ID == collection.UserID
// 	// TODO: apply non-nil fields to the fetched collection
// 	// TODO: app.models.Collections.Update(collection) — with optimistic locking
// 	// TODO: on ErrEditConflict → editConflictResponse
// 	// TODO: return 200 with updated collection JSON

// }

// deleteCollectionHandler handles DELETE /v1/collections/:id
func (app *application) deleteCollectionHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	// TODO: verify ownership — fetch collection, check contextGetUser(r).ID == collection.UserID
	// TODO: app.models.Collections.Delete(id)
	// TODO: on ErrRecordNotFound → notFoundResponse
	_ = id

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

	err = app.writeJSON(w, http.StatusOK, envelop{
		"metadata":    metadata,
		"collections": collections,
	}, nil)

	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

}
