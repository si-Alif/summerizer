package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/ingestion/chunker"
	"github.com/si-Alif/summerizer/internal/ingestion/cleaner"
	"github.com/si-Alif/summerizer/internal/ingestion/fetcher"
)

type Pipeline struct {
	fetcher *fetcher.Fetcher
	chunker *chunker.Chunker
	models  data.Models
	logger  *slog.Logger
}

var (
	ErrNoContentToChunk  = errors.New("no content extracted")
	ErrNoChunksGenerated = errors.New("chunker returned no chunks")
)

func NewPipeline(
	models data.Models,
	logger *slog.Logger,
	f *fetcher.Fetcher,
	c *chunker.Chunker,
) *Pipeline {
	return &Pipeline{
		fetcher: f,
		chunker: c,
		models:  models,
		logger:  logger,
	}
}

type FailureDecision struct {
	NonRetryable bool
	Reason       string
}

// Process runs the full ingestion pipeline for a single source.
//
// Flow:
//  1. fetch  — download HTML from source URL
//  2. clean  — extract section-aware content blocks from HTML
//  3. chunk  — split blocks into token-sized chunks with overlap
//  4. store  — delete old chunks (if re-ingesting), bulk insert new ones
//  5. embed  — enqueue async embedding work for worker pool
//
// On error at any step:
//   - source.status = "failed"
//   - source.step_error = error message
//   - source.current_step = the step that failed
//   - retry_count is incremented (handled by caller/worker)
//
// context : t(s) context window (90s by default) to limit the total processing time for a source, including retries
func (p *Pipeline) ProcessSource(ctx context.Context, source *data.Source) error {
	log := p.logger.With("source_id", source.ID, "url", source.URL)
	pipelineStartedAt := time.Now()

	log.Info("Starting ingestion pipeline")

	// Step 1: Fetch
	log.Info("Fetching content")

	newVersion, err := p.models.Sources.UpdateStatus(source.ID, "ingesting", "fetch", source.Version)
	if err != nil {
		if errors.Is(err, data.ErrEditConflict) {
			return errors.Join(fmt.Errorf("edit conflict when updating source status to fetch for source %d", source.ID), data.ErrEditConflict)
		}
		p.failSource(source, "fetch", err)
		return fmt.Errorf("update step to fetch for source %d: %w", source.ID, err)
	}
	source.Version = newVersion

	fetchStartedAt := time.Now()
	rawContent, err := p.fetcher.Fetch(ctx, source.URL)
	if err != nil {
		p.failSource(source, "fetch", err)
		return fmt.Errorf("fetching content: %w", err)
	}

	log.Info("pipeline: fetched",
		"title", rawContent.Title,
		"chars", len(rawContent.TextContent),
		"duration_ms", time.Since(fetchStartedAt).Milliseconds(),
	)

	// Step 2: Clean
	log.Info("Cleaning content")

	cleanStartedAt := time.Now()
	newVersion, err = p.models.Sources.UpdateStatus(source.ID, "ingesting", "clean", source.Version)

	if err != nil {
		if errors.Is(err, data.ErrEditConflict) {
			return errors.Join(fmt.Errorf("edit conflict when updating source status to clean for source %d", source.ID), data.ErrEditConflict)
		}
		err := fmt.Errorf("update step to clean for source %d: %w", source.ID, err)
		p.failSource(source, "clean", err)
		return err
	}
	source.Version = newVersion

	blocks, cleanMethod, err := cleaner.ExtractBlocksWithMethod(ctx, rawContent.HTMLContent)

	if err != nil || len(blocks) == 0 {
		switch {
		case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
			p.failSource(source, "clean", err)
			return fmt.Errorf("cleaning content: %w", err)
		case err != nil:
			log.Warn("pipeline: HTML extraction failed, falling back to plain text", "error", err)
		default:
			log.Warn("pipeline: HTML extraction returned no blocks, falling back to plain text")
		}

		cleanMethod = cleaner.MethodPlainText
		blocks = cleaner.FromPlainText(rawContent.TextContent)
	}

	if len(blocks) > 2000 {
		log.Warn("pipeline: extracted a large number of blocks, which may indicate an issue with the HTML structure or extraction logic . Falling back to plain text", "block_count", len(blocks))
		cleanMethod = cleaner.MethodPlainText
		blocks = cleaner.FromPlainText(rawContent.TextContent)
	}

	if len(blocks) == 0 {
		p.failSource(source, "clean", ErrNoContentToChunk)
		return fmt.Errorf("clean failed: %w", ErrNoContentToChunk)
	}

	log.Info("pipeline: cleaned",
		"method", cleanMethod,
		"blocks", len(blocks),
		"duration_ms", time.Since(cleanStartedAt).Milliseconds(),
	)

	// --- Step 3: CHUNK ---
	log.Info("pipeline: chunking")

	chunkStartedAt := time.Now()
	newVersion, err = p.models.Sources.UpdateStatus(source.ID, "ingesting", "chunk", source.Version)

	if err != nil {
		if errors.Is(err, data.ErrEditConflict) {
			return errors.Join(fmt.Errorf("edit conflict when updating source status to chunk for source %d", source.ID), data.ErrEditConflict)
		}
		err := fmt.Errorf("update step to chunk for source %d: %w", source.ID, err)
		p.failSource(source, "chunk", err)
		return err
	}
	source.Version = newVersion

	chunks, err := p.chunker.ChunkContent(blocks, rawContent.Title, source.URL)

	if err != nil {
		p.failSource(source, "chunk", err)
		return fmt.Errorf("chunking failed: %w", err)
	}

	if len(chunks) == 0 {
		p.failSource(source, "chunk", ErrNoChunksGenerated)
		return fmt.Errorf("chunking failed: no chunks generated")
	}

	log.Info("pipeline: chunked",
		"chunks", len(chunks),
		"duration_ms", time.Since(chunkStartedAt).Milliseconds(),
	)

	// --- Step 4: STORE ---
	log.Info("pipeline: storing chunks")

	storeStartedAt := time.Now()
	newVersion, err = p.models.Sources.UpdateStatus(source.ID, "ingesting", "store", source.Version)

	if err != nil {
		if errors.Is(err, data.ErrEditConflict) {
			return errors.Join(fmt.Errorf("edit conflict when updating source status to store for source %d", source.ID), data.ErrEditConflict)
		}
		p.failSource(source, "store", err)
		return fmt.Errorf("update step to store for source %d: %w", source.ID, err)
	}
	source.Version = newVersion

	dataChunks := make([]*data.Chunk, len(chunks))

	for i, chunk := range chunks {
		metadata, err := json.Marshal(chunk.Metadata)

		if err != nil {
			metadata = []byte("{}")
		}

		dataChunks[i] = &data.Chunk{
			SourceID:   source.ID,
			ChunkIndex: chunk.Index,
			Content:    chunk.Content,
			TokenCount: chunk.TokenCount,
			Metadata:   metadata,
		}
	}

	err = p.models.Chunks.DeleteBySourceID(source.ID)

	if err != nil {
		p.failSource(source, "store", err)
		return fmt.Errorf("deleting old chunks: %w", err)
	}

	err = p.models.Chunks.BulkInsert(dataChunks)

	if err != nil {
		p.failSource(source, "store", err)
		return fmt.Errorf("bulk inserting chunks: %w", err)
	}

	log.Info("pipeline: stored chunks successfully",
		"chunks_stored", len(chunks),
		"duration_ms", time.Since(storeStartedAt).Milliseconds(),
	)

	// Step 5: ENQUEUE EMBEDDING JOB
	enqueueStartedAt := time.Now()

	newVersion, err = p.models.Sources.UpdateStatus(source.ID, "ingesting", "embed", source.Version)
	if err != nil {
		if errors.Is(err, data.ErrEditConflict) {
			return errors.Join(fmt.Errorf("edit conflict when updating source status to embed for source %d", source.ID), data.ErrEditConflict)
		}
		p.failSource(source, "embed", err)
		return fmt.Errorf("update step to embed for source %d: %w", source.ID, err)
	}

	source.Version = newVersion

	job := &data.EmbeddingJob{
		SourceID:      source.ID,
		SourceVersion: int(source.Version),
		Status:        data.EmbeddingJobStatusPending,
	}

	err = p.models.EmbeddingJobs.Insert(job)
	if err != nil {
		if errors.Is(err, data.ErrDuplicateRecord) {
			log.Warn("pipeline: embedding job already queued for this source version",
				"source_id", source.ID,
				"source_version", source.Version,
			)
			return nil
		}

		p.failSource(source, "embed", err)
		return fmt.Errorf("enqueue embedding job for source %d: %w", source.ID, err)
	}

	log.Info("pipeline: enqueued embedding job",
		"source_id", source.ID,
		"job_id", job.ID,
		"source_version", source.Version,
		"chunks_stored", len(dataChunks),
		"enqueue_duration_ms", time.Since(enqueueStartedAt).Milliseconds(),
		"pipeline_duration_ms", time.Since(pipelineStartedAt).Milliseconds(),
	)

	return nil
}

