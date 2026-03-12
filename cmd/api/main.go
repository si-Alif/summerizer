package main

import (
	"context"
	"database/sql"
	"flag"
	"log/slog"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/ingestion"
	"github.com/si-Alif/summerizer/internal/ingestion/chunker"
	"github.com/si-Alif/summerizer/internal/ingestion/embedder"
	"github.com/si-Alif/summerizer/internal/ingestion/fetcher"
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
	worker_pool struct{
		worker_count int
		poll_interval time.Duration
	}
}

type application struct {
	config config
	logger *slog.Logger
	models data.Models
	workers *worker.Pool
}

func main() {

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

	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// open a database connection pool, verify connectivity, and handle any errors
	db, err := openDB(cfg)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	defer db.Close()

	logger.Info("database connection pool established")

	models := data.NewModels(db)

	webFetcher := fetcher.NewFetcher()

	textChunker , err := chunker.New(400 , 1)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	embedder := embedder.NewEmbedder("" , "")

	pipeline := ingestion.NewPipeline(models , logger , webFetcher , textChunker , embedder)

	worker_pool := worker.NewPool(models , cfg.worker_pool.worker_count , cfg.worker_pool.poll_interval , logger , pipeline)

	app := application{
		config: cfg,
		logger: logger,
		models: models,
		workers: worker_pool,
	}

	app.workers.Start(context.Background())

	err = app.serve()

	// if any error happens in the codebase during , then it logs the error and the process exits
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
