# Summerizer

## TL;DR

- Built a production-style RAG backend in Go with async ingestion and embedding pipelines
- Uses PostgreSQL + pgvector (HNSW) for semantic retrieval
- Designed for reliability under concurrency (job queues, retries, failure recovery)
- Built with production-oriented constraints: reliability, failure isolation, and observability.
- Measured improvements: cold start 30s → 8s, Hit@1 92%, p95 search latency ~1.18s🐱

---

## Why I Built This

I wanted to understand what actually goes wrong in a RAG system once you move past the happy path demo. Fetching text, calling an embedding API, and doing cosine similarity is trivial. What I wanted to figure out was everything _after_ that. What happens when the embedding model is cold? What happens when two workers race to claim the same source? What happens when the pipeline crashes halfway through ingestion and the source just... sits there forever?

So I built Summerizer as a real backend project ; not a prototype, not a notebook ; with queue-based ingestion, worker pools, consistency controls, vector retrieval, and phase-based evaluation to prove that changes actually helped. Every design decision came from either hitting a problem or actively thinking about what would break next.

---

## What It Does

- Users create collections and attach external sources (currently web ingestion)
- Sources go through an async pipeline: fetch → clean → chunk → store → embed
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
    IW->>DB: Claim pending source (SKIP LOCKED)
    IW->>IW: fetch → clean → chunk
    IW->>DB: Store chunks
    IW->>DB: Enqueue embedding job
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

## The Journey 🐱

I didn't start with this architecture. I started with the obvious thing: inline embedding ; fetch a source, chunk it, embed it, all in one synchronous pipeline, all in the same worker. It worked fine until it didn't.

The moment I added concurrency, the cracks showed up fast. The embedding model has a cold start. If ten sources get ingested simultaneously and all try to embed at the same time, half of them time out, half of those retry, and suddenly everything is waiting on a model that's already overloaded. Ingestion and embedding were fighting over the same resources, and a flaky embedding run would mark an entire source as failed - even if the fetch and chunk steps had worked perfectly.

So I split them. Ingestion became its own queue, its own workers, its own completion state. Once chunks are stored, the pipeline enqueues an `embedding_job` and returns. A completely separate pool of embedding workers picks those up on their own schedule. Now a cold model only affects embedding latency ; it doesn't corrupt ingestion state or block new sources from being processed.

That one change dropped cold startup time from ~30s to ~8s between phase0 and phase2 captures. Not because the embedding got faster ; because ingestion was no longer waiting for it.

The other thing I kept running into was correctness under concurrency. With multiple workers polling the same queue, you need to guarantee that two workers can't claim the same source. The naive solution is a status flag ; set it to `processing` before you start. But there's a race window between the SELECT that sees a pending source and the UPDATE that marks it processing. I used `FOR UPDATE SKIP LOCKED` inside a transaction so the lock and the status update are atomic. No race window, no double-processing .

I also added optimistic versioning to every state transition. Every time the pipeline updates a source ; step by step as it moves through fetch → clean → chunk → store → embed ; it bumps a version field. If two things try to write to the same record simultaneously, the second one gets an edit conflict error instead of silently overwriting. This surfaces concurrency bugs as explicit failures rather than mysterious state corruption.

The cleaner was its own rabbit hole. HTML from the real web is not clean. The first version just pulled all `<p>` tags and called it done. That broke immediately on sites where every line is wrapped in `<li>`, or where nav/footer content bleeds into the body, or where the actual content is buried under layers of nesting. The current cleaner converts to Markdown first (best quality), falls back to legacy HTML traversal if that fails, and falls back to plain text as a last resort. It also bails out of Markdown extraction if it returns more than 2,000 blocks , because that's almost always a sign the HTML structure is broken, not that the page is genuinely that dense.

By the end I had phase-based logs, DB snapshots, and E2E automation scripts ; not because I planned to from the start, but because I kept needing to prove to myself that a change had actually helped. That habit of measuring before claiming turned out to be the most useful thing I built.

