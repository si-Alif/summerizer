package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultEmbeddingModel        = "nomic-embed-text"
	defaultEmbeddingModelBaseURL = "http://localhost:11434"
	requestTimeout               = 90 * time.Second
	defaultNomicOnlineEndpoint   = "https://api-atlas.nomic.ai/v1/embedding/text"
	defaultNomicOnlineModel      = "nomic-embed-text-v1.5"
	defaultEmbeddingDimension    = 768

	defaultEmbedBatchSize = 32
	minEmbedBatchSize     = 1
	defaultKeepAlive      = "30m"
	defaultRetryDelay     = 500 * time.Millisecond
	defaultMaxRetryDelay  = 10 * time.Second
	coldStartThreshold    = 2 * time.Second
)

var (
	ErrEmptyInput         = errors.New("embedder: input texts cannot be empty")
	ErrBadRequestResponse = errors.New("embedder: bad request response body")
	ErrEmbeddingFailed    = errors.New("embedder: failed to get embeddings")
	ErrInvalidResponse    = errors.New("embedder: invalid response from embedding service")
	ErrFailedRequest      = errors.New("embedder: failed to make request to embedding service")
	ErrMissingNomicToken  = errors.New("embedder: missing nomic online embedding token")
)

type Embedder struct {
	httpClient       *http.Client
	model            string
	baseURL          string
	keepAlive        string
	defaultBatchSize int
	retryDelay       time.Duration
	maxRetryDelay    time.Duration
	nomicToken       string
	nomicEndpoint    string
	nomicModel       string
	nomicDimension   int
}

type Option func(*Embedder)

func WithBatchSize(n int) Option {
	return func(e *Embedder) {
		if n > 0 {
			e.defaultBatchSize = n
		}
	}
}

func WithRetryDelay(d time.Duration) Option {
	return func(e *Embedder) {
		if d > 0 {
			e.retryDelay = d
		}
	}
}

func WithMaxRetryDelay(d time.Duration) Option {
	return func(e *Embedder) {
		if d > 0 {
			e.maxRetryDelay = d
		}
	}
}

func WithKeepAlive(s string) Option {
	return func(e *Embedder) {
		if s != "" {
			e.keepAlive = s
		}
	}
}

func WithNomicOnlineToken(token string) Option {
	return func(e *Embedder) {
		e.nomicToken = strings.TrimSpace(token)
	}
}

func WithNomicOnlineModel(model string) Option {
	return func(e *Embedder) {
		if model != "" {
			e.nomicModel = model
		}
	}
}

func WithNomicOnlineEndpoint(endpoint string) Option {
	return func(e *Embedder) {
		if endpoint != "" {
			e.nomicEndpoint = endpoint
		}
	}
}

func WithNomicOnlineDimension(d int) Option {
	return func(e *Embedder) {
		if d > 0 {
			e.nomicDimension = d
		}
	}
}

type EmbeddingErrors struct {
	StatusCode int
	Err        error
}

type EmbeddingStats struct {
	TotalDuration   time.Duration `json:"total_duration"`
	LoadDuration    time.Duration `json:"load_duration"`
	PromptEvalCount int           `json:"prompt_eval_count"`
}

type BatchedEmbeddingStats struct {
	TotalDuration    time.Duration `json:"total_duration"`
	LoadDuration     time.Duration `json:"load_duration"`
	PromptEvalCount  int           `json:"prompt_eval_count"`
	Batches          int           `json:"batches"`
	Retries          int           `json:"retries"`
	FinalBatchSize   int           `json:"final_batch_size"`
	ColdStartBatches int           `json:"cold_start_batches"`
}

func (e *EmbeddingErrors) Error() string {
	if e == nil {
		return "embedder error: <nil>"
	}
	if e.Err == nil {
		return fmt.Sprintf("embedder error status=%d", e.StatusCode)
	}
	return fmt.Sprintf("embedder error status=%d: %v", e.StatusCode, e.Err)
}

func (e *EmbeddingErrors) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewEmbedder(baseURL, model string, opts ...Option) *Embedder {
	if baseURL == "" {
		baseURL = defaultEmbeddingModelBaseURL
	}
	if model == "" {
		model = defaultEmbeddingModel
	}

	e := &Embedder{
		httpClient:       &http.Client{Timeout: requestTimeout},
		model:            model,
		baseURL:          baseURL,
		keepAlive:        defaultKeepAlive,
		defaultBatchSize: defaultEmbedBatchSize,
		retryDelay:       defaultRetryDelay,
		maxRetryDelay:    defaultMaxRetryDelay,
		nomicEndpoint:    defaultNomicOnlineEndpoint,
		nomicModel:       defaultNomicOnlineModel,
		nomicDimension:   defaultEmbeddingDimension,
	}

	if e.nomicToken == "" {
		e.nomicToken = strings.TrimSpace(os.Getenv("SUMMERIZER_NOMIC_ONLINE_EMBEDDING_MODEL_TOKEN"))
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

type embedRequestBody struct {
	Model     string   `json:"model"`
	Input     []string `json:"input"`
	Truncate  *bool    `json:"truncate,omitempty"`
	KeepAlive string   `json:"keep_alive,omitempty"`
}

type embedResponse struct {
	Embeddings      [][]float32 `json:"embeddings"`
	TotalDuration   int64       `json:"total_duration,omitempty"`
	LoadDuration    int64       `json:"load_duration,omitempty"`
	PromptEvalCount int         `json:"prompt_eval_count,omitempty"`
}

type nomicOnlineEmbedRequestBody struct {
	Model          string   `json:"model"`
	Texts          []string `json:"texts"`
	TaskType       string   `json:"task_type"`
	Dimensionality int      `json:"dimensionality,omitempty"`
}

type nomicOnlineEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// GetEmbeddings is one call to Ollama /api/embed.
func (e *Embedder) GetQueryEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	embeddings, _, err := e.GetEmbeddingsWithStats(ctx, texts)
	if err != nil {
		return nil, err
	}

	return embeddings, nil
}

// GetEmbeddings provides backward-compatible access to local embeddings.
func (e *Embedder) GetEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	return e.GetQueryEmbeddings(ctx, texts)
}

