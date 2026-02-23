package main

import (
	"net/http"

	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/validator"
)

// searchCollectionHandler handles POST /v1/collections/:id/search
// Performs vector similarity search within a collection's chunks.
func (app *application) searchCollectionHandler(w http.ResponseWriter, r *http.Request) {
	collectionID, err := app.readIDParam(r)
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

	// TODO: verify collection ownership — contextGetUser(r).ID owns collectionID
	// TODO: embed the query via searchService.Embed(ctx, input.Query)
	// TODO: call chunks.SearchByEmbedding(ctx, collectionID, embedding, input.TopK)
	// TODO: return results as JSON
	_ = collectionID

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

