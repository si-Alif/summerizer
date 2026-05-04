package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/ingestion/embedder"
)

// embeddingPoolConfig holds all configurable parameters for the embedding pool
type embeddingPoolConfig struct {
	workerCount        int
	pollInterval       time.Duration
	jobTimeout         time.Duration
	reclaimInterval    time.Duration
	stuckJobThreshold  time.Duration
	batchSize          int
	claimBatchSize     int
	maxBackoffInterval time.Duration
}

// EmbeddingPoolOption is a function that configures an embedding pool setting
type EmbeddingPoolOption func(*embeddingPoolConfig)

// WithWorkerCount sets the number of embedding worker goroutines
func WithWorkerCount(n int) EmbeddingPoolOption {
	return func(c *embeddingPoolConfig) {
		if n > 0 {
			c.workerCount = n
		}
	}
}

// WithPollInterval sets the base interval between fallback polls
func WithPollInterval(d time.Duration) EmbeddingPoolOption {
	return func(c *embeddingPoolConfig) {
		if d > 0 {
			c.pollInterval = d
		}
	}
}

// WithJobTimeout sets the timeout for processing a single embedding job
func WithJobTimeout(d time.Duration) EmbeddingPoolOption {
	return func(c *embeddingPoolConfig) {
		if d > 0 {
			c.jobTimeout = d
		}
	}
}

// WithReclaimInterval sets the interval for reclaiming stuck embedding jobs
func WithReclaimInterval(d time.Duration) EmbeddingPoolOption {
	return func(c *embeddingPoolConfig) {
		if d > 0 {
			c.reclaimInterval = d
		}
	}
}

// WithStuckJobThreshold sets the threshold for considering an embedding job as stuck
func WithStuckJobThreshold(d time.Duration) EmbeddingPoolOption {
	return func(c *embeddingPoolConfig) {
		if d > 0 {
			c.stuckJobThreshold = d
		}
	}
}

// WithBatchSize sets the target embedding request batch size (for Nomic API calls)
func WithBatchSize(n int) EmbeddingPoolOption {
	return func(c *embeddingPoolConfig) {
		if n > 0 {
			c.batchSize = n
		}
	}
}

// WithClaimBatchSize sets the number of embedding jobs to claim per poll
func WithClaimBatchSize(n int) EmbeddingPoolOption {
	return func(c *embeddingPoolConfig) {
		if n > 0 {
			c.claimBatchSize = n
		}
	}
}

// WithMaxBackoffInterval sets the maximum backoff interval when the embedding queue is empty
func WithMaxBackoffInterval(d time.Duration) EmbeddingPoolOption {
	return func(c *embeddingPoolConfig) {
		if d > 0 {
			c.maxBackoffInterval = d
		}
	}
}

type EmbeddingPool struct {
	workerCount        int
	pollInterval       time.Duration
	jobTimeout         time.Duration
	reclaimInterval    time.Duration
	stuckJobThreshold  time.Duration
	batchSize          int
	claimBatchSize     int
	maxBackoffInterval time.Duration
	models             data.Models
	logger             *slog.Logger
	embedder           *embedder.Embedder
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	startedAt          time.Time
	firstPollOnce      sync.Once
	firstClaimOnce     sync.Once
	wakeCH             chan struct{}
	dbDSN              string
}

// NewEmbeddingPool creates a new embedding worker pool with sensible defaults.
// Required parameters: models, logger, embedderClient, and dbDSN.
// All other parameters can be customized via options.
//
// Example:
//
//	pool := worker.NewEmbeddingPool(
//	    models,
//	    logger,
//	    embedder,
//	    dsn,
//	    worker.WithWorkerCount(8),
//	    worker.WithClaimBatchSize(10),
//	    worker.WithMaxBackoffInterval(5*time.Minute),
//	)
func NewEmbeddingPool(
	models data.Models,
	logger *slog.Logger,
	embedderClient *embedder.Embedder,
	dbDSN string,
	opts ...EmbeddingPoolOption,
) *EmbeddingPool {
	cfg := &embeddingPoolConfig{
		workerCount:        4,
		pollInterval:       2 * time.Second,
		jobTimeout:         5 * time.Minute,
		reclaimInterval:    time.Minute,
		stuckJobThreshold:  10 * time.Minute,
		batchSize:          32,
		claimBatchSize:     5,
		maxBackoffInterval: 2 * time.Minute,
	}

	// Apply all provided options
	for _, opt := range opts {
		opt(cfg)
	}

	// Validate and adjust config if needed
	if cfg.maxBackoffInterval < cfg.pollInterval {
		cfg.maxBackoffInterval = cfg.pollInterval
	}

	return &EmbeddingPool{
		workerCount:        cfg.workerCount,
		pollInterval:       cfg.pollInterval,
		jobTimeout:         cfg.jobTimeout,
		reclaimInterval:    cfg.reclaimInterval,
		stuckJobThreshold:  cfg.stuckJobThreshold,
		batchSize:          cfg.batchSize,
		claimBatchSize:     cfg.claimBatchSize,
		maxBackoffInterval: cfg.maxBackoffInterval,
		models:             models,
		logger:             logger,
		embedder:           embedderClient,
		wakeCH:             make(chan struct{}, 1),
		dbDSN:              dbDSN,
	}
}