---

## Engineering Decisions

| Problem                                                      | Decision                                                                                                            | Why It Matters                                                                                                    |
| ------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| Inline embedding made ingestion fragile under model pressure | Split into two independent queues: `sources` (ingestion) + `embedding_jobs` (embedding) with dedicated worker pools | Ingestion completes fast. Embedding failures don't cascade backwards. Query path stays unaffected.                |
| Concurrent workers racing to claim the same source           | `FOR UPDATE SKIP LOCKED` inside a transaction + optimistic versioning on every state transition                     | No double-processing. Edit conflicts are explicit errors, not silent corruption.                                  |
| Vector search without another service to run                 | PostgreSQL + `pgvector` (`vector(768)`) + HNSW cosine index                                                         | One fewer thing to operate. Retrieval performance holds at this scale and the operational simplicity is worth it. |
| Embedding backend flexibility                                | Configurable backend (Nomic online by default, Ollama offline optional)                                             | Lets you trade cost/latency for local control without redesigning the pipeline.                                   |
| "It's better now" isn't evidence                             | Phase-based logs, DB snapshots, strict retrieval evals, E2E automation                                              | Every optimization claim is backed by a captured measurement. The numbers are in `tmp/`.                          |

---

## Measured Outcomes

From captured evaluation runs , not written for the README 🐱.

| Metric                    | Value                                                 |
| ------------------------- | ----------------------------------------------------- |
| Retrieval Hit@1           | `0.923`                                               |
| Retrieval Hit@5           | `1.00`                                                |
| MRR                       | `0.962`                                               |
| Search p95 latency        | `1.177s` (strict eval set)                            |
| Grounded answer cite rate | `0.846`                                               |
| Cold startup time         | ~`30s` → ~`8s` (phase0 → phase2)                      |
| E2E phase2 run            | 5/5 sources completed, search + ask returned HTTP 200 |

Treat these as workload-specific engineering evidence, not universal benchmarks. The value isn't the specific numbers — it's that there are numbers at all.

---

## Tech Stack

- **Go 1.25**
- **PostgreSQL 16 + pgvector** -> storage, queue state, and vector index all in one place
- **httprouter, pgx** -> no framework, deliberate choices
- **Embedder** -> Nomic online (default) or Ollama offline; backend chosen at startup
- **LLM** -> Gemini API for answer generation
- **Docker Compose** -> local infra only

---

## Current Scope

Working:

- User registration, activation, auth tokens
- Collection and source management APIs
- Async ingestion workers + async embedding workers
- Full pipeline with retries, reclaim loops, and step-tagged failure tracking
- Semantic search and grounded ask with source citations
- PostgreSQL migrations with HNSW vector index strategy
- Middleware: auth, activation gate, rate limiter, panic recovery
- Embedding backend selection (online Nomic by default; offline Ollama optional)

Honest gaps 🐱:

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

Runs a full collection → sources → search → ask scenario and drops artifacts under `tmp/phase2/e2e/<run_id>/`.

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

## If you're reviewing the code, I'd appreciate feedback on the following areas 🐱

- **`pool.go` + `embedding_pool.go`** ⟹ how the two worker pools handle panics, context cancellation on shutdown, and the `firstPollOnce` / `firstClaimOnce` instrumentation for measuring startup latency
- **`pipeline.go`** ⟹ step-tagged state transitions and `classifyFailure`, which separates permanent HTTP failures (don't retry) from transient ones (do retry)
- **`embedding_jobs.go`** ⟹ `ClaimPending` does SELECT and UPDATE in one transaction so there's no race window between seeing a job and locking it
- **`chunker.go`** ⟹ the token budget math (prefix tokens count against the limit) and the sentence splitter's abbreviation list
- **`cleaner.go`** ⟹ three-strategy fallback and the Markdown parser state machine that tracks section hierarchy across headings

The design didn't come out this way on the first try. The commit history shows it 😸.
