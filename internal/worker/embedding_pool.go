package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/ingestion/embedder"
)

type EmbeddingPool struct {
	workerCount       int
	pollInterval      time.Duration
	jobTimeout        time.Duration
	reclaimInterval   time.Duration
	stuckJobThreshold time.Duration
	batchSize         int
	models            data.Models
	logger            *slog.Logger
	embedder          *embedder.Embedder
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	startedAt         time.Time
	firstPollOnce     sync.Once
	firstClaimOnce    sync.Once
}

func NewEmbeddingPool(
	data data.Models,
	workerCount int,
	pollInterval time.Duration,
	logger *slog.Logger,
	embedderClient *embedder.Embedder,
	jobTimeout time.Duration,
	reclaimInterval time.Duration,
	stuckJobThreshold time.Duration,
	batchSize int,
) *EmbeddingPool {
	if batchSize <= 0 {
		batchSize = 32
	}

	return &EmbeddingPool{
		workerCount:       workerCount,
		pollInterval:      pollInterval,
		jobTimeout:        jobTimeout,
		reclaimInterval:   reclaimInterval,
		stuckJobThreshold: stuckJobThreshold,
		batchSize:         batchSize,
		models:            data,
		logger:            logger,
		embedder:          embedderClient,
	}
}

func (p *EmbeddingPool) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)
	p.startedAt = time.Now()

	go p.reclaimStuckEmbeddingJobs(ctx)

	for i := 0; i < p.workerCount; i++ {
		p.wg.Add(1)
		go p.run(ctx, i)
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
		case <-time.After(p.pollInterval):
			p.firstPollOnce.Do(func() {
				elapsedMs := int64(0)
				if !p.startedAt.IsZero() {
					elapsedMs = time.Since(p.startedAt).Milliseconds()
				}

				p.logger.Info("embedding worker first poll attempt",
					"worker_id", workerID,
					"elapsed_ms", elapsedMs,
					"poll_interval", p.pollInterval.String(),
				)
			})

			p.poll(ctx, workerID)
		}
	}
}

func (p *EmbeddingPool) poll(ctx context.Context, workerID int) {
	lockedBy := p.lockedBy(workerID)
	jobs, err := p.models.EmbeddingJobs.ClaimPending(1, lockedBy)
	if err != nil {
		p.logger.Error("failed to claim pending embedding jobs", "worker_id", workerID, "error", err)
		return
	}

	if len(jobs) == 0 {
		return
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
