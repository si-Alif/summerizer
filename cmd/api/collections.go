package main

import (
	"net/http"
	"time"

	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/validator"
)

func (app *application) showCollectionHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	// TODO: replace with app.models.Collections.Get(id) once DB is wired
	collection := data.Collection{
		ID:          id,
		UserID:      1,
		Title:       "Go Concurrency",
		Description: "Resources about Go concurrency patterns",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Version:     1,
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

	// TODO: verify ownership — contextGetUser(r).ID == collection.UserID

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
		UserID:      1, // TODO: replace with contextGetUser(r).ID
		Title:       input.Title,
		Description: input.Description,
	}

	v := validator.New()
	if data.ValidateCollection(v, collection); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	// TODO: check collection count per user (max 20) before insert
	// TODO: app.models.Collections.Insert(collection)
	// TODO: return 201 Created with collection JSON + Location header

}

// updateCollectionHandler handles PATCH /v1/collections/:id
// Supports partial updates — only provided fields are updated.
func (app *application) updateCollectionHandler(w http.ResponseWriter, r *http.Request) {
	id, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	// TODO: replace with app.models.Collections.Get(id) once DB is wired
	_ = id

	// Use pointer fields to distinguish "not provided" (nil) from "provided empty"
	var input struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
	}

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	v := validator.New()
	if data.ValidateCollectionUpdate(v, input.Title, input.Description); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	// TODO: verify ownership — contextGetUser(r).ID == collection.UserID
	// TODO: apply non-nil fields to the fetched collection
	// TODO: app.models.Collections.Update(collection) — with optimistic locking
	// TODO: on ErrEditConflict → editConflictResponse
	// TODO: return 200 with updated collection JSON

}

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
	v := validator.New()

	// Parse pagination from query string with defaults
	page := app.readIntQueryParameter(r, "page", 1, v)
	pageSize := app.readIntQueryParameter(r, "page_size", 20, v)

	filters := data.Filters{
		Page:     page,
		PageSize: pageSize,
	}

	data.ValidateFilters(v, filters)

	if !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	// TODO: userID := contextGetUser(r).ID
	// TODO: collections, metadata, err := app.models.Collections.ListByUser(userID, filters)
	// TODO: return 200 with { metadata, collections }
	_ = filters

}
