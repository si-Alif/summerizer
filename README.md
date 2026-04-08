# Summerizer

Summerizer is a Go backend for building topic-based knowledge collections from external resources and querying them with Retrieval-Augmented Generation (RAG).

Users can:

- create collections
- add sources (web/youtube/pdf URL types)
- let background workers ingest and process content
- run semantic search on chunks
- ask grounded questions against their own collection

This project is built to demonstrate production-minded backend engineering: API design, async workers, PostgreSQL + pgvector, retries, and LLM integration.

## Why This Project

When learning a topic, information is scattered across articles, videos, and PDFs. Summerizer turns those scattered resources into a searchable, queryable knowledge base.

## Current Status

Implemented:

- User registration and activation flow
- Token-based authentication endpoint
- Route protection with authenticated + activated user middleware for collection/source/search APIs
- Collection CRUD
- Source CRUD
- Background worker pool with polling and retries
- Ingestion pipeline: fetch -> clean -> chunk -> embed -> store
- Vector search endpoint
- RAG "ask" endpoint using retrieved context
- PostgreSQL migrations with pgvector and HNSW index
- Rate limiting and panic recovery middleware

In progress / known gaps:

- URL type detection supports web/youtube/pdf, but ingestion implementation is currently web-first
- Collection sharing and permission-based access (owner/editor/viewer/public) are not implemented yet
- Test coverage is currently limited (chunker tests exist; broader API/integration tests are pending)

## Architecture (High-Level)

```
Client -> REST API (Go, httprouter) -> Postgres (pgvector)
Worker Pool (goroutines) -> Ingestion Pipeline -> Chunk + Embedding storage
Search Service -> vector similarity -> LLM answer generation with cited source context
```

## Tech Stack

- Go 1.25
- PostgreSQL 16 + pgvector
- pgx
- httprouter
- Docker Compose
- golang-migrate
- Embeddings: local Ollama endpoint (default model: nomic-embed-text)
- LLM generation: Hugging Face chat completions API

## API Endpoints (v1)

Health:

- `GET /v1/healthcheck`

Users and auth:

- `POST /v1/users`
- `PUT /v1/users/activated`
- `POST /v1/tokens/authentication`

Collections:

- `POST /v1/collections`
- `GET /v1/collections`
- `GET /v1/collections/:id`
- `PATCH /v1/collections/:id`
- `DELETE /v1/collections/:id`
- `POST /v1/collections/:id/search`
- `POST /v1/collections/:id/ask`

Sources:

- `POST /v1/collections/:id/sources`
- `GET /v1/collections/:id/sources`
- `GET /v1/sources/:id`
- `DELETE /v1/sources/:id`

Access policy:

- Public routes: `GET /v1/healthcheck`, `POST /v1/users`, `PUT /v1/users/activated`, `POST /v1/tokens/authentication`
- Authenticated + activated required: all collection, source, search, and ask routes

## Local Setup

### Prerequisites

- Go 1.25+
- Docker + Docker Compose
- migrate CLI
- Ollama running locally (for embeddings)
- Hugging Face API token (for answer generation)

### 1) Start Postgres

```bash
make db/start
```

### 2) Set environment variables

```bash
export SUMMERIZER_DB_DSN="postgres://summerizer:pa55word@localhost/summerizer?sslmode=disable"
export SUMMERIZER_HF_API_KEY="<your_hf_api_key>"
```

### 3) Run migrations

```bash
make db/migrations/up
```

### 4) Start API

```bash
make run/api
```

### 5) Verify healthcheck

```bash
curl http://localhost:4000/v1/healthcheck
```

## Example Flow

1. Register user
2. Activate account
3. Create authentication token
4. Create a collection
5. Add a source URL
6. Wait for ingestion to complete
7. Ask a question against the collection

## Engineering Highlights

- **Concurrency**: worker pool with graceful shutdown
- **Resilience**: retry scheduling with exponential backoff for failed source processing
- **Data correctness**: optimistic locking using version columns
- **Search performance**: pgvector cosine similarity + HNSW index
- **API consistency**: structured JSON responses + validation + middleware

## Security and Production Notes

- Do not commit secrets (.envrc, API keys, SMTP creds)
- Use environment-specific configs and secret management
- Keep authorization checks in the data layer even when middleware guards routes
- Add explicit per-collection authorization for sharing (owner/editor/viewer/public)
- Add integration tests and request tracing before production deployment

## Roadmap

- Complete YouTube transcript ingestion
- Complete PDF extraction ingestion
- Add collection sharing + permission-based retrieval/editing
- Add integration + end-to-end tests
- Add observability (metrics/tracing)
- Improve citation formatting in answers

## Why This Is a Strong Portfolio Project

This is not a toy CRUD app. It demonstrates:

- system design across API, workers, storage, and AI integrations
- real-world backend concerns (retries, indexing, migrations, validation, limits)
- practical RAG architecture in Go