func (p *EmbeddingPool) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)
	p.startedAt = time.Now()

	go p.reclaimStuckEmbeddingJobs(ctx)

	p.wg.Add(1)

	go func() {
		defer p.wg.Done()
		p.startListener(ctx)
	}()

	for i := 0; i < p.workerCount; i++ {
		p.wg.Add(1)
		go p.run(ctx, i)
	}
}

func (p *EmbeddingPool) startListener(ctx context.Context) {
	retryDelay := 5 * time.Second

	// OUTER LOOP: Handle connection establishment & retry
	for {
		// check if context is done before attempting to connect
		if ctx.Err() != nil {
			p.logger.Info("embedding job listener stopping")
			return
		}

		// try to establish a connection to listen for notifications
		conn, err := pgx.Connect(ctx, p.dbDSN)
		if err != nil {
			// connaction failed , check if it's for context cancellation or an actual error
			if ctx.Err() != nil {
				return
			}
			// if it's an actual error, log it and retry after a delay
			p.logger.Error("failed to connect to database for embedding job listener", "error", err)
			if !sleepWithContext(ctx, retryDelay) {
				return
			}
			continue // try connecting again
		}

		// Tell DB : "I want to listen for notifications on the embedding_jobs channel"
		_, err = conn.Exec(ctx, "LISTEN embedding_jobs")
		if err != nil {
			// listen failed , check if it's for context cancellation or an actual error
			if ctx.Err() != nil {
				_ = conn.Close(context.Background())
				return // if context is done, close the connection and exit
			}
			// if it's an actual error, log it, close the connection, and retry after a delay
			p.logger.Error("failed to listen for embedding job notifications", "error", err)
			_ = conn.Close(context.Background())
			if !sleepWithContext(ctx, retryDelay) {
				return
			}
			continue // try establishing the connection and listening again
		}

		p.logger.Info("started listening for embedding job notifications")

		// INNER LOOP : Listen for notifications on this connection
		for {
			// wait for a notification . This is the actual breaking point which block the goroutine until either a notification is received or an error occurs (like connection loss or context cancellation)
			_, err := conn.WaitForNotification(ctx)
			if err != nil {
				// if WaitForNotification returns an error, it could be due to context cancellation or a connection issue. We need to check which one it is.
				if ctx.Err() != nil {
					_ = conn.Close(context.Background()) // if context is done, close the connection and exit
					p.logger.Info("embedding job listener stopping")
					return
				}
				// if it's an actual error, log it, close the connection, and break the inner loop to retry establishing a new connection
				p.logger.Error("failed to wait for embedding job notification", "error", err)
				_ = conn.Close(context.Background())
				break // exit the loop
			}

			// if we got to this point , means WaitForNotification() was unblocked which means a notification was received
			select {
			case p.wakeCH <- struct{}{}: // try to send a wake signal to the workers to prompt them to poll for new jobs immediately
			default:
			}
		}

	}
}

func (p *EmbeddingPool) reclaimStuckEmbeddingJobs(ctx context.Context) {
	ticker := time.NewTicker(p.reclaimInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reclaimed, err := p.models.EmbeddingJobs.ReclaimStuckAtProcessing(p.stuckJobThreshold)
			if err != nil {
				p.logger.Error("failed to reclaim stuck embedding jobs", "error", err)
				continue
			}
			if reclaimed > 0 {
				p.logger.Info("reclaimed stuck embedding jobs", "count", reclaimed)
			}
		}
	}
}

