# Summerizer

## TL;DR

- Production-style RAG backend in Go with async ingestion and embedding pipelines
- PostgreSQL + pgvector (HNSW) for semantic retrieval
- Reliability under concurrency: job queues, retries, SKIP LOCKED, optimistic versioning
- Hybrid LISTEN/NOTIFY + exponential backoff reduces idle DB polling without hurting latency
- Measured outcomes: cold start 30s -> 8s, Hit@1 0.923, p95 search ~1.18s 😺

---

## What It Does

- Users create collections and attach external sources (currently web ingestion)
- Sources go through an async pipeline: fetch -> clean -> chunk -> store -> embed
- Chunks + vectors live in PostgreSQL (`pgvector`) for semantic retrieval
- Collections support scoped search and grounded Q&A with source citations

---

## System Architecture

```mermaid
flowchart LR
    C[Client / Frontend] --> API[Go REST API<br/>cmd/api]

    API --> AUTH[Auth + Middleware<br/>rate-limit, recover, activate]
    AUTH --> APP[Application Services]

    APP --> COL[Collections + Sources]
    APP --> SEARCH[Search Service]
    APP --> ASK[Ask Service]

    COL --> DB[PostgreSQL + pgvector]

    COL --> INQ[sources queue state]
    INQ --> IW[Ingestion Workers]
    IW --> PIPE[Ingestion Pipeline<br/>fetch -> clean -> chunk -> store]
    PIPE --> DB

    PIPE --> EJQ[embedding_jobs queue]
    EJQ --> EW[Embedding Workers]
    EW --> EMB[Embedder]
    EMB --> DB

    SEARCH --> DB
    SEARCH --> QEMB[Query Embedder<br/>Nomic online - default<br/>Ollama offline - optional]
    QEMB --> DB

    ASK --> SEARCH
    ASK --> LLM[LLM Provider]
```

---

## End-to-End Flow

```mermaid
sequenceDiagram
    participant U as User
    participant API as API
    participant IW as Ingestion Worker
    participant EW as Embedding Worker
    participant DB as PostgreSQL
    participant LLM as LLM

    U->>API: Add source URL to collection
    API->>DB: Insert source (pending)
    DB-->>IW: pg_notify('sources', id) via trigger
    IW->>DB: Claim pending source (SKIP LOCKED)
    IW->>IW: fetch -> clean -> chunk
    IW->>DB: Store chunks
    IW->>DB: Enqueue embedding job
    DB-->>EW: pg_notify('embedding_jobs', id) via trigger
    EW->>DB: Claim embedding job (SKIP LOCKED)
    EW->>DB: Write chunk embeddings
    EW->>DB: Mark source completed

    U->>API: /search or /ask
    API->>DB: Vector retrieval (HNSW cosine)
    API->>LLM: Grounded prompt with retrieved chunks
    LLM-->>API: Answer + citations
    API-->>U: JSON response
```

---

## Why I Built This

I wanted to see what breaks in a RAG system once you move beyond the demo path. Embedding and cosine similarity are the easy part. The hard part is everything after that: concurrency, retries, data consistency, and keeping the system reliable under load.

Summerizer is a real backend project (not a notebook): queue-based ingestion, worker pools, and phase-based measurement to prove improvements. Every design decision came from production-style failures or a “what would break next?” mindset.

---

## Worker Pool Design (Hybrid Scheduling)

- One dedicated `pgx.Conn` per pool runs `LISTEN` and blocks on `WaitForNotification`.
- A buffered wake channel (size 1) coalesces bursts, preventing a thundering herd on the DB.
- Workers select on wake signal, fallback timer, and context cancellation.
- Fallback polling uses exponential backoff up to a configurable max interval.
- A reconnect loop keeps the listener alive across DB restarts or network blips.

---

## Engineering Decisions

