package main

import (
	"context"
	"database/sql"
	"flag"
	"log/slog"
	"os"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/ingestion"
	"github.com/si-Alif/summerizer/internal/ingestion/chunker"
	"github.com/si-Alif/summerizer/internal/ingestion/embedder"
	"github.com/si-Alif/summerizer/internal/ingestion/fetcher"
	"github.com/si-Alif/summerizer/internal/llm/huggingface"
	"github.com/si-Alif/summerizer/internal/mailer"

	// "github.com/si-Alif/summerizer/internal/llm/ollama"
	"github.com/si-Alif/summerizer/internal/search"
	"github.com/si-Alif/summerizer/internal/worker"
)

var (
	version = "1.0.0"
)

type config struct {
	port int
	env  string
	db   struct {
		dsn          string
		maxOpenConns int
		maxIdleConns int
		maxIdleTime  time.Duration
	}
	worker_pool struct {
		worker_count  int
		poll_interval time.Duration
	}
	limiter struct {
		rps     float64
		burst   int
		enabled bool
	}
	smtp struct {
		host     string
		port     int
		username string
		password string
		sender   string
	}
	rollout struct {
		inline_embedding_enabled  bool
		async_embedding_enabled   bool
		dual_write_embedding_jobs bool
	}
}

type application struct {
	config  config
	logger  *slog.Logger
	models  data.Models
	workers *worker.Pool
	service *search.Service
	mailer  *mailer.Mailer
	wg      sync.WaitGroup
}

