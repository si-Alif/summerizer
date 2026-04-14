package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/ingestion"
)

type Pool struct {
	workerCount    int
	pollInterval   time.Duration
	models         data.Models
	logger         *slog.Logger
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	pipeline       *ingestion.Pipeline
	sourceTimeout  time.Duration
	reclaimInterval time.Duration
	stuckSourceThreshold time.Duration
	startedAt      time.Time
	firstPollOnce  sync.Once
	firstClaimOnce sync.Once
}

func NewPool(
	data data.Models,
	workerCount int,
	pollInterval time.Duration,
	logger *slog.Logger,
	pipeline *ingestion.Pipeline,
	sourceTimeout time.Duration,
	reclaimInterval time.Duration,
	stuckSourceThreshold time.Duration,
) *Pool {
	return &Pool{
		workerCount:  workerCount,
		pollInterval: pollInterval,
		models:       data,
		logger:       logger,
		pipeline:     pipeline,
		sourceTimeout:  sourceTimeout,
		reclaimInterval: reclaimInterval,
		stuckSourceThreshold: stuckSourceThreshold,
	}
}

func (p *Pool) Start(ctx context.Context) {

	ctx, p.cancel = context.WithCancel(ctx)
	p.startedAt = time.Now()

	go p.reclaimStuckSources(ctx)

	for i := 0; i < p.workerCount; i++ {
		p.wg.Add(1)
		go p.run(ctx, i)
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
		case <- ticker.C :
			recoveryCount , err := p.models.Sources.ReclaimStuckAtIngesting(p.stuckSourceThreshold)
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
		case <-time.After(p.pollInterval):
			p.firstPollOnce.Do(func() {
				elapsedMs := int64(0)
				if !p.startedAt.IsZero() {
					elapsedMs = time.Since(p.startedAt).Milliseconds()
				}

				p.logger.Info("worker first poll attempt",
					"worker_id", worker_id,
					"elapsed_ms", elapsedMs,
					"poll_interval", p.pollInterval.String(),
				)
			})
			p.poll(ctx, worker_id)
		}
	}
}

func (p *Pool) poll(ctx context.Context, worker_id int) {
	sources, err := p.models.Sources.ClaimPending(1)
	if err != nil {
		p.logger.Error("failed to claim pending sources", "worker_id", worker_id, "error", err)
		return
	}

	if len(sources) == 0 {
		return
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

}

func (p *Pool) process(ctx context.Context, worker_id int, source *data.Source) {

	sourceIngestionCtx ,  cancel := context.WithTimeout(ctx , p.sourceTimeout)
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
		}
		p.logger.Error("failed to update source status",
			"worker_id", worker_id,
			"source_id", source.ID,
			"error", err,
		)
		return
	}

	p.logger.Info("source completed", "worker_id", worker_id, "source_id", source.ID)

}

func (p *Pool) Shutdown() {
	p.logger.Info("shutting down worker pool ....")
	if p.cancel != nil {
		p.cancel()
	}

	p.wg.Wait()

	p.logger.Info("worker pool stopped")
}