| Problem                                                      | Decision                                                                 | Why It Matters                                                        |
| ------------------------------------------------------------ | ------------------------------------------------------------------------ | --------------------------------------------------------------------- |
| Inline embedding made ingestion fragile under model pressure | Split into two queues: `sources` + `embedding_jobs` with dedicated pools | Ingestion completes fast. Embedding failures do not cascade backward. |
| Concurrent workers racing to claim the same source           | `FOR UPDATE SKIP LOCKED` in a transaction + optimistic versioning        | No double-processing. Conflicts are explicit.                         |
| Fixed-interval polling burning DB calls when idle            | Hybrid LISTEN/NOTIFY + exponential backoff + slow fallback poll          | Workers wake instantly on new work; idle polling stays quiet.         |
| Notification bursts causing thundering herd                  | Buffered wake channel (size 1) with non-blocking send                    | 20 inserts cause 1 poll, not 20.                                      |
| Vector search without another service to run                 | PostgreSQL + `pgvector` (`vector(768)`) + HNSW cosine index              | One fewer system to operate.                                          |
| Embedding backend flexibility                                | Nomic online by default, Ollama offline optional                         | Trade cost/latency vs. local control without redesign.                |

---

## Measured Outcomes

From captured evaluation runs:

| Metric                    | Value                                                 |
| ------------------------- | ----------------------------------------------------- |
| Retrieval Hit@1           | `0.923`                                               |
| Retrieval Hit@5           | `1.00`                                                |
| MRR                       | `0.962`                                               |
| Search p95 latency        | `1.177s` (strict eval set)                            |
| Grounded answer cite rate | `0.846`                                               |
| Cold startup time         | ~`30s` -> ~`8s` (phase0 -> phase2)                    |
| E2E phase2 run            | 5/5 sources completed, search + ask returned HTTP 200 |

Treat these as workload-specific evidence, not universal benchmarks.

---

## Operational Evidence: DB Polling Snapshots (Production)

These are cumulative `pg_stat_statements` counts from Render. The slope between snapshots is what matters.

| Snapshot (local time) | sources ClaimPending SELECT count | embedding_jobs ClaimPending SELECT count | Notes                                                                  |
| --------------------- | --------------------------------- | ---------------------------------------- | ---------------------------------------------------------------------- |
| 15:47                 | 11,684                            | 11,672                                   | Fixed-interval polling on both pools                                   |
| 16:06                 | 12,324                            | 11,700                                   | Embedding LISTEN/NOTIFY deployed; LISTEN visible in active connections |
| 16:13                 | 13,314                            | 11,716                                   | Embedding count grows slowly while sources keep polling                |
| 21:51                 | 17,534                            | 11,848                                   | Both pools on hybrid; LISTEN sources + embedding_jobs visible          |

---

## Tech Stack

- **Go 1.25**
- **PostgreSQL 16 + pgvector** -> storage, queue state, and vector index in one place
- **httprouter, pgx** -> no framework, deliberate choices
- **Embedder** -> Nomic online (default) or Ollama offline; backend chosen at startup
- **LLM** -> Gemini API for answer generation
- **Docker Compose** -> local infra only

---

## Current Scope

Working:

- User registration, activation, auth tokens
- Collection and source management APIs
- Async ingestion workers + async embedding workers (hybrid LISTEN/NOTIFY scheduling)
- Full pipeline with retries, reclaim loops, and step-tagged failure tracking
- Semantic search and grounded ask with source citations
- PostgreSQL migrations with HNSW vector index strategy
- Middleware: auth, activation gate, rate limiter, panic recovery
- Embedding backend selection (online Nomic by default; offline Ollama optional)

Honest gaps:

- URL type detection covers `web/youtube/pdf` but only web ingestion is implemented right now
- Sharing/permissions model (owner/editor/viewer/public) is designed, not built yet
- Integration test coverage and deployment automation are in progress

---

## Chunking Strategy

- Token-aware chunking using the `cl100k_base` tokenizer
- Default target budget: 400 tokens per chunk with 1-sentence overlap
- Prefix budget is enforced: document title + section path are added to `EmbedText`, and token budget is reduced accordingly
- Section-aware boundaries: chunks do not cross section path changes
- Overlap only applies to paragraph/list content (no overlap for code/table blocks)
- Oversized units split by words; code/table blocks prefer line-based splitting
- List blocks are split from semicolon or markdown list formats; sentence splitter handles common abbreviations
- Chunk metadata includes section title/path, heading level, block type(s), and embed-text token counts

