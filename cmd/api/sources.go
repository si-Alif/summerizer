package main

// import (
// 	"net/http"
// 	"time"

// 	"github.com/si-Alif/summerizer/internal/data"
// 	"github.com/si-Alif/summerizer/internal/validator"
// )

// func (app *application) showSourceHandler(w http.ResponseWriter, r *http.Request) {
// 	id, err := app.readIDParam(r)
// 	if err != nil {
// 		app.notFoundResponse(w, r)
// 		return
// 	}

// 	// TODO: replace with app.models.Sources.Get(id) once DB is wired
// 	// TODO: verify that source.CollectionID belongs to a collection owned by contextGetUser(r)
// 	//   → notFoundResponse if not owner
// 	// TODO: if source.Status == "ingesting" or "pending", consider resourceNotReadyResponse

// 	step := "fetch"
// 	errMsg := ""

// 	source := data.Source{
// 		ID:           id,
// 		CollectionID: 1,
// 		SourceType:   "web",
// 		URL:          "https://example.com/article",
// 		Title:        "Example Article",
// 		Status:       "ingesting",
// 		CurrentStep:  &step,
// 		StepError:    &errMsg,
// 		RetryCount:   0,
// 		NextRetryAt:  nil,
// 		Metadata:     data.JsonMap{},
// 		CreatedAt:    time.Now(),
// 		UpdatedAt:    time.Now(),
// 		Version:      1,
// 	}

// 	err = app.writeJSON(w, http.StatusOK, envelop{"source": source}, nil)
// 	if err != nil {
// 		app.serverErrorResponse(w, r, err)
// 	}
// }

// // todo : implement createSourceHandler with input validation and error handling
// func (app *application) createSourceHandler(w http.ResponseWriter, r *http.Request) {
// 	// The collection ID comes from the route: /v1/collections/:id/sources
// 	collectionID, err := app.readIDParam(r)
// 	if err != nil {
// 		app.notFoundResponse(w, r)
// 		return
// 	}

// 	var input struct {
// 		URL string `json:"url"`
// 	}

// 	err = app.readJSON(w, r, &input)
// 	if err != nil {
// 		app.badRequestResponse(w, r, err)
// 		return
// 	}

// 	// Auto-detect source type from the URL pattern
// 	sourceType := validator.DetectSourceType(input.URL)

// 	source := &data.Source{
// 		CollectionID: collectionID,
// 		URL:          input.URL,
// 		SourceType:   sourceType,
// 		Status:       "pending",
// 	}

// 	v := validator.New()
// 	if data.ValidateSource(v, source); !v.Valid() {
// 		app.failedValidationResponse(w, r, v.Errors)
// 		return
// 	}

// 	// TODO: verify collection ownership — contextGetUser(r).ID owns collectionID
// 	// TODO: check source count per collection (max 50) before insert
// 	// TODO: check for duplicate URL within same collection
// 	// TODO: app.models.Sources.Insert(source)
// 	// TODO: return 202 Accepted with source JSON

// }

// // listSourcesHandler handles GET /v1/collections/:id/sources?page=1&page_size=20&status=completed
// func (app *application) listSourcesHandler(w http.ResponseWriter, r *http.Request) {
// 	collectionID, err := app.readIDParam(r)
// 	if err != nil {
// 		app.notFoundResponse(w, r)
// 		return
// 	}

// 	v := validator.New()

// 	// Parse pagination
// 	page := app.readIntQueryParameter(r, "page", 1, v)
// 	pageSize := app.readIntQueryParameter(r, "page_size", 20, v)

// 	filters := data.Filters{
// 		Page:     page,
// 		PageSize: pageSize,
// 	}

// 	data.ValidateFilters(v, filters)

// 	// Optional status filter — if provided, must be a valid status
// 	status := app.readStringQueryParameter(r, "status", "")
// 	if status != "" {
// 		v.Check(
// 			validator.PermittedValue(status, data.PermittedStatuses...),
// 			"status",
// 			"must be one of: pending, ingesting, completed, failed, stale",
// 		)
// 	}

// 	if !v.Valid() {
// 		app.failedValidationResponse(w, r, v.Errors)
// 		return
// 	}

// 	// TODO: verify collection ownership — contextGetUser(r).ID owns collectionID
// 	// TODO: sources, metadata, err := app.models.Sources.ListByCollection(collectionID, filters, status)
// 	// TODO: return 200 with { metadata, sources }
// 	_ = collectionID
// 	_ = status

// }

// // deleteSourceHandler handles DELETE /v1/sources/:id
// func (app *application) deleteSourceHandler(w http.ResponseWriter, r *http.Request) {
// 	id, err := app.readIDParam(r)
// 	if err != nil {
// 		app.notFoundResponse(w, r)
// 		return
// 	}

// 	// TODO: fetch source, verify ownership via collection → contextGetUser(r).ID
// 	// TODO: app.models.Sources.Delete(id)
// 	// TODO: on ErrRecordNotFound → notFoundResponse
// 	_ = id

// }
