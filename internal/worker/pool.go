package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/si-Alif/summerizer/internal/data"
)

type Pool struct {
	workerCount  int
	pollInterval time.Duration
	models       data.Models
	logger       *slog.Logger
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

func NewPool(
	data data.Models,
	workerCount int,
	pollInterval time.Duration,
	logger *slog.Logger,
) *Pool {
	return &Pool{
		workerCount:  workerCount,
		pollInterval: pollInterval,
		models:       data,
		logger:       logger,
	}
}

func (p *Pool) Start(ctx context.Context){

	ctx , p.cancel = context.WithCancel(ctx)

	for i := 0; i < p.workerCount; i++ {
		p.wg.Add(1)
		go p.run(ctx , i)
	}
}

func (p *Pool) run(ctx context.Context , worker_id int) {
	defer p.wg.Done()

	defer func(){
		if r:= recover(); r != nil {
			p.logger.Error("worker panicked" , "worker_id" , worker_id , "panic" , r)
		}
	}()

	p.logger.Info("worker started" , "worker_id" , worker_id)

	for {
		select{
			case <-ctx.Done() :
				p.logger.Info("worker stopping" , "worker_id" , worker_id)
				return
			case <-time.After(p.pollInterval) :
				p.poll(ctx,worker_id)
		}
	}
}

func (p *Pool) poll(ctx context.Context , worker_id int){
	sources , err := p.models.Sources.ClaimPending(1)
	if err != nil {
		p.logger.Error("failed to claim pending sources", "worker_id", worker_id, "error", err)
		return
	}

	if len(sources) == 0{
		return
	}

	for _, source := range sources{
		p.process(ctx , worker_id , source)
	}

}

func (p *Pool) process(ctx context.Context , worker_id int , source *data.Source){
	p.logger.Info("processing source",
		"worker_id", worker_id,
		"source_id", source.ID,
		"url", source.URL,
		"type", source.SourceType,
	)

	// TODO : build pipeline . For now , just simulate work via sleeping
	time.Sleep(3 * time.Second)

	err := p.models.Sources.UpdateStatus(
		source.ID ,
		"completed",
		nil,
		nil ,
		source.RetryCount,
		nil,
	)

	if err != nil {
		p.logger.Error("failed to update source status",
			"worker_id", worker_id,
			"source_id", source.ID,
			"error", err,
		)
		return
	}

	p.logger.Info("source completed", "worker_id", worker_id, "source_id", source.ID)

}

func (p *Pool) Shutdown(){
	p.logger.Info("shutting down worker pool ....")
	p.cancel()

	p.wg.Wait()

	p.logger.Info("worker pool stopped")
}