## Fetcher Optimizations

- Custom HTTP transport tuned for throughput (HTTP/2, keep-alives, connection limits)
- Per-attempt timeout (15s) + total fetch timeout (90s)
- Retry policy: max 3 attempts with exponential backoff + jitter, honors Retry-After
- Max body size 10MB; reads one extra byte to detect oversized payloads
- Rejects non-HTTP(S) URLs and non-HTML content types early
- Uses readability extraction; fails fast on empty content

---

## Local Setup

### Prerequisites

- Go 1.25+
- Docker + Docker Compose
- `migrate` CLI
- Ollama (for local embedding path)
- Gemini API key

### Run It

```bash
# Start PostgreSQL
make db/start

# Export env
export SUMMERIZER_DB_DSN="postgres://summerizer:pa55word@localhost/summerizer?sslmode=disable"
export SUMMERIZER_GEMINI_API_KEY="<your_key>"
export SUMMERIZER_GEMINI_MODEL="gemini-3-flash-preview"

# Migrations
make db/migrations/up

# API
make run/api

# Verify
curl http://localhost:4000/v1/healthcheck
```

---

## Render Deployment

### Required Environment Variables

- `PORT`
- `DATABASE_URL` or `SUMMERIZER_DB_DSN`
- `SUMMERIZER_NOMIC_ONLINE_EMBEDDING_MODEL_TOKEN`
- `SUMMERIZER_GEMINI_API_KEY` (or `GEMINI_API_KEY` / `GOOGLE_API_KEY`)
- `SMTP_HOST`
- `SMTP_PORT`
- `SMTP_USERNAME`
- `SMTP_PASSWORD`
- `SMTP_SENDER`
- `CORS_TRUSTED_ORIGINS`
- `SUMMERIZER_DEBUG_VARS_TOKEN`

### Render Settings (Docker)

- Use the Dockerfile in this repo.
- The container entrypoint runs migrations at startup and then launches the API.
- By default the container runs with `-env=production` (see Dockerfile `CMD`).

### Migrations

The entrypoint resolves the DSN in this order:

1. `SUMMERIZER_DB_DSN`
2. `DATABASE_URL`

It then runs:

- `migrate -path /app/migrations -database "$DSN" up`

### Debug Vars

Set `SUMMERIZER_DEBUG_VARS_TOKEN` and query with:

- `curl -H "X-Debug-Token: $SUMMERIZER_DEBUG_VARS_TOKEN" https://<host>/debug/vars`

### Fast Verification

```bash
make phase2/run/e2e
```

Runs a full collection -> sources -> search -> ask scenario and drops artifacts under `tmp/phase2/e2e/<run_id>/`.

---

## API Surface (v1)

**Public**

```
GET  /v1/healthcheck
POST /v1/users
PUT  /v1/users/activated
POST /v1/tokens/authentication
```

**Authenticated + activated**

```
POST   /v1/collections
GET    /v1/collections
GET    /v1/collections/:id
PATCH  /v1/collections/:id
DELETE /v1/collections/:id
POST   /v1/collections/:id/sources
GET    /v1/collections/:id/sources
GET    /v1/sources/:id
DELETE /v1/sources/:id
POST   /v1/collections/:id/search
POST   /v1/collections/:id/ask
```

---

## If you're reviewing the code, I'd appreciate feedback on the following areas

- **`pool.go` + `embedding_pool.go`** -> hybrid LISTEN/NOTIFY scheduler: wake channel coalescing, backoff on empty polls, listener reconnect loop
- **`pipeline.go`** -> step-tagged state transitions and `classifyFailure` (permanent vs transient errors)
- **`embedding_jobs.go`** -> `ClaimPending` does SELECT + UPDATE in one transaction
- **`chunker.go`** -> token budget math and sentence splitter
- **`cleaner.go`** -> three-strategy fallback and Markdown parser state machine

The design did not come out this way on the first try. The commit history shows it.
