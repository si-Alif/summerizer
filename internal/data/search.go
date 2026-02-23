package data

import "github.com/si-Alif/summerizer/internal/validator"

//represents vector-search request body. Based on the query , vector search will be done in DB and top_k results will be returned.
type SearchInput struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}


// represents the RAG endpoint request body. Similar to SearchInput but with a tighter cap on top_k since more chunks = more tokens = higher cost and risk of exceeding LLM limits
type AskInput struct {
	Question string `json:"question"`
	TopK     int    `json:"top_k"` // lesser count than SearchInput to control LLM context window size
}


// validates the search related endpoint request query params.
func ValidateSearchInput(v *validator.Validator, input *SearchInput ) {
	v.Check(validator.NotBlank(input.Query), "query", "must be provided")
	v.Check(len(input.Query) <= 2000, "query", "must not be more than 2000 characters")

	// by default return top 5 results
	v.Check(input.TopK >= 1, "top_k", "must be at least 1")
	v.Check(input.TopK <= 20, "top_k", "must not be greater than 20")
}

func ValidateAskInput(v *validator.Validator, input *AskInput) {
	v.Check(validator.NotBlank(input.Question), "question", "must be provided")
	v.Check(len(input.Question) <= 2000, "question", "must not be more than 2000 characters")

	// by default return top 5 results
	v.Check(input.TopK >= 1, "top_k", "must be at least 1")
	v.Check(input.TopK <= 10, "top_k", "must not be greater than 10") // tighter cap for RAG to control LLM context size
}


