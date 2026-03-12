package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/ingestion/embedder"
	"github.com/si-Alif/summerizer/internal/validator"
)

// searchCollectionHandler handles POST /v1/collections/:id/search
// Performs vector similarity search within a collection's chunks.
func (app *application) searchCollectionHandler(w http.ResponseWriter, r *http.Request) {
	collectionID, err := app.readIDParam(r)

	userID := app.GetUserFromSubsequentRequestContext(r).ID

	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	var input data.SearchInput

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	// Default top_k if not provided
	if input.TopK == 0 {
		input.TopK = 5
	}

	v := validator.New()
	if data.ValidateSearchInput(v, &input); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	collection, err := app.models.Collections.GetByID(collectionID, userID)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(w, r)
		default:
			app.serverErrorResponse(w, r, err)
		}
		return
	}

	embedding, err := embedQuery(input.Query, app.embedder)

	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	results, err := app.models.Chunks.SearchByVector(collection.ID, embedding, input.TopK )

	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	err = app.writeJSON(w, http.StatusOK, envelop{"results": results}, nil)

}

// askCollectionHandler handles POST /v1/collections/:id/ask
// Full RAG flow: embed question → retrieve chunks → LLM answer.
func (app *application) askCollectionHandler(w http.ResponseWriter, r *http.Request) {
	collectionID, err := app.readIDParam(r)
	if err != nil {
		app.notFoundResponse(w, r)
		return
	}

	var input data.AskInput

	err = app.readJSON(w, r, &input)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	// Default top_k if not provided — 5 chunks is a reasonable LLM context size
	if input.TopK == 0 {
		input.TopK = 5
	}

	v := validator.New()
	if data.ValidateAskInput(v, &input); !v.Valid() {
		app.failedValidationResponse(w, r, v.Errors)
		return
	}

	// TODO: verify collection ownership — contextGetUser(r).ID owns collectionID
	// TODO: call searchService.Ask(ctx, collectionID, input.Question, input.TopK)
	// TODO: return { answer, sources } as JSON
	_ = collectionID

}


func embedQuery(query string, embedder *embedder.Embedder) ([]float32, error) {
	ctx , cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	text := make([]string , 1)
	text[0] = query

	res , err := embedder.GetEmbeddings(ctx , text)
	if err != nil {
		return nil, err
	}

	return res[0], nil
}

