package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	defaultEmbeddingModel = "nomic-embed-text"
	defaultEmbeddingModelBaseURL = "http://localhost:11434"
	requestTimeout = 60 * time.Second
)

var (
	ErrBadRequestResponse = errors.New("embedder: bad request response body")
	ErrEmbeddingFailed = errors.New("embedder: failed to get embeddings")
	ErrInvalidResponse = errors.New("embedder: invalid response from embedding service")
	ErrFailedRequest = errors.New("embedder: failed to make request to embedding service")
)

type Embedder struct {
	httpClient *http.Client
	model string
	baseURL string
}

type EmbeddeingErrors struct {
	StatusCode int
	Err error
}

func NewEmbedder(baseURL, model string) *Embedder {

	if baseURL == "" {
		baseURL = defaultEmbeddingModelBaseURL
	}

	if model == "" {
		model = defaultEmbeddingModel
	}

	return &Embedder{
		httpClient: &http.Client{Timeout: requestTimeout},
		model: model,
		baseURL: baseURL,
	}
}

type embedRequestBody struct {
	Model string `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (e *Embedder) GetEmbeddings(ctx context.Context, texts []string) ([][]float32, *EmbeddeingErrors) {
	texts_len := len(texts)

	if texts_len == 0 {
		return nil, &EmbeddeingErrors{StatusCode: http.StatusBadRequest, Err: ErrBadRequestResponse	}
	}

	payload , err := json.Marshal(embedRequestBody{
		Model: e.model,
		Input: texts,
	})

	if err != nil {
		return nil, &EmbeddeingErrors{StatusCode: http.StatusBadRequest, Err: ErrBadRequestResponse}
	}

	queryURL := fmt.Sprintf("%s/api/embed", e.baseURL)
	req , err := http.NewRequestWithContext(ctx , "POST", queryURL, bytes.NewBuffer(payload))

	if err != nil {
		return nil, &EmbeddeingErrors{StatusCode: http.StatusBadRequest, Err: ErrFailedRequest}
	}

	req.Header.Set("Content-Type", "application/json")

	resp , err := e.httpClient.Do(req)

	if err != nil {
		return nil, &EmbeddeingErrors{StatusCode: http.StatusBadRequest, Err: ErrFailedRequest}
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &EmbeddeingErrors{StatusCode: resp.StatusCode, Err: ErrInvalidResponse}
	}

	var embedResp embedResponse

	err = json.NewDecoder(resp.Body).Decode(&embedResp)

	if err != nil {
		return nil, &EmbeddeingErrors{StatusCode: http.StatusBadRequest, Err: ErrInvalidResponse}
	}

	if len(embedResp.Embeddings) != texts_len {
		return nil, &EmbeddeingErrors{StatusCode: http.StatusBadRequest, Err : ErrBadRequestResponse}
	}

	return embedResp.Embeddings, nil
}