// GetSearchQueryEmbedding returns one embedding vector for online query-time search.
// If a Nomic token is configured, it uses Nomic's hosted endpoint with task_type=search_query.
// Otherwise, it falls back to the local embedding endpoint.
func (e *Embedder) GetSearchQueryEmbedding(ctx context.Context, query string) ([]float32, error) {
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return nil, &EmbeddingErrors{StatusCode: http.StatusBadRequest, Err: ErrEmptyInput}
	}

	if e.nomicToken != "" {
		return e.getNomicSearchQueryEmbedding(ctx, trimmedQuery)
	}

	res, err := e.GetQueryEmbeddings(ctx, []string{trimmedQuery})
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, &EmbeddingErrors{StatusCode: http.StatusBadGateway, Err: ErrBadRequestResponse}
	}

	return res[0], nil
}

func (e *Embedder) getNomicSearchQueryEmbedding(ctx context.Context, query string) ([]float32, error) {
	if e.nomicToken == "" {
		return nil, &EmbeddingErrors{StatusCode: http.StatusUnauthorized, Err: ErrMissingNomicToken}
	}

	payload, err := json.Marshal(nomicOnlineEmbedRequestBody{
		Model:          e.nomicModel,
		Texts:          []string{"search_query: " + query},
		TaskType:       "search_query",
		Dimensionality: e.nomicDimension,
	})
	if err != nil {
		return nil, &EmbeddingErrors{StatusCode: http.StatusInternalServerError, Err: errors.Join(ErrEmbeddingFailed, err)}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.nomicEndpoint, bytes.NewBuffer(payload))
	if err != nil {
		return nil, &EmbeddingErrors{StatusCode: http.StatusInternalServerError, Err: errors.Join(ErrFailedRequest, err)}
	}

	req.Header.Set("Authorization", "Bearer "+e.nomicToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, &EmbeddingErrors{StatusCode: http.StatusServiceUnavailable, Err: errors.Join(ErrFailedRequest, err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			trimmedBody := strings.TrimSpace(string(body))
			if trimmedBody != "" {
				return nil, &EmbeddingErrors{StatusCode: resp.StatusCode, Err: fmt.Errorf("%w: %s", ErrInvalidResponse, trimmedBody)}
			}
		}
		return nil, &EmbeddingErrors{StatusCode: resp.StatusCode, Err: ErrInvalidResponse}
	}

	var embedResp nomicOnlineEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, &EmbeddingErrors{StatusCode: http.StatusBadGateway, Err: errors.Join(ErrInvalidResponse, err)}
	}

	if len(embedResp.Embeddings) != 1 {
		return nil, &EmbeddingErrors{StatusCode: http.StatusBadGateway, Err: ErrBadRequestResponse}
	}

	vector := embedResp.Embeddings[0]
	if len(vector) != e.nomicDimension {
		return nil, &EmbeddingErrors{StatusCode: http.StatusBadGateway, Err: fmt.Errorf("%w: expected %d dimensions, got %d", ErrBadRequestResponse, e.nomicDimension, len(vector))}
	}

	return vector, nil
}