func (p *EmbeddingPool) run(ctx context.Context, workerID int) {
	backoffInterval := p.pollInterval
	timer := time.NewTimer(backoffInterval)
	defer timer.Stop()

	defer p.wg.Done()

	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("embedding worker panicked", "worker_id", workerID, "panic", r)
		}
	}()

	p.logger.Info("embedding worker started", "worker_id", workerID)

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("embedding worker stopping", "worker_id", workerID)
			return
		case <-timer.C:
			p.firstPollOnce.Do(func() {
				elapsedMs := int64(0)
				if !p.startedAt.IsZero() {
					elapsedMs = time.Since(p.startedAt).Milliseconds()
				}

				p.logger.Info("embedding worker first fallback poll attempt",
					"worker_id", workerID,
					"elapsed_ms", elapsedMs,
					"poll_interval", p.pollInterval.String(),
				)
			})
			p.logger.Info("embedding worker fallback polling for jobs", "worker_id", workerID)

			found, err := p.poll(ctx, workerID)
			backoffInterval = adjustBackoff(backoffInterval, p.pollInterval, p.maxBackoffInterval, found, err)
			resetTimer(timer, backoffInterval)
		case <-p.wakeCH:
			p.logger.Info("embedding worker received wake signal", "worker_id", workerID)
			found, err := p.poll(ctx, workerID)
			backoffInterval = adjustBackoff(backoffInterval, p.pollInterval, p.maxBackoffInterval, found, err)
			resetTimer(timer, backoffInterval)
		}

	}
}

func (p *EmbeddingPool) poll(ctx context.Context, workerID int) (bool, error) {
	lockedBy := p.lockedBy(workerID)
	jobs, err := p.models.EmbeddingJobs.ClaimPending(p.claimBatchSize, lockedBy)
	if err != nil {
		p.logger.Error("failed to claim pending embedding jobs", "worker_id", workerID, "error", err)
		return false, err
	}

	if len(jobs) == 0 {
		return false, nil
	}

	p.firstClaimOnce.Do(func() {
		elapsedMs := int64(0)
		if !p.startedAt.IsZero() {
			elapsedMs = time.Since(p.startedAt).Milliseconds()
		}

		p.logger.Info("embedding worker first successful claim",
			"worker_id", workerID,
			"claimed_jobs", len(jobs),
			"elapsed_ms", elapsedMs,
		)
	})

	for _, job := range jobs {
		p.process(ctx, workerID, lockedBy, job)
	}

	return true, nil
}

