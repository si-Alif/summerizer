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
	requestTimeout = 500 * time.Second
)

var (
	ErrEmptyInput = errors.New("embedder: input texts cannot be empty")
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

type EmbeddingErrors struct {
	StatusCode int
	Err error
}


// Error method helps the EmbeddingErrors type to satisfy the error interface, allowing it to be used as an "error" in Go . It provides a string representation of the error, including the status code and the underlying error message if available.
func (e *EmbeddingErrors) Error() string {
	if e == nil {
		return "embedder error: <nil>"
	}

	if e.Err == nil {
		return fmt.Sprintf("embedder error status=%d", e.StatusCode)
	}

	return fmt.Sprintf("embedder error status=%d: %v", e.StatusCode, e.Err)
}

// Unwrap method allows you to retrieve the underlying error wrapped by the EmbeddingErrors type. This is useful for error handling and allows you to check for specific error types using errors.Is()
func (e *EmbeddingErrors) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Err
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

func (e *Embedder) GetEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	texts_len := len(texts)

	if texts_len == 0 {
		return nil, &EmbeddingErrors{StatusCode: http.StatusBadRequest, Err: ErrEmptyInput}
	}

	payload , err := json.Marshal(embedRequestBody{
		Model: e.model,
		Input: texts,
	})

	if err != nil {
		return nil, &EmbeddingErrors{StatusCode: http.StatusInternalServerError, Err: errors.Join(ErrEmbeddingFailed, err)}
	}

	queryURL := fmt.Sprintf("%s/api/embed", e.baseURL)
	req , err := http.NewRequestWithContext(ctx , "POST", queryURL, bytes.NewBuffer(payload))

	if err != nil {
		return nil, &EmbeddingErrors{StatusCode: http.StatusInternalServerError, Err: errors.Join(ErrFailedRequest, err)}
	}

	req.Header.Set("Content-Type", "application/json")

	resp , err := e.httpClient.Do(req)

	if err != nil {
		return nil, &EmbeddingErrors{StatusCode: http.StatusServiceUnavailable, Err: errors.Join(ErrFailedRequest, err)}
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &EmbeddingErrors{StatusCode: resp.StatusCode, Err: ErrInvalidResponse}
	}

	var embedResp embedResponse

	err = json.NewDecoder(resp.Body).Decode(&embedResp)

	if err != nil {
		return nil, &EmbeddingErrors{StatusCode: http.StatusBadGateway, Err: errors.Join(ErrInvalidResponse, err)}
	}

	if len(embedResp.Embeddings) != texts_len {
		return nil, &EmbeddingErrors{StatusCode: http.StatusBadGateway, Err: ErrBadRequestResponse}
	}

	return embedResp.Embeddings, nil
}