func (p *Pipeline) failSource(source *data.Source, step string, err error) {

	decision := classifyFailure(err)

	var updateErr error

	if decision.NonRetryable {
		updateErr = p.models.Sources.MarkAsStale(source.ID, step, err.Error(), source.Version)
	} else {
		updateErr = p.models.Sources.MarkAsFailed(source.ID, step, err.Error(), source.Version)
	}

	if updateErr != nil {
		if errors.Is(updateErr, data.ErrEditConflict) {
			p.logger.Warn(
				"edit conflict when marking source as failed/stale, likely due to concurrent modification",
				"source_id", source.ID,
				"step", step,
				"error", updateErr,
			)
			return
		}
		p.logger.Error("failed to update source status",
			"source_id", source.ID,
			"step", step,
			"error", updateErr,
			"original_error", err,
			"non_retryable", decision.NonRetryable,
			"reason", decision.Reason,
		)
	}

}

func classifyFailure(err error) FailureDecision {
	if err == nil {
		return FailureDecision{}
	}

	if errors.Is(err, fetcher.ErrInvalidURL) ||
		errors.Is(err, fetcher.ErrEmptyContent) ||
		errors.Is(err, fetcher.ErrUnexpectedContentType) ||
		errors.Is(err, ErrNoContentToChunk) ||
		errors.Is(err, ErrNoChunksGenerated) {
		return FailureDecision{
			NonRetryable: true,
			Reason:       "invalid or unprocessable content",
		}
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return FailureDecision{
			NonRetryable: false,
			Reason:       "context cancellation or timeout",
		}
	}

	var (
		networkErr net.Error
		fetcherErr *fetcher.FetcherErrors
	)

	switch {
	case errors.As(err, &fetcherErr):
		if isPermanentHTTPStatus(fetcherErr.StatusCode) {
			return FailureDecision{
				NonRetryable: true,
				Reason:       "invalid or unprocessable content",
			}
		} else {
			return FailureDecision{
				NonRetryable: false,
				Reason:       fmt.Sprintf("temporary error with status code %d", fetcherErr.StatusCode),
			}
		}
	case errors.As(err, &networkErr):
		if networkErr.Timeout() {
			return FailureDecision{
				NonRetryable: false,
				Reason:       "network timeout",
			}
		} else {
			return FailureDecision{
				NonRetryable: false,
				Reason:       "network error",
			}
		}
	default:
		return FailureDecision{
			NonRetryable: false,
			Reason:       "unknown error",
		}
	}

}

func isPermanentHTTPStatus(code int) bool {
	switch code {
	case 400, 401, 403, 404, 405, 410, 422, 451:
		return true
	case 408, 409, 425, 429:
		return false
	default:
		if code >= 500 {
			return false
		}
		return code >= 400 && code < 500
	}
}