func (p *EmbeddingPool) process(ctx context.Context, workerID int, lockedBy string, job *data.EmbeddingJob) {
	if p.embedder == nil {
		p.logger.Error("embedder client is nil, cannot process embedding job",
			"worker_id", workerID,
			"job_id", job.ID,
		)
		_ = p.models.EmbeddingJobs.MarkAsFailed(job.ID, "embedder client is nil", job.Version, lockedBy)
		return
	}

	jobCtx, cancel := context.WithTimeout(ctx, p.jobTimeout)
	defer cancel()

	startedAt := time.Now()
	p.logger.Info("processing embedding job",
		"worker_id", workerID,
		"job_id", job.ID,
		"source_id", job.SourceID,
		"attempts", job.Attempts,
		"max_attempts", job.MaxAttempts,
		"batch_size", p.batchSize,
	)

	chunks, err := p.models.Chunks.GetBySourceID(job.SourceID)
	if err != nil {
		p.markJobFailed(workerID, lockedBy, job, fmt.Errorf("loading chunks for source %d: %w", job.SourceID, err))
		return
	}

	if len(chunks) == 0 {
		p.markJobFailed(workerID, lockedBy, job, fmt.Errorf("no chunks found for source %d", job.SourceID))
		return
	}

	texts := make([]string, len(chunks))
	chunkIDs := make([]int64, len(chunks))
	for i, chunk := range chunks {
		texts[i] = chunk.Content
		chunkIDs[i] = chunk.ID
	}

	embeddings, stats, err := p.embedder.GetEmbeddingsBatchedWithStats(jobCtx, texts, p.batchSize)
	if err != nil {
		p.markJobFailed(workerID, lockedBy, job, fmt.Errorf("embedding chunks for source %d: %w", job.SourceID, err))
		return
	}

	err = p.models.Chunks.BulkUpdateEmbedding(chunkIDs, embeddings)
	if err != nil {
		p.markJobFailed(workerID, lockedBy, job, fmt.Errorf("updating embeddings for source %d: %w", job.SourceID, err))
		return
	}

	_, sourceUpdateErr := p.models.Sources.UpdateStatus(job.SourceID, "completed", "embed", int32(job.SourceVersion))
	if sourceUpdateErr != nil {
		if errors.Is(sourceUpdateErr, data.ErrEditConflict) {
			p.logger.Warn("source status update conflict after embedding; continuing to finalize job",
				"worker_id", workerID,
				"job_id", job.ID,
				"source_id", job.SourceID,
				"source_version", job.SourceVersion,
			)
		} else {
			p.markJobFailed(workerID, lockedBy, job, fmt.Errorf("updating source %d to completed: %w", job.SourceID, sourceUpdateErr))
			return
		}
	}

	newVersion, err := p.models.EmbeddingJobs.MarkAsCompleted(job.ID, job.Version, lockedBy)
	if err != nil {
		if errors.Is(err, data.ErrEditConflict) {
			p.logger.Warn("embedding job completion conflict",
				"worker_id", workerID,
				"job_id", job.ID,
				"source_id", job.SourceID,
				"error", err,
			)
			return
		}

		p.logger.Error("failed to mark embedding job completed",
			"worker_id", workerID,
			"job_id", job.ID,
			"source_id", job.SourceID,
			"error", err,
		)
		return
	}

	if stats.ColdStartBatches > 0 {
		p.logger.Warn("embedding model had cold-start batches",
			"worker_id", workerID,
			"job_id", job.ID,
			"source_id", job.SourceID,
			"cold_start_batches", stats.ColdStartBatches,
			"load_duration_ms", stats.LoadDuration.Milliseconds(),
		)
	}

	p.logger.Info("embedding job completed",
		"worker_id", workerID,
		"job_id", job.ID,
		"source_id", job.SourceID,
		"new_job_version", newVersion,
		"chunks_embedded", len(chunks),
		"batches", stats.Batches,
		"retries", stats.Retries,
		"final_batch_size", stats.FinalBatchSize,
		"embed_total_duration_ms", stats.TotalDuration.Milliseconds(),
		"job_duration_ms", time.Since(startedAt).Milliseconds(),
	)
}

func (p *EmbeddingPool) markJobFailed(workerID int, lockedBy string, job *data.EmbeddingJob, cause error) {
	if cause == nil {
		return
	}

	err := p.models.EmbeddingJobs.MarkAsFailed(job.ID, cause.Error(), job.Version, lockedBy)
	if err != nil {
		if errors.Is(err, data.ErrEditConflict) {
			p.logger.Warn("embedding job failure update conflict",
				"worker_id", workerID,
				"job_id", job.ID,
				"source_id", job.SourceID,
				"error", err,
			)
			return
		}

		p.logger.Error("failed to mark embedding job as failed",
			"worker_id", workerID,
			"job_id", job.ID,
			"source_id", job.SourceID,
			"error", err,
			"cause", cause,
		)
		return
	}

	p.logger.Warn("embedding job marked failed",
		"worker_id", workerID,
		"job_id", job.ID,
		"source_id", job.SourceID,
		"attempts", job.Attempts+1,
		"cause", cause,
	)
}

func adjustBackoff(current, base, max time.Duration, found bool, err error) time.Duration {
	if err != nil {
		return nextBackoff(current, max)
	}
	if found {
		return base
	}
	return nextBackoff(current, max)
}

func nextBackoff(current, max time.Duration) time.Duration {
	if current <= 0 {
		return max
	}
	if current >= max {
		return max
	}
	next := current * 2
	if next > max {
		return max
	}
	return next
}

func resetTimer(timer *time.Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (p *EmbeddingPool) lockedBy(workerID int) string {
	return fmt.Sprintf("embedding-worker-%d", workerID)
}

func (p *EmbeddingPool) Shutdown() {
	p.logger.Info("shutting down embedding worker pool ....")
	if p.cancel != nil {
		p.cancel()
	}

	p.wg.Wait()
	p.logger.Info("embedding worker pool stopped")
}
