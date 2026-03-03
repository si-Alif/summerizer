package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/si-Alif/summerizer/internal/data"
)

type Pool struct {
	workerCount int
	pollInterval time.Duration
	models data.Models
	logger *slog.Logger
	cancel context.CancelFunc
	wg sync.WaitGroup
}

func NewPool(
	data data.Models,
	workerCount int,
	pollInterval time.Duration,
	logger *slog.Logger,
) *Pool {
	return &Pool{
		workerCount: workerCount,
		pollInterval: pollInterval,
		models: data,
		logger: logger,
	}
}