func (e *Embedder) GetEmbeddingsWithStats(ctx context.Context, texts []string) ([][]float32, EmbeddingStats, error) {
	textsLen := len(texts)
	if textsLen == 0 {
		return nil, EmbeddingStats{}, &EmbeddingErrors{StatusCode: http.StatusBadRequest, Err: ErrEmptyInput}
	}

	truncate := true
	payload, err := json.Marshal(embedRequestBody{
		Model:     e.model,
		Input:     texts,
		Truncate:  &truncate,
		KeepAlive: e.keepAlive,
	})
	if err != nil {
		return nil, EmbeddingStats{}, &EmbeddingErrors{StatusCode: http.StatusInternalServerError, Err: errors.Join(ErrEmbeddingFailed, err)}
	}

	queryURL := fmt.Sprintf("%s/api/embed", e.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, queryURL, bytes.NewBuffer(payload))
	if err != nil {
		return nil, EmbeddingStats{}, &EmbeddingErrors{StatusCode: http.StatusInternalServerError, Err: errors.Join(ErrFailedRequest, err)}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, EmbeddingStats{}, &EmbeddingErrors{StatusCode: http.StatusServiceUnavailable, Err: errors.Join(ErrFailedRequest, err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, EmbeddingStats{}, &EmbeddingErrors{StatusCode: resp.StatusCode, Err: ErrInvalidResponse}
	}

	var embedResp embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, EmbeddingStats{}, &EmbeddingErrors{StatusCode: http.StatusBadGateway, Err: errors.Join(ErrInvalidResponse, err)}
	}

	if len(embedResp.Embeddings) != textsLen {
		return nil, EmbeddingStats{}, &EmbeddingErrors{StatusCode: http.StatusBadGateway, Err: ErrBadRequestResponse}
	}

	stats := EmbeddingStats{
		TotalDuration:   time.Duration(embedResp.TotalDuration),
		LoadDuration:    time.Duration(embedResp.LoadDuration),
		PromptEvalCount: embedResp.PromptEvalCount,
	}

	return embedResp.Embeddings, stats, nil
}

// GetEmbeddingsBatched batches input and applies adaptive batch-size retry strategy.
func (e *Embedder) GetEmbeddingsBatched(ctx context.Context, texts []string, batchSize int) ([][]float32, error) {
	embeddings, _, err := e.GetEmbeddingsBatchedWithStats(ctx, texts, batchSize)
	if err != nil {
		return nil, err
	}

	return embeddings, nil
}

func (e *Embedder) GetEmbeddingsBatchedWithStats(ctx context.Context, texts []string, batchSize int) ([][]float32, BatchedEmbeddingStats, error) {
	if len(texts) == 0 {
		return nil, BatchedEmbeddingStats{}, &EmbeddingErrors{StatusCode: http.StatusBadRequest, Err: ErrEmptyInput}
	}

	if batchSize <= 0 {
		batchSize = e.defaultBatchSize
	}

	originalBatch := batchSize
	currentBatch := batchSize
	all := make([][]float32, 0, len(texts))
	stats := BatchedEmbeddingStats{}
	i := 0
	retryCount := 0

	for i < len(texts) {
		select {
		case <-ctx.Done():
			return nil, stats, &EmbeddingErrors{
				StatusCode: http.StatusRequestTimeout,
				Err:        errors.Join(ErrEmbeddingFailed, ctx.Err()),
			}
		default:
		}

		if currentBatch < minEmbedBatchSize {
			currentBatch = minEmbedBatchSize
		}

		end := i + currentBatch
		if end > len(texts) {
			end = len(texts)
		}

		embeddings, oneStats, err := e.GetEmbeddingsWithStats(ctx, texts[i:end])
		if err == nil {
			all = append(all, embeddings...)
			i = end
			retryCount = 0

			stats.TotalDuration += oneStats.TotalDuration
			stats.LoadDuration += oneStats.LoadDuration
			stats.PromptEvalCount += oneStats.PromptEvalCount
			stats.Batches++
			if oneStats.LoadDuration >= coldStartThreshold {
				stats.ColdStartBatches++
			}

			if currentBatch < originalBatch {
				currentBatch = min(originalBatch, currentBatch*2)
			}
			continue
		}

		if currentBatch == minEmbedBatchSize || !isRetryableEmbeddingError(err) {
			return nil, stats, err
		}

		currentBatch = max(minEmbedBatchSize, currentBatch/2)
		stats.Retries++
		retryCount++
		delay := e.nextRetryDelay(retryCount)

		select {
		case <-ctx.Done():
			return nil, stats, &EmbeddingErrors{
				StatusCode: http.StatusRequestTimeout,
				Err:        errors.Join(ErrEmbeddingFailed, ctx.Err()),
			}
		case <-time.After(delay):
		}
	}

	if len(all) != len(texts) {
		return nil, stats, &EmbeddingErrors{StatusCode: http.StatusBadGateway, Err: ErrBadRequestResponse}
	}

	stats.FinalBatchSize = currentBatch
	return all, stats, nil
}

func (e *Embedder) nextRetryDelay(retryCount int) time.Duration {
	if retryCount <= 0 {
		return e.retryDelay
	}

	exp := retryCount - 1
	if exp > 16 {
		exp = 16
	}

	delay := e.retryDelay * time.Duration(1<<exp)
	delay = min(delay, e.maxRetryDelay)

	jitterWindow := delay / 5
	if jitterWindow > 0 {
		jitter := time.Duration(rand.Int63n(int64(jitterWindow) + 1))
		delay += jitter
		delay = min(delay, e.maxRetryDelay)
	}

	return delay
}

func isRetryableEmbeddingError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}

	var embeddingErr *EmbeddingErrors
	if errors.As(err, &embeddingErr) {
		switch embeddingErr.StatusCode {
		case http.StatusRequestTimeout,
			http.StatusTooManyRequests,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		default:
			return false
		}
	}

	return false
}