func main() {
	processStartedAt := time.Now()

	var cfg config

	flag.IntVar(&cfg.port, "port", 4000, "API server port")
	flag.StringVar(&cfg.env, "env", "development", "Environment (development | production | test)")

	// DB connection pool settings
	flag.StringVar(&cfg.db.dsn, "db-dsn", "", "PostgreSQL data source name")
	flag.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", 25, "PostgreSQL max open connections")
	flag.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", 25, "PostgreSQL max idle connections")
	flag.DurationVar(&cfg.db.maxIdleTime, "db-max-idle-time", 15*time.Minute, "PostgreSQL max idle time for a connection")

	// worker pool settings
	flag.IntVar(&cfg.worker_pool.worker_count, "worker-count", 10, "Number of worker goroutines")
	flag.DurationVar(&cfg.worker_pool.poll_interval, "poll-interval", 5*time.Second, "Interval between polls for pending sources")

	// rate limiter settings
	flag.Float64Var(&cfg.limiter.rps, "limiter-rps", 2, "Rate limiter maximum requests per second")
	flag.IntVar(&cfg.limiter.burst, "limiter-burst", 4, "Rate limiter burst size")
	flag.BoolVar(&cfg.limiter.enabled, "limiter-enabled", true, "Enable rate limiter")

	// SMTP settings
	flag.StringVar(&cfg.smtp.host, "smtp-host", "", "SMTP server host")
	flag.IntVar(&cfg.smtp.port, "smtp-port", 0, "SMTP server port")
	flag.StringVar(&cfg.smtp.username, "smtp-username", "", "SMTP server username")
	flag.StringVar(&cfg.smtp.password, "smtp-password", "", "SMTP server password")
	flag.StringVar(&cfg.smtp.sender, "smtp-sender", "", "Email address of the sender")
	flag.BoolVar(&cfg.rollout.inline_embedding_enabled, "inline-embedding-enabled", true, "Run embedding inline in ingestion pipeline")
	flag.BoolVar(&cfg.rollout.async_embedding_enabled, "async-embedding-enabled", false, "Enable async embedding workflow (requires queue worker)")
	flag.BoolVar(&cfg.rollout.dual_write_embedding_jobs, "dual-write-embedding-jobs", false, "Enqueue embedding jobs while still running inline embedding")

	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	if !cfg.rollout.inline_embedding_enabled {
		logger.Error("invalid rollout configuration",
			"reason", "inline embedding cannot be disabled before async embedding worker is implemented",
		)
		os.Exit(1)
	}

	if cfg.rollout.async_embedding_enabled || cfg.rollout.dual_write_embedding_jobs {
		logger.Warn("async embedding rollout flags enabled but queue path is not implemented yet; running inline embedding only",
			"async_embedding_enabled", cfg.rollout.async_embedding_enabled,
			"dual_write_embedding_jobs", cfg.rollout.dual_write_embedding_jobs,
		)
	}

	logStartupPhase := func(phase string, startedAt time.Time) {
		logger.Info("startup phase complete",
			"phase", phase,
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
	}

	openDBStartedAt := time.Now()
	db, err := openDB(cfg)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	defer db.Close()
	logStartupPhase("open_db", openDBStartedAt)

	logger.Info("database connection pool established")

	modelsStartedAt := time.Now()
	models := data.NewModels(db)
	logStartupPhase("init_models", modelsStartedAt)

	mailerStartedAt := time.Now()
	mailerSvc, err := mailer.NewMailer(cfg.smtp.host, cfg.smtp.port, cfg.smtp.username, cfg.smtp.password, cfg.smtp.sender)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	logStartupPhase("init_mailer", mailerStartedAt)

	fetcherStartedAt := time.Now()
	webFetcher := fetcher.NewFetcher()
	logStartupPhase("init_fetcher", fetcherStartedAt)

	chunkerStartedAt := time.Now()
	textChunker, err := chunker.New(400, 1)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	logStartupPhase("init_chunker", chunkerStartedAt)

	embedderStartedAt := time.Now()
	embedderClient := embedder.NewEmbedder("", "")
	logStartupPhase("init_embedder", embedderStartedAt)

	pipelineStartedAt := time.Now()
	pipeline := ingestion.NewPipeline(models, logger, webFetcher, textChunker, embedderClient)
	logStartupPhase("init_pipeline", pipelineStartedAt)

	workerPoolStartedAt := time.Now()
	workerPool := worker.NewPool(models, cfg.worker_pool.worker_count, cfg.worker_pool.poll_interval, logger, pipeline)
	logStartupPhase("init_worker_pool", workerPoolStartedAt)

	llmStartedAt := time.Now()
	llmClientHF, err := huggingface.NewHFModel("", "")
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	logStartupPhase("init_llm", llmStartedAt)

	searchStartedAt := time.Now()
	searchService := search.NewService(embedderClient, models, llmClientHF)
	logStartupPhase("init_search_service", searchStartedAt)

	appInitStartedAt := time.Now()
	app := application{
		config:  cfg,
		logger:  logger,
		models:  models,
		workers: workerPool,
		service: searchService,
		mailer:  mailerSvc,
	}
	logStartupPhase("init_application", appInitStartedAt)

	workerStartStartedAt := time.Now()
	app.workers.Start(context.Background())
	logStartupPhase("start_workers", workerStartStartedAt)

	logger.Info("startup complete",
		"duration_ms", time.Since(processStartedAt).Milliseconds(),
		"worker_count", cfg.worker_pool.worker_count,
		"poll_interval", cfg.worker_pool.poll_interval.String(),
		"inline_embedding_enabled", cfg.rollout.inline_embedding_enabled,
		"async_embedding_enabled", cfg.rollout.async_embedding_enabled,
		"dual_write_embedding_jobs", cfg.rollout.dual_write_embedding_jobs,
	)

	err = app.serve()

	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	app.workers.Shutdown()

}

func openDB(cfg config) (*sql.DB, error) {
	db, err := sql.Open("pgx", cfg.db.dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(cfg.db.maxOpenConns)
	db.SetMaxIdleConns(cfg.db.maxIdleConns)
	db.SetConnMaxIdleTime(cfg.db.maxIdleTime)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = db.PingContext(ctx)

	if err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}
