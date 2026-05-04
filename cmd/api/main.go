package main

import (
	"context"
	"database/sql"
	"expvar"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
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
	"github.com/si-Alif/summerizer/internal/vcs"

	// "github.com/si-Alif/summerizer/internal/llm/ollama"
	"github.com/si-Alif/summerizer/internal/search"
	"github.com/si-Alif/summerizer/internal/worker"
)

var (
	version = vcs.Version()
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
		claim_batch_size       int
		max_backoff_interval   time.Duration
	}
	embedding_pool struct {
		worker_count         int
		poll_interval        time.Duration
		job_timeout          time.Duration
		reclaim_interval     time.Duration
		stuck_job_threshold  time.Duration
		batch_size           int
		claim_batch_size     int
		max_backoff_interval time.Duration
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
	debugVarsToken string
}

type application struct {
	config           config
	logger           *slog.Logger
	db               *sql.DB
	models           data.Models
	workers          *worker.Pool
	embeddingWorkers *worker.EmbeddingPool
	service          *search.Service
	mailer           *mailer.Mailer
	wg               sync.WaitGroup
}

func envString(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func envStringSlice(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	return strings.Fields(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func main() {
	processStartedAt := time.Now()

	var cfg config

	defaultPort := envInt("PORT", 4000)
	defaultEnv := envString("ENV", envString("SUMMERIZER_ENV", "development"))
	defaultDBDSN := envString("SUMMERIZER_DB_DSN", envString("DATABASE_URL", ""))
	defaultSMTPHost := envString("SMTP_HOST", "")
	defaultSMTPPort := envInt("SMTP_PORT", 0)
	defaultSMTPUsername := envString("SMTP_USERNAME", "")
	defaultSMTPPassword := envString("SMTP_PASSWORD", "")
	defaultSMTPSender := envString("SMTP_SENDER", "")
	defaultDebugVarsToken := envString("SUMMERIZER_DEBUG_VARS_TOKEN", "")

	cfg.cors.trustedOrigins = envStringSlice("CORS_TRUSTED_ORIGINS")

	flag.IntVar(&cfg.port, "port", defaultPort, "API server port")
	flag.StringVar(&cfg.env, "env", defaultEnv, "Environment (development | production | test)")

	// DB connection pool settings
	flag.StringVar(&cfg.db.dsn, "db-dsn", defaultDBDSN, "PostgreSQL data source name")
	flag.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", 25, "PostgreSQL max open connections")
	flag.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", 25, "PostgreSQL max idle connections")
	flag.DurationVar(&cfg.db.maxIdleTime, "db-max-idle-time", 15*time.Minute, "PostgreSQL max idle time for a connection")

	// worker pool settings
	flag.IntVar(&cfg.worker_pool.worker_count, "worker-count", 10, "Number of worker goroutines")
	flag.DurationVar(&cfg.worker_pool.poll_interval, "poll-interval", 5*time.Second, "Interval between polls for pending sources")
	flag.DurationVar(&cfg.worker_pool.source_timeout, "source-timeout", 90*time.Second, "Timeout for processing a single source")
	flag.DurationVar(&cfg.worker_pool.reclaim_interval, "reclaim-interval", 1*time.Minute, "Interval between reclaiming stale sources")
	flag.DurationVar(&cfg.worker_pool.stuck_source_threshold, "stuck-source-threshold", 10*time.Minute, "Threshold for considering a source as stuck")
	flag.IntVar(&cfg.worker_pool.claim_batch_size, "worker-claim-batch-size", 1, "Number of sources to claim per poll")
	flag.DurationVar(&cfg.worker_pool.max_backoff_interval, "worker-max-backoff-interval", 2*time.Minute, "Max backoff interval when source queue is empty")

	// embedding worker pool settings
	flag.IntVar(&cfg.embedding_pool.worker_count, "embedding-worker-count", 4, "Number of embedding worker goroutines")
	flag.DurationVar(&cfg.embedding_pool.poll_interval, "embedding-poll-interval", 2*time.Second, "Base interval between fallback polls for pending embedding jobs")
	flag.DurationVar(&cfg.embedding_pool.job_timeout, "embedding-job-timeout", 5*time.Minute, "Timeout for processing a single embedding job")
	flag.DurationVar(&cfg.embedding_pool.reclaim_interval, "embedding-reclaim-interval", 1*time.Minute, "Interval between reclaiming stuck embedding jobs")
	flag.DurationVar(&cfg.embedding_pool.stuck_job_threshold, "embedding-stuck-job-threshold", 10*time.Minute, "Threshold for considering an embedding job as stuck")
	flag.IntVar(&cfg.embedding_pool.batch_size, "embedding-batch-size", 32, "Target embedding request batch size")
	flag.IntVar(&cfg.embedding_pool.claim_batch_size, "embedding-claim-batch-size", 5, "Number of embedding jobs to claim per poll")
	flag.DurationVar(&cfg.embedding_pool.max_backoff_interval, "embedding-max-backoff-interval", 2*time.Minute, "Max backoff interval when embedding queue is empty")

	// rate limiter settings
	flag.Float64Var(&cfg.limiter.rps, "limiter-rps", 2, "Rate limiter maximum requests per second")
	flag.IntVar(&cfg.limiter.burst, "limiter-burst", 4, "Rate limiter burst size")
	flag.BoolVar(&cfg.limiter.enabled, "limiter-enabled", true, "Enable rate limiter")

	// SMTP settings
	flag.StringVar(&cfg.smtp.host, "smtp-host", defaultSMTPHost, "SMTP server host")
	flag.IntVar(&cfg.smtp.port, "smtp-port", defaultSMTPPort, "SMTP server port")
	flag.StringVar(&cfg.smtp.username, "smtp-username", defaultSMTPUsername, "SMTP server username")
	flag.StringVar(&cfg.smtp.password, "smtp-password", defaultSMTPPassword, "SMTP server password")
	flag.StringVar(&cfg.smtp.sender, "smtp-sender", defaultSMTPSender, "Email address of the sender")
	flag.StringVar(&cfg.debugVarsToken, "debug-vars-token", defaultDebugVarsToken, "Debug vars access token")

	showVer := flag.Bool("version", false, "Show version and exit")

	// CORS settings
	flag.Func("cors-trusted-origins", "Trusted CORS origin(space separated)", func(s string) error {
		cfg.cors.trustedOrigins = strings.Fields(s)
		return nil
	})

	flag.Parse()

	if *showVer {
		fmt.Printf("version: \t%s\n", version)
		os.Exit(0)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	if strings.TrimSpace(cfg.db.dsn) == "" {
		logger.Error("missing database DSN", "hint", "set SUMMERIZER_DB_DSN or DATABASE_URL (or use -db-dsn)")
		os.Exit(1)
	}

	missingSMTP := []string{}
	if strings.TrimSpace(cfg.smtp.host) == "" {
		missingSMTP = append(missingSMTP, "SMTP_HOST")
	}
	if cfg.smtp.port == 0 {
		missingSMTP = append(missingSMTP, "SMTP_PORT")
	}
	if strings.TrimSpace(cfg.smtp.username) == "" {
		missingSMTP = append(missingSMTP, "SMTP_USERNAME")
	}
	if strings.TrimSpace(cfg.smtp.password) == "" {
		missingSMTP = append(missingSMTP, "SMTP_PASSWORD")
	}
	if strings.TrimSpace(cfg.smtp.sender) == "" {
		missingSMTP = append(missingSMTP, "SMTP_SENDER")
	}
	if len(missingSMTP) > 0 {
		logger.Error("missing SMTP configuration", "missing", strings.Join(missingSMTP, ", "))
		os.Exit(1)
	}

	geminiAPIKey := firstNonEmpty(
		os.Getenv("SUMMERIZER_GEMINI_API_KEY"),
		os.Getenv("GEMINI_API_KEY"),
		os.Getenv("GOOGLE_API_KEY"),
	)
	if geminiAPIKey == "" {
		logger.Error("missing Gemini API key", "hint", "set SUMMERIZER_GEMINI_API_KEY, GEMINI_API_KEY, or GOOGLE_API_KEY")
		os.Exit(1)
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
	nomicOnlineToken := strings.TrimSpace(os.Getenv("SUMMERIZER_NOMIC_ONLINE_EMBEDDING_MODEL_TOKEN"))

	if nomicOnlineToken == "" {
		logger.Error("nomic online token not found; embeddings require nomic")
		os.Exit(1)
	}

	searchEmbedderClient, err := embedder.NewEmbedder(
		embedder.NomicOnlineEmbedderType,
		embedder.WithBatchSize(8),
		embedder.WithNomicToken(nomicOnlineToken),
		embedder.WithNomicModel("nomic-embed-text-v1.5"),
		embedder.WithNomicDimension(768),
		embedder.WithMaxIdleConnsPerHost(cfg.embedding_pool.worker_count),
	)
	if err != nil {
		logger.Error("failed to init search embedder", "error", err)
		os.Exit(1)
	}

	embeddingWorkerEmbedder, err := embedder.NewEmbedder(
		embedder.NomicOnlineEmbedderType,
		embedder.WithBatchSize(cfg.embedding_pool.batch_size),
		embedder.WithNomicToken(nomicOnlineToken),
		embedder.WithNomicModel("nomic-embed-text-v1.5"),
		embedder.WithNomicDimension(768),
		embedder.WithMaxIdleConnsPerHost(cfg.embedding_pool.worker_count),
	)
	if err != nil {
		logger.Error("failed to init embedding worker embedder", "error", err)
		os.Exit(1)
	}
	logStartupPhase("init_embedders", embedderStartedAt)

	logger.Info("nomic online embedding enabled", "model", "nomic-embed-text-v1.5", "dimension", 768)

	pipelineStartedAt := time.Now()
	pipeline := ingestion.NewPipeline(models, logger, webFetcher, textChunker)
	logStartupPhase("init_pipeline", pipelineStartedAt)

	workerPoolStartedAt := time.Now()
	workerPool := worker.NewPool(
		models,
		logger,
		pipeline,
		cfg.db.dsn,
		worker.WithIngestionWorkerCount(cfg.worker_pool.worker_count),
		worker.WithIngestionPollInterval(cfg.worker_pool.poll_interval),
		worker.WithIngestionSourceTimeout(cfg.worker_pool.source_timeout),
		worker.WithIngestionReclaimInterval(cfg.worker_pool.reclaim_interval),
		worker.WithIngestionStuckSourceThreshold(cfg.worker_pool.stuck_source_threshold),
		worker.WithIngestionClaimBatchSize(cfg.worker_pool.claim_batch_size),
		worker.WithIngestionMaxBackoffInterval(cfg.worker_pool.max_backoff_interval),
	)
	logStartupPhase("init_worker_pool", workerPoolStartedAt)

	embeddingPoolStartedAt := time.Now()
	embeddingPool := worker.NewEmbeddingPool(
		models,
		logger,
		embeddingWorkerEmbedder,
		cfg.db.dsn,
		worker.WithWorkerCount(cfg.embedding_pool.worker_count),
		worker.WithPollInterval(cfg.embedding_pool.poll_interval),
		worker.WithJobTimeout(cfg.embedding_pool.job_timeout),
		worker.WithReclaimInterval(cfg.embedding_pool.reclaim_interval),
		worker.WithStuckJobThreshold(cfg.embedding_pool.stuck_job_threshold),
		worker.WithBatchSize(cfg.embedding_pool.batch_size),
		worker.WithClaimBatchSize(cfg.embedding_pool.claim_batch_size),
		worker.WithMaxBackoffInterval(cfg.embedding_pool.max_backoff_interval),
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

	expvar.Publish("goroutines", expvar.Func(func() any {
		return runtime.NumGoroutine()
	}))

	expvar.Publish("db_stats", expvar.Func(func() any {
		return db.Stats()
	}))

	expvar.Publish("timestamp", expvar.Func(func() any {
		return time.Now().Unix()
	}))

	app := application{
		config:           cfg,
		logger:           logger,
		db:               db,
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
