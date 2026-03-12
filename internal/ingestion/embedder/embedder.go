package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	defaultEmbeddingModel = "nomic-embed-text"
	defaultEmbeddingModelBaseURL = "http://localhost:11434"
	requestTimeout = 60 * time.Second
)

type Embedder struct {
	httpClient *http.Client
	model string
	baseURL string
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
		return nil, nil
	}

	payload , err := json.Marshal(embedRequestBody{
		Model: e.model,
		Input: texts,
	})

	if err != nil {
		return nil, fmt.Errorf("embedder: marshal request: %w", err)
	}

	queryURL := fmt.Sprintf("%s/api/embed", e.baseURL)
	req , err := http.NewRequestWithContext(ctx , "POST", queryURL, bytes.NewBuffer(payload))

	if err != nil {
		return nil, fmt.Errorf("embedder: creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp , err := e.httpClient.Do(req)

	if err != nil {
		return nil, fmt.Errorf("embedder: making request: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedder: non-200 status code: %d", resp.StatusCode)
	}

	var embedResp embedResponse

	err = json.NewDecoder(resp.Body).Decode(&embedResp)

	if err != nil {
		return nil, fmt.Errorf("embedder: decoding response: %w", err)
	}

	if len(embedResp.Embeddings) != texts_len {
		return nil, fmt.Errorf("embedder: response embeddings length mismatch: got %d, expected %d", len(embedResp.Embeddings), texts_len)
	}

	return embedResp.Embeddings, nil
}