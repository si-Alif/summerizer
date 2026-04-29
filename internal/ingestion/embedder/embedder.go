package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// ollama specific
	defaultOllamaEmbeddingModel = "nomic-embed-text"
	defaultOllamaModelBaseURL   = "http://localhost:11434"
	defaultKeepAlive            = "30m"

	// nomic online specific
	defaultNomicOnlineEndpoint = "https://api-atlas.nomic.ai/v1/embedding/text"
	defaultNomicOnlineModel    = "nomic-embed-text-v1.5"
	defaultEmbeddingDimension  = 768

	// shared defaults
	defaultEmbedBatchSize = 32
	minEmbedBatchSize     = 1
	defaultRetryDelay     = 500 * time.Millisecond
	defaultMaxRetryDelay  = 10 * time.Second

	// cold start threshold
	coldStartDurationThreshold = 5 * time.Second

	// HTTP client threshold
	dialTimeout           = 5 * time.Second
	tlsHandshakeTimeout   = 5 * time.Second
	responseHeaderTimeout = 10 * time.Second
	idleConnTimeout       = 60 * time.Second
	requestTimeout        = 90 * time.Second
)

var (
	ErrEmptyInput               = errors.New("embedder: input texts cannot be empty")
	ErrBadRequestResponse       = errors.New("embedder: bad request response body")
	ErrEmbeddingFailed          = errors.New("embedder: failed to get embeddings")
	ErrInvalidResponse          = errors.New("embedder: invalid response from embedding service")
	ErrFailedRequest            = errors.New("embedder: failed to make request to embedding service")
	ErrMissingNomicToken        = errors.New("embedder: missing nomic online embedding token")
	ErrUnknownEmbeddingInstance = errors.New("embedder: unknown embedding instance")
)

// Error
type EmbeddingErrors struct {
	StatusCode int
	Err        error
}

func (e *EmbeddingErrors) Error() string {
	if e == nil {
		return "embedder error: <nil>"
	}
	if e.Err == nil {
		return fmt.Sprintf("embedder error: status code %d, <nil error>", e.StatusCode)
	}

	return fmt.Sprintf("embedder error: status code %d, error: %v", e.StatusCode, e.Err)
}

func (e *EmbeddingErrors) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
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

type embedderInstance interface {
	embed(ctx context.Context, texts []string, taskType string) ([][]float32, *EmbeddingStats, error)
}

//--------------------------------------------
// ollama embedder specific types and functions
//--------------------------------------------

type ollmaEmbedder struct {
	httpClient *http.Client
	baseURL    string
	modelName  string
	keepAlive  string
}

type ollamaEmbeddingRequest struct {
	Model     string   `json:"model"`
	Input     []string `json:"input"`
	Truncate  bool     `json:"truncate,omitempty"`
	KeepAlive string   `json:"keep_alive,omitempty"`
}

type ollamaEmbeddingResponse struct {
	Embeddings      [][]float32   `json:"embeddings"`
	TotalDuration   time.Duration `json:"total_duration,omitempty"`
	LoadDuration    time.Duration `json:"load_duration,omitempty"`
	PromptEvalCount int           `json:"prompt_eval_count,omitempty"`
}

