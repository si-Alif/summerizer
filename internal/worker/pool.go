package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/ingestion"
)

// ingestionPoolConfig holds all configurable parameters for the ingestion pool
type ingestionPoolConfig struct {
	workerCount          int
	pollInterval         time.Duration
	sourceTimeout        time.Duration
	reclaimInterval      time.Duration
	stuckSourceThreshold time.Duration
	claimBatchSize       int
	maxBackoffInterval   time.Duration
}

// IngestionPoolOption is a function that configures an ingestion pool setting
type IngestionPoolOption func(*ingestionPoolConfig)

// WithIngestionWorkerCount sets the number of ingestion worker goroutines
func WithIngestionWorkerCount(n int) IngestionPoolOption {
	return func(c *ingestionPoolConfig) {
		if n > 0 {
			c.workerCount = n
		}
	}
}

// WithIngestionPollInterval sets the base interval between fallback polls
func WithIngestionPollInterval(d time.Duration) IngestionPoolOption {
	return func(c *ingestionPoolConfig) {
		if d > 0 {
			c.pollInterval = d
		}
	}
}

// WithIngestionSourceTimeout sets the timeout for processing a single source
func WithIngestionSourceTimeout(d time.Duration) IngestionPoolOption {
	return func(c *ingestionPoolConfig) {
		if d > 0 {
			c.sourceTimeout = d
		}
	}
}

// WithIngestionReclaimInterval sets the interval for reclaiming stuck sources
func WithIngestionReclaimInterval(d time.Duration) IngestionPoolOption {
	return func(c *ingestionPoolConfig) {
		if d > 0 {
			c.reclaimInterval = d
		}
	}
}

// WithIngestionStuckSourceThreshold sets the threshold for considering a source as stuck
func WithIngestionStuckSourceThreshold(d time.Duration) IngestionPoolOption {
	return func(c *ingestionPoolConfig) {
		if d > 0 {
			c.stuckSourceThreshold = d
		}
	}
}

// WithIngestionClaimBatchSize sets the number of sources to claim per poll
func WithIngestionClaimBatchSize(n int) IngestionPoolOption {
	return func(c *ingestionPoolConfig) {
		if n > 0 {
			c.claimBatchSize = n
		}
	}
}

// WithIngestionMaxBackoffInterval sets the maximum backoff interval when the queue is empty
func WithIngestionMaxBackoffInterval(d time.Duration) IngestionPoolOption {
	return func(c *ingestionPoolConfig) {
		if d > 0 {
			c.maxBackoffInterval = d
		}
	}
}

type Pool struct {
	workerCount          int
	pollInterval         time.Duration
	models               data.Models
	logger               *slog.Logger
	cancel               context.CancelFunc
	wg                   sync.WaitGroup
	pipeline             *ingestion.Pipeline
	sourceTimeout        time.Duration
	reclaimInterval      time.Duration
	stuckSourceThreshold time.Duration
	claimBatchSize       int
	maxBackoffInterval   time.Duration
	wakeCH               chan struct{}
	dbDSN                string
	startedAt            time.Time
	firstPollOnce        sync.Once
	firstClaimOnce       sync.Once
}

func NewPool(
	models data.Models,
	logger *slog.Logger,
	pipeline *ingestion.Pipeline,
	dbDSN string,
	opts ...IngestionPoolOption,
) *Pool {
	cfg := &ingestionPoolConfig{
		workerCount:          10,
		pollInterval:         5 * time.Second,
		sourceTimeout:        90 * time.Second,
		reclaimInterval:      time.Minute,
		stuckSourceThreshold: 10 * time.Minute,
		claimBatchSize:       1,
		maxBackoffInterval:   2 * time.Minute,
	}

	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.maxBackoffInterval < cfg.pollInterval {
		cfg.maxBackoffInterval = cfg.pollInterval
	}

	return &Pool{
		workerCount:          cfg.workerCount,
		pollInterval:         cfg.pollInterval,
		models:               models,
		logger:               logger,
		pipeline:             pipeline,
		sourceTimeout:        cfg.sourceTimeout,
		reclaimInterval:      cfg.reclaimInterval,
		stuckSourceThreshold: cfg.stuckSourceThreshold,
		claimBatchSize:       cfg.claimBatchSize,
		maxBackoffInterval:   cfg.maxBackoffInterval,
		wakeCH:               make(chan struct{}, 1),
		dbDSN:                dbDSN,
	}
}

func (p *Pool) Start(ctx context.Context) {

	ctx, p.cancel = context.WithCancel(ctx)
	p.startedAt = time.Now()

	go p.reclaimStuckSources(ctx)

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

func (p *Pool) startListener(ctx context.Context) {
	retryDelay := 5 * time.Second

	for {
		if ctx.Err() != nil {
			p.logger.Info("source listener stopping")
			return
		}

		conn, err := pgx.Connect(ctx, p.dbDSN)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			p.logger.Error("failed to connect to database for source listener", "error", err)
			if !sleepWithContext(ctx, retryDelay) {
				return
			}
			continue
		}

		_, err = conn.Exec(ctx, "LISTEN sources")
		if err != nil {
			if ctx.Err() != nil {
				_ = conn.Close(context.Background())
				return
			}
			p.logger.Error("failed to listen for source notifications", "error", err)
			_ = conn.Close(context.Background())
			if !sleepWithContext(ctx, retryDelay) {
				return
			}
			continue
		}

		p.logger.Info("started listening for source notifications")

		for {
			_, err := conn.WaitForNotification(ctx)
			if err != nil {
				if ctx.Err() != nil {
					_ = conn.Close(context.Background())
					p.logger.Info("source listener stopping")
					return
				}
				p.logger.Error("failed to wait for source notification", "error", err)
				_ = conn.Close(context.Background())
				break
			}

			select {
			case p.wakeCH <- struct{}{}:
			default:
			}
		}
	}
}

