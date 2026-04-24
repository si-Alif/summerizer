package main

import (
	"context"
	"database/sql"
	"expvar"
	"flag"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/ingestion"
	"github.com/si-Alif/summerizer/internal/ingestion/chunker"
	"github.com/si-Alif/summerizer/internal/ingestion/embedder"
	"github.com/si-Alif/summerizer/internal/ingestion/fetcher"
	"github.com/si-Alif/summerizer/internal/llm/gemini"
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
		worker_count           int
		poll_interval          time.Duration
		source_timeout         time.Duration
		reclaim_interval       time.Duration
		stuck_source_threshold time.Duration
	}
	embedding_pool struct {
		worker_count        int
		poll_interval       time.Duration
		job_timeout         time.Duration
		reclaim_interval    time.Duration
		stuck_job_threshold time.Duration
		batch_size          int
	}
	limiter struct {
		rps     float64
		burst   int
		enabled bool
	}
	cors struct {
		trustedOrigins []string
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
	config           config
	logger           *slog.Logger
	models           data.Models
	workers          *worker.Pool
	embeddingWorkers *worker.EmbeddingPool
	service          *search.Service
	mailer           *mailer.Mailer
	wg               sync.WaitGroup
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
	flag.DurationVar(&cfg.worker_pool.source_timeout, "source-timeout", 90*time.Second, "Timeout for processing a single source")
	flag.DurationVar(&cfg.worker_pool.reclaim_interval, "reclaim-interval", 1*time.Minute, "Interval between reclaiming stale sources")
	flag.DurationVar(&cfg.worker_pool.stuck_source_threshold, "stuck-source-threshold", 10*time.Minute, "Threshold for considering a source as stuck")

	// embedding worker pool settings
	flag.IntVar(&cfg.embedding_pool.worker_count, "embedding-worker-count", 4, "Number of embedding worker goroutines")
	flag.DurationVar(&cfg.embedding_pool.poll_interval, "embedding-poll-interval", 2*time.Second, "Interval between polls for pending embedding jobs")
	flag.DurationVar(&cfg.embedding_pool.job_timeout, "embedding-job-timeout", 5*time.Minute, "Timeout for processing a single embedding job")
	flag.DurationVar(&cfg.embedding_pool.reclaim_interval, "embedding-reclaim-interval", 1*time.Minute, "Interval between reclaiming stuck embedding jobs")
	flag.DurationVar(&cfg.embedding_pool.stuck_job_threshold, "embedding-stuck-job-threshold", 10*time.Minute, "Threshold for considering an embedding job as stuck")
	flag.IntVar(&cfg.embedding_pool.batch_size, "embedding-batch-size", 32, "Target embedding request batch size")

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
	flag.BoolVar(&cfg.rollout.inline_embedding_enabled, "inline-embedding-enabled", false, "Deprecated: inline embedding path has been removed")
	flag.BoolVar(&cfg.rollout.async_embedding_enabled, "async-embedding-enabled", true, "Enable async embedding workflow (required)")
	flag.BoolVar(&cfg.rollout.dual_write_embedding_jobs, "dual-write-embedding-jobs", false, "Deprecated: dual-write path has been removed")

	// CORS settings
	flag.Func("cors-trusted-origins" , "Trusted CORS origin(space separated)" , func(s string) error {
		cfg.cors.trustedOrigins = strings.Fields(s)
		return nil
	})

	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))


	if !cfg.rollout.async_embedding_enabled {
		logger.Error("invalid rollout configuration",
			"reason", "async embedding must be enabled because ingestion now enqueues embedding jobs",
		)
		os.Exit(1)
	}

	if cfg.rollout.inline_embedding_enabled {
		logger.Warn("inline embedding flag is deprecated and ignored; pipeline is async-only now",
			"inline_embedding_enabled", cfg.rollout.inline_embedding_enabled,
		)
		cfg.rollout.inline_embedding_enabled = false
	}

	if cfg.rollout.dual_write_embedding_jobs {
		logger.Warn("dual-write flag is deprecated and ignored; async queue is the only embedding path now",
			"dual_write_embedding_jobs", cfg.rollout.dual_write_embedding_jobs,
		)
		cfg.rollout.dual_write_embedding_jobs = false
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
	nomicOnlineToken := os.Getenv("SUMMERIZER_NOMIC_ONLINE_EMBEDDING_MODEL_TOKEN")
	searchEmbedderClient := embedder.NewEmbedder(
		"",
		"",
		embedder.WithBatchSize(8),
		embedder.WithKeepAlive("5m"),
		embedder.WithNomicOnlineToken(nomicOnlineToken),
		embedder.WithNomicOnlineModel("nomic-embed-text-v1.5"),
		embedder.WithNomicOnlineDimension(768),
	)
	embeddingWorkerEmbedder := embedder.NewEmbedder("", "", embedder.WithBatchSize(cfg.embedding_pool.batch_size), embedder.WithKeepAlive("30m"))
	logStartupPhase("init_embedders", embedderStartedAt)

	if nomicOnlineToken == "" {
		logger.Warn("nomic online token not found; search query embedding will fall back to local embedder")
	} else {
		logger.Info("nomic online query embedding enabled", "model", "nomic-embed-text-v1.5", "dimension", 768)
	}

	pipelineStartedAt := time.Now()
	pipeline := ingestion.NewPipeline(models, logger, webFetcher, textChunker)
	logStartupPhase("init_pipeline", pipelineStartedAt)

	workerPoolStartedAt := time.Now()
	workerPool := worker.NewPool(
		models,
		cfg.worker_pool.worker_count,
		cfg.worker_pool.poll_interval,
		logger,
		pipeline,
		cfg.worker_pool.source_timeout,
		cfg.worker_pool.reclaim_interval,
		cfg.worker_pool.stuck_source_threshold,
	)
	logStartupPhase("init_worker_pool", workerPoolStartedAt)

	embeddingPoolStartedAt := time.Now()
	embeddingPool := worker.NewEmbeddingPool(
		models,
		cfg.embedding_pool.worker_count,
		cfg.embedding_pool.poll_interval,
		logger,
		embeddingWorkerEmbedder,
		cfg.embedding_pool.job_timeout,
		cfg.embedding_pool.reclaim_interval,
		cfg.embedding_pool.stuck_job_threshold,
		cfg.embedding_pool.batch_size,
	)
	logStartupPhase("init_embedding_pool", embeddingPoolStartedAt)

	llmStartedAt := time.Now()
	llmClientGemini, err := gemini.NewGeminiModel("")
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	logStartupPhase("init_llm", llmStartedAt)

	searchStartedAt := time.Now()
	searchService := search.NewService(searchEmbedderClient, models, llmClientGemini)
	logStartupPhase("init_search_service", searchStartedAt)

	appInitStartedAt := time.Now()


	// exp variables for monitoring
	expvar.NewString("version").Set(version)

	expvar.Publish("goroutines" , expvar.Func(func() any {
		return runtime.NumGoroutine()
	}))

	expvar.Publish("db_stats", expvar.Func(func() any {
		return db.Stats()
	}))

	expvar.Publish("timestamp" , expvar.Func(func() any {
		return time.Now().Unix()
	}))

	app := application{
		config:           cfg,
		logger:           logger,
		models:           models,
		workers:          workerPool,
		embeddingWorkers: embeddingPool,
		service:          searchService,
		mailer:           mailerSvc,
	}
	logStartupPhase("init_application", appInitStartedAt)

	workerStartStartedAt := time.Now()
	app.workers.Start(context.Background())
	logStartupPhase("start_ingestion_workers", workerStartStartedAt)

	embeddingWorkerStartStartedAt := time.Now()
	app.embeddingWorkers.Start(context.Background())
	logStartupPhase("start_embedding_workers", embeddingWorkerStartStartedAt)

	logger.Info("startup complete",
		"duration_ms", time.Since(processStartedAt).Milliseconds(),
		"ingestion_worker_count", cfg.worker_pool.worker_count,
		"ingestion_poll_interval", cfg.worker_pool.poll_interval.String(),
		"embedding_worker_count", cfg.embedding_pool.worker_count,
		"embedding_poll_interval", cfg.embedding_pool.poll_interval.String(),
		"embedding_batch_size", cfg.embedding_pool.batch_size,
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
	app.embeddingWorkers.Shutdown()

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