func (oe *ollmaEmbedder) embed(ctx context.Context, texts []string, taskType string) ([][]float32, *EmbeddingStats, error) {
	if len(texts) == 0 {
		return nil, nil, ErrEmptyInput
	}

	toTruncate := true

	reqBody := ollamaEmbeddingRequest{
		Model:     oe.modelName,
		Input:     texts,
		KeepAlive: oe.keepAlive,
		Truncate:  toTruncate,
	}

	jsonInpData, err := json.Marshal(reqBody)

	if err != nil {
		return nil, nil, &EmbeddingErrors{
			StatusCode: http.StatusInternalServerError,
			Err:        errors.Join(ErrEmbeddingFailed, err),
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/embed", oe.baseURL), bytes.NewBuffer(jsonInpData))

	if err != nil {
		return nil, nil, &EmbeddingErrors{
			StatusCode: http.StatusInternalServerError,
			Err:        errors.Join(ErrFailedRequest, err),
		}
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := oe.httpClient.Do(req)

	if err != nil {
		return nil, nil, &EmbeddingErrors{
			StatusCode: http.StatusServiceUnavailable,
			Err:        errors.Join(ErrFailedRequest, err),
		}
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, &EmbeddingErrors{
			StatusCode: resp.StatusCode,
			Err:        errors.Join(ErrInvalidResponse, err),
		}
	}

	var embeddingResp ollamaEmbeddingResponse

	// use decoder to decode as it's more efficient for streaming / large response body
	if err := json.NewDecoder(resp.Body).Decode(&embeddingResp); err != nil {
		return nil, nil, &EmbeddingErrors{
			StatusCode: http.StatusBadGateway,
			Err:        errors.Join(ErrInvalidResponse, err),
		}
	}

	if err != nil {
		return nil, nil, &EmbeddingErrors{
			StatusCode: http.StatusBadGateway,
			Err:        errors.Join(ErrInvalidResponse, err),
		}
	}

	if len(embeddingResp.Embeddings) != len(texts) {
		return nil, &EmbeddingStats{}, &EmbeddingErrors{
			StatusCode: http.StatusBadGateway,
			Err:        errors.Join(ErrInvalidResponse, fmt.Errorf("mismatched embedding count")),
		}
	}

	stats := &EmbeddingStats{
		TotalDuration:   time.Duration(embeddingResp.TotalDuration),
		LoadDuration:    time.Duration(embeddingResp.LoadDuration),
		PromptEvalCount: embeddingResp.PromptEvalCount,
	}

	return embeddingResp.Embeddings, stats, nil
}

//--------------------------------------------
// nomic online embedder specific types and functions
//--------------------------------------------

type nomicOnlineEmbedder struct {
	httpClient *http.Client
	endpoint   string
	modelName  string
	token      string
	dimension  int
}

type nomicEmbedRequest struct {
	Model          string   `json:"model"`
	Texts          []string `json:"texts"`
	TaskType       string   `json:"task_type"`
	Dimensionality int      `json:"dimensionality,omitempty"`
}

type nomicEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (ne *nomicOnlineEmbedder) embed(ctx context.Context, texts []string, taskType string) ([][]float32, *EmbeddingStats, error) {
	switch {
	case len(texts) == 0:
		return nil, nil, &EmbeddingErrors{
			StatusCode: http.StatusBadRequest,
			Err:        ErrEmptyInput,
		}
	case ne.token == "":
		return nil, nil, &EmbeddingErrors{
			StatusCode: http.StatusUnauthorized,
			Err:        ErrMissingNomicToken,
		}
	}

	reqBody := nomicEmbedRequest{
		Model:          ne.modelName,
		Texts:          texts,
		TaskType:       taskType,
		Dimensionality: ne.dimension,
	}

	jsonInpData, err := json.Marshal(reqBody)

	if err != nil {
		return nil, nil, &EmbeddingErrors{
			StatusCode: http.StatusInternalServerError,
			Err:        errors.Join(ErrEmbeddingFailed, err),
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ne.endpoint, bytes.NewBuffer(jsonInpData))

	if err != nil {
		return nil, nil, &EmbeddingErrors{
			StatusCode: http.StatusInternalServerError,
			Err:        errors.Join(ErrFailedRequest, err),
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ne.token))

	resp, err := ne.httpClient.Do(req)

	if err != nil {
		return nil, nil, &EmbeddingErrors{
			StatusCode: http.StatusServiceUnavailable,
			Err:        errors.Join(ErrFailedRequest, err),
		}
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, &EmbeddingErrors{
			StatusCode: resp.StatusCode,
			Err:        errors.Join(ErrInvalidResponse, err),
		}
	}

	var embeddingResp nomicEmbedResponse

	// use decoder to decode as it's more efficient for streaming / large response body
	if err := json.NewDecoder(resp.Body).Decode(&embeddingResp); err != nil {
		return nil, nil, &EmbeddingErrors{
			StatusCode: http.StatusBadGateway,
			Err:        errors.Join(ErrInvalidResponse, err),
		}
	}

	if len(embeddingResp.Embeddings) != len(texts) {
		return nil, &EmbeddingStats{}, &EmbeddingErrors{
			StatusCode: http.StatusBadGateway,
			Err:        errors.Join(ErrInvalidResponse, fmt.Errorf("mismatched embedding count")),
		}
	}

	for _, vec := range embeddingResp.Embeddings {
		if len(vec) != ne.dimension {
			return nil, &EmbeddingStats{}, &EmbeddingErrors{
				StatusCode: http.StatusBadGateway,
				Err:        fmt.Errorf("%w: expected %d dimensions, got %d", ErrBadRequestResponse, ne.dimension, len(vec)),
			}
		}
	}

	return embeddingResp.Embeddings, &EmbeddingStats{}, nil
}

// Define EmbedderType to distinguish between different embedding service instances
type EmbedderType string

const (
	OllamaEmbedderType      EmbedderType = "ollama"
	NomicOnlineEmbedderType EmbedderType = "nomic"
)

type Embedder struct {
	embedder         embedderInstance
	defaultBatchSize int
	retryDelay       time.Duration
	maxRetryDelay    time.Duration
}

type config struct {
	batchSize     int
	retryDelay    time.Duration
	maxRetryDelay time.Duration

	// ollama
	ollamaBaseURL string
	ollamaModel   string
	keepAlive     string

	// nomic
	nomicToken     string
	nomicEndpoint  string
	nomicModel     string
	nomicDimension int

	// HTTP transport tuning
	maxIdleConnsPerHost int
}

type Option func(*config)

func WithBatchSize(batchSize int) Option {
	return func(c *config) {
		if batchSize > 0 {
			c.batchSize = batchSize
		}
	}
}

func WithRetryDelay(retryDelay time.Duration) Option {
	return func(c *config) {
		if retryDelay > 0 {
			c.retryDelay = retryDelay
		}
	}
}

func WithMaxRetryDelay(maxRetryDelay time.Duration) Option {
	return func(c *config) {
		if maxRetryDelay > 0 {
			c.maxRetryDelay = maxRetryDelay
		}
	}
}

func WithOllamaBaseURL(URL string) Option {
	return func(c *config) {
		if URL != "" {
			c.ollamaBaseURL = URL
		}
	}
}

func WithKeepAlive(keepAlive string) Option {
	return func(c *config) {
		if keepAlive != "" {
			c.keepAlive = keepAlive
		}
	}
}

func WithOllamaModel(model string) Option {
	return func(c *config) {
		if model != "" {
			c.ollamaModel = model
		}
	}
}

// to overwrite default token provided via environment variable
func WithNomicToken(token string) Option {
	return func(c *config) {
		if token != "" {
			c.nomicToken = token
		}
	}
}

func WithNomicEndpoint(endpoint string) Option {
	return func(c *config) {
		if endpoint != "" {
			c.nomicEndpoint = endpoint
		}
	}
}

func WithNomicModel(model string) Option {
	return func(c *config) {
		if model != "" {
			c.nomicModel = model
		}
	}
}

func WithNomicDimension(dimension int) Option {
	return func(c *config) {
		if dimension > 0 {
			c.nomicDimension = dimension
		}
	}
}

func WithMaxIdleConnsPerHost(maxIdleConnsPerHost int) Option {
	return func(c *config) {
		if maxIdleConnsPerHost > 0 {
			c.maxIdleConnsPerHost = maxIdleConnsPerHost
		}
	}
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewEmbedder builds an Embedder backed by the requested backend.
//
// Example — Nomic :
//
//	e, err := embedder.NewEmbedder(embedder.BackendNomic,
//	    embedder.WithNomicToken(cfg.NomicToken),
//	    embedder.WithMaxIdleConnsPerHost(cfg.EmbeddingWorkerCount),
//	)
//
// Example — Ollama :
//
//	e, err := embedder.NewEmbedder(embedder.BackendOllama,
//	    embedder.WithOllamaBaseURL("http://localhost:11434"),
//	    embedder.WithMaxIdleConnsPerHost(cfg.EmbeddingWorkerCount),
//	)
func NewEmbedder(embedderType EmbedderType, opts ...Option) (*Embedder, error) {
	cfg := &config{
		batchSize:           defaultEmbedBatchSize,
		retryDelay:          defaultRetryDelay,
		maxRetryDelay:       defaultMaxRetryDelay,
		ollamaBaseURL:       defaultOllamaModelBaseURL,
		ollamaModel:         defaultOllamaEmbeddingModel,
		keepAlive:           defaultKeepAlive,
		nomicEndpoint:       defaultNomicOnlineEndpoint,
		nomicModel:          defaultNomicOnlineModel,
		nomicDimension:      defaultEmbeddingDimension,
		maxIdleConnsPerHost: 8, // default value for max idle connections per host
	}

	cfg.nomicToken = strings.TrimSpace(os.Getenv("SUMMERIZER_NOMIC_ONLINE_EMBEDDING_MODEL_TOKEN"))

	// all those option functions returns an anonymous function which takes in the config struct and updates the relevant fields if the provided value is valid
	for _, opt := range opts {
		opt(cfg)
	}

	transport := buildTransport(cfg.maxIdleConnsPerHost)
	httpClient := &http.Client{Transport: transport, Timeout: requestTimeout}

	var instance embedderInstance
	switch embedderType {
	case OllamaEmbedderType:
		instance = &ollmaEmbedder{
			httpClient: httpClient,
			baseURL:    cfg.ollamaBaseURL,
			modelName:  cfg.ollamaModel,
			keepAlive:  cfg.keepAlive,
		}
	case NomicOnlineEmbedderType:
		instance = &nomicOnlineEmbedder{
			httpClient: httpClient,
			endpoint:   cfg.nomicEndpoint,
			modelName:  cfg.nomicModel,
			token:      cfg.nomicToken,
			dimension:  cfg.nomicDimension,
		}
	default:
		return nil, ErrUnknownEmbeddingInstance
	}

	return &Embedder{
		embedder:         instance,
		defaultBatchSize: cfg.batchSize,
		retryDelay:       cfg.retryDelay,
		maxRetryDelay:    cfg.maxRetryDelay,
	}, nil

}

func buildTransport(maxIdleConnsPerHost int) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          max(64, maxIdleConnsPerHost*4),
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		IdleConnTimeout:       idleConnTimeout,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
}

// ------------------------------------------
// Public API
// ------------------------------------------

func (e *Embedder) GetSearchQueryEmbedding(ctx context.Context, query string) ([]float32, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, &EmbeddingErrors{StatusCode: http.StatusBadRequest, Err: ErrEmptyInput}
	}

	embeddings, _, err := e.embedder.embed(ctx, []string{query}, "search_query")
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, &EmbeddingErrors{StatusCode: http.StatusBadGateway, Err: ErrBadRequestResponse}
	}

	return embeddings[0], nil
}

func (e *Embedder) GetEmbeddingsBatched(ctx context.Context, texts []string, batchSize int) ([][]float32, error) {
	embeddings, _, err := e.GetEmbeddingsBatchedWithStats(ctx, texts, batchSize)
	return embeddings, err
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

		// Chunk embedding always uses search_document task type so Nomic
		// applies the correct asymmetric projection for the indexed side.
		embeddings, oneStats, err := e.embedder.embed(ctx, texts[i:end], "search_document")
		if err == nil {
			all = append(all, embeddings...)
			i = end
			retryCount = 0

			stats.TotalDuration += oneStats.TotalDuration
			stats.LoadDuration += oneStats.LoadDuration
			stats.PromptEvalCount += oneStats.PromptEvalCount
			stats.Batches++
			if oneStats.LoadDuration >= coldStartDurationThreshold {
				stats.ColdStartBatches++
			}

			// Recover toward original batch size after a successful smaller batch.
			if currentBatch < originalBatch {
				currentBatch = min(originalBatch, currentBatch*2)
			}
			continue
		}

		if currentBatch == minEmbedBatchSize || !isRetryableEmbeddingError(err) {
			return nil, stats, err
		}

		// Halve the batch and retry — this handles OOM / rate-limit spikes.
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

	var embErr *EmbeddingErrors
	if errors.As(err, &embErr) {
		switch embErr.StatusCode {
		case http.StatusRequestTimeout,
			http.StatusTooManyRequests,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		}
	}

	return false
}