func (p *Pool) reclaimStuckSources(ctx context.Context) {
	// ticker.NewTicker(t) ticks after a certain amount of time(t) continuously
	// it returns a channel that can be used to receive time at each tick .
	// we can use that channel to trigger the reclaiming of stuck sources at regular intervals defined by p.reclaimInterval.
	ticker := time.NewTicker(p.reclaimInterval)

	// to stop the ticker use ticker.Stop()
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			recoveryCount, err := p.models.Sources.ReclaimStuckAtIngesting(p.stuckSourceThreshold)
			if err != nil {
				p.logger.Error("failed to reclaim stuck sources", "error", err)
				continue
			}
			if recoveryCount > 0 {
				p.logger.Info("reclaimed stuck sources", "count", recoveryCount)
			}
		}
	}

}

func (p *Pool) run(ctx context.Context, worker_id int) {
	backoffInterval := p.pollInterval
	timer := time.NewTimer(backoffInterval)
	defer timer.Stop()

	defer p.wg.Done()

	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("worker panicked", "worker_id", worker_id, "panic", r)
		}
	}()

	p.logger.Info("worker started", "worker_id", worker_id)

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("worker stopping", "worker_id", worker_id)
			return
		case <-timer.C:
			p.firstPollOnce.Do(func() {
				elapsedMs := int64(0)
				if !p.startedAt.IsZero() {
					elapsedMs = time.Since(p.startedAt).Milliseconds()
				}

				p.logger.Info("worker first fallback poll attempt",
					"worker_id", worker_id,
					"elapsed_ms", elapsedMs,
					"poll_interval", p.pollInterval.String(),
				)
			})
			p.logger.Info("worker fallback polling for sources", "worker_id", worker_id)

			found, err := p.poll(ctx, worker_id)
			backoffInterval = adjustBackoff(backoffInterval, p.pollInterval, p.maxBackoffInterval, found, err)
			resetTimer(timer, backoffInterval)
		case <-p.wakeCH:
			p.logger.Info("worker received wake signal", "worker_id", worker_id)
			found, err := p.poll(ctx, worker_id)
			backoffInterval = adjustBackoff(backoffInterval, p.pollInterval, p.maxBackoffInterval, found, err)
			resetTimer(timer, backoffInterval)
		}
	}
}

func (p *Pool) poll(ctx context.Context, worker_id int) (bool, error) {
	sources, err := p.models.Sources.ClaimPending(p.claimBatchSize)
	if err != nil {
		p.logger.Error("failed to claim pending sources", "worker_id", worker_id, "error", err)
		return false, err
	}

	if len(sources) == 0 {
		return false, nil
	}

	p.firstClaimOnce.Do(func() {
		elapsedMs := int64(0)
		if !p.startedAt.IsZero() {
			elapsedMs = time.Since(p.startedAt).Milliseconds()
		}

		p.logger.Info("worker first successful claim",
			"worker_id", worker_id,
			"claimed_sources", len(sources),
			"elapsed_ms", elapsedMs,
		)
	})

	for _, source := range sources {
		p.process(ctx, worker_id, source)
	}

	return true, nil

}

func (p *Pool) process(ctx context.Context, worker_id int, source *data.Source) {

	// 90s
	sourceIngestionCtx, cancel := context.WithTimeout(ctx, p.sourceTimeout)
	defer cancel()

	p.logger.Info("processing source",
		"worker_id", worker_id,
		"source_id", source.ID,
		"url", source.URL,
		"type", source.SourceType,
	)

	err := p.pipeline.ProcessSource(sourceIngestionCtx, source)

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			p.logger.Warn("source processing timed out",
				"worker_id", worker_id,
				"source_id", source.ID,
				"timeout", p.sourceTimeout.String(),
			)
			return
		}
		if errors.Is(err, data.ErrEditConflict) {
			p.logger.Warn(
				"edit conflict when processing source, likely due to concurrent modification",
				"worker_id", worker_id,
				"source_id", source.ID,
				"error", err,
			)
			return
		}
		p.logger.Error("failed to update source status",
			"worker_id", worker_id,
			"source_id", source.ID,
			"error", err,
		)
		return
	}

	p.logger.Info("source ingestion staged for embedding", "worker_id", worker_id, "source_id", source.ID)

}

func (p *Pool) Shutdown() {
	p.logger.Info("shutting down worker pool ....")
	if p.cancel != nil {
		p.cancel()
	}

	p.wg.Wait()

	p.logger.Info("worker pool stopped")
}
