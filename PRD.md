# Summerizer — Product Requirements Document (PRD)



> A step-by-step guide to building the system from zero to production-ready. Each step tells you WHAT to do, WHY you're doing it, and WHAT comes next.



---



## Table of Contents

1. [Product Overview](#1-product-overview)
2. [System Architecture](#2-system-architecture)
3. [Step-by-Step Implementation Guide](#3-step-by-step-implementation-guide)
	- [Step 1: Project Scaffolding & Infrastructure](#step-1-project-scaffolding--infrastructure)
	- [Step 2: HTTP Server & Healthcheck](#step-2-http-server--healthcheck)
	- [Step 3: Database Connection & Migrations Setup](#step-3-database-connection--migrations-setup)
	- [Step 4: User Registration](#step-4-user-registration)
	- [Step 5: Authentication (JWT)](#step-5-authentication-jwt)
	- [Step 6: Collection CRUD](#step-6-collection-crud)
	- [Step 7: Source CRUD](#step-7-source-crud)
	- [Step 8: Background Worker Pool](#step-8-background-worker-pool)
	- [Step 9: Web Page Ingestion Pipeline](#step-9-web-page-ingestion-pipeline)
	- [Step 10: YouTube Transcript Ingestion](#step-10-youtube-transcript-ingestion)
	- [Step 11: PDF Ingestion](#step-11-pdf-ingestion)
	- [Step 12: Embedding Integration](#step-12-embedding-integration)
	- [Step 13: Vector Search Endpoint](#step-13-vector-search-endpoint)
	- [Step 14: LLM Answer Generation](#step-14-llm-answer-generation)
	- [Step 15: Polish & Hardening](#step-15-polish--hardening)

4. [API Reference](#4-api-reference)
5. [Database Schema](#5-database-schema)
6. [Dependencies](#6-dependencies)
7. [Glossary](#7-glossary)



## 1. Product Overview

### What Is Summerizer?

Summerizer is a backend API that lets users create **collections** of learning resources — web pages, YouTube videos, PDFs — and then **ask questions** about them. The system processes each resource (scrape, extract, chunk, embed), stores it as searchable vectors, and uses an LLM to generate answers grounded in the user's own material.

### Who Is It For?
Students, researchers, or self-learners who want to aggregate resources on a topic and query them conversationally instead of re-reading everything.


### What Problem Does It Solve?

You have 15 browser tabs, 3 YouTube videos, and 2 PDFs on a topic. You want to ask "How does X relate to Y?" and get an answer that draws from ALL of those sources — not just one. That's what Summerizer does.



### Core User Flow

```

User registers  → creates a Collection ("Go Concurrency")
				→ adds sources (URLs, YouTube links, PDFs)
				→ system processes each source in the background:
					fetch content → clean text → split into chunks → generate                        embeddings → store vectors

→ user asks: "How do goroutines differ from threads?"
			→ system embeds the question
			→ finds most relevant chunks via vector similarity
			→ sends chunks + question to LLM
			→ returns an answer with source citations
```



### What This Project Demonstrates (for your resume)

- **RAG (Retrieval-Augmented Generation)** architecture — the most in-demand AI backend pattern
- **Async processing** — background workers, job queues, state machines
- **Database design** — relational schema with pgvector, optimistic locking, indexes
- **Concurrency in Go** — worker pools, graceful shutdown, panic recovery
- **Clean architecture** — interfaces for external deps, consistent error handling, structured logging
- **Production practices** — migrations, Docker, rate limiting, pagination, API versioning

---

## 2. System Architecture

### High-Level Data Flow

```
┌──────────┐     ┌──────────────┐     ┌──────────────┐
│  Client  │────▶│  HTTP Server │────▶│   Postgres   │
│  (curl/  │◀────│  (httprouter)│◀────│  + pgvector  │
│   app)   │     └──────┬───────┘     └──────┬───────┘
└──────────┘            │                    │
                        │ starts             │ polls
                        ▼                    │
                 ┌──────────────┐            │
                 │ Worker Pool  │◀───────────┘
                 │(N goroutines)│
                 └──────┬───────┘
                        │ calls
                        ▼
              ┌─────────────────────┐
              │  Ingestion Pipeline │
              │  fetch → clean →    │
              │  chunk → embed →    │     ┌───────────────┐
              │  store              │────▶│ Embedding API │
              └─────────────────────┘     │(HuggingFace/  │
                                          │ Ollama/OpenAI)│
                                          └───────────────┘
```


### How the Pieces Connect

| Component               | Lives In              | Talks To                                      | Purpose                                                          |
| ----------------------- | --------------------- | --------------------------------------------- | ---------------------------------------------------------------- |
| **HTTP Handlers**       | `cmd/server/*.go`     | `internal/data/`, `internal/search/`          | Accept requests, validate input, return responses                |
| **Data Layer**          | `internal/data/`      | Postgres via `pgx`                            | All DB reads and writes — models + query methods                 |
| **Worker Pool**         | `internal/worker/`    | `internal/data/`, `internal/ingestion/`       | Claims pending sources from DB, dispatches to pipeline           |
| **Ingestion Pipeline**  | `internal/ingestion/` | Fetchers, Chunker, Embedder, `internal/data/` | Processes a source through fetch → clean → chunk → embed → store |
| **Search**              | `internal/search/`    | Embedder, LLMClient, `internal/data/`         | Embeds query, retrieves similar chunks, generates LLM answer     |
| **Postgres + pgvector** | Docker container      | —                                             | Stores all data + vector embeddings + HNSW index                 |


### The `application` Struct (the glue)

  Everything connects through a single struct in `cmd/server/server.go`:

```go

type application struct {
	config config
	logger *slog.Logger
	models data.Models
	worker *worker.Pool // added in Step 8

	// search and ingestion refs added in later steps

}

```



Handlers are methods on this struct:

> `func (app *application) createCollectionHandler(w http.ResponseWriter, r *http.Request)`.

This gives every handler access to the DB, logger, and config without global variables.

---

## 3. Step-by-Step Implementation Guide


> Each step is designed to produce something **runnable and testable** before moving to the next. Don't skip ahead — each step builds on the previous one.


### Step 1: Project Scaffolding & Infrastructure


**WHAT you're doing**:
	Setting up the project skeleton, Go module, Docker, Makefile — the foundation everything else sits on.


**WHY**: Starting with infrastructure means every subsequent step has a consistent environment. You'll never hear "it works on my machine" because Docker ensures the same Postgres everywhere.


**Tasks**:
1. **Initialize Go module**

```bash

mkdir -p summerizer && cd summerizer

go mod init github.com/<your-username>/summerizer

```



2. **Create the directory structure**

```bash
mkdir -p cmd/server

mkdir -p internal/data

mkdir -p internal/validator

mkdir -p internal/vcs

mkdir -p internal/worker

mkdir -p internal/ingestion/fetcher

mkdir -p internal/ingestion/chunker

mkdir -p internal/search

mkdir -p migrations

mkdir -p remote

```



3. **Create `docker-compose.yml`**

- Postgres 16 with pgvector: image `pgvector/pgvector:pg16`
- Expose port 5432
- Set `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`
- Mount a named volume for data persistence
- This gives you a local database with vector support in one command: `docker compose up -d`



4. **Create `Makefile`** with these targets:
	- `run` — `go run ./cmd/server` (with env vars for DB DSN, JWT secret, etc.)
	- `build` — `go build` with `-ldflags` to embed version info
	- `migrate-up` — run all pending migrations
	- `migrate-down` — roll back the last migration
	- `audit` — `go vet` + `staticcheck` + `golangci-lint` + `go test -race`
	- `vendor` — `go mod tidy && go mod verify && go mod vendor`


5. **Create `.gitignore`** — include: `.envrc`, `vendor/` (optional), binary output
6. **Create `.envrc`** — store `SUMMERIZER_DB_DSN`, `SUMMERIZER_JWT_SECRET` etc. Add to `.gitignore` immediately.
7. **Create `internal/vcs/vcs.go`** — a small package that uses `runtime/debug.ReadBuildInfo()` to extract the git commit hash at build time. You'll use this in the healthcheck response.


**HOW to verify**:
- `docker compose up -d` starts Postgres.
- `psql` connects to it.
- `go build ./cmd/server` compiles (even if `main.go` is just a stub with `func main() {}`).


**WHAT comes next**: You have a project that compiles and a database that runs. Next, you'll put an HTTP server in front of it.

---

### Step 2: HTTP Server & Healthcheck


**WHAT you're doing**:
Building the HTTP server skeleton — the `application` struct, router, middleware, error helpers, and your first endpoint (`/v1/healthcheck`).


**WHY**: This establishes all the HTTP plumbing that every future endpoint uses. Error format, JSON helpers, middleware chain — do it once, use it forever. The healthcheck endpoint proves the server is alive and will be used by Docker/load balancers later.


**Tasks**:
1. **`cmd/server/main.go`**
	- Define the `config` struct (port, env, db DSN, jwt secret, worker settings, limiter settings)
	- Parse config from environment variables or flags
	- Create a `*slog.Logger` (text handler for dev, JSON for production)
	- For now, just start the HTTP server (DB connection comes in Step 3)
2. **`cmd/server/server.go`**
	- Define the `application` struct: holds `config`, `logger`
	- Define `func (app *application) serve() error` — creates `http.Server`, calls `app.routes()`, handles graceful shutdown with `signal.NotifyContext`
3. **`cmd/server/router.go`**
	- `func (app *application) routes() http.Handler` — creates httprouter, registers routes, wraps with middleware
	- For now, just one route: `GET /v1/healthcheck`
	- Return the router wrapped in `app.recoverPanic()` middleware
4. **`cmd/server/middleware.go`**
	- `recoverPanic` — catches panics in handlers, logs the stack trace, returns 500 to client. Without this, a panic kills your entire server.
5. **`cmd/server/helpers.go`**
	- `writeJSON(w, status, data, headers)` — marshals data to JSON, sets Content-Type, writes response. Every handler uses this.
	- `readJSON(w, r, dst)` — decodes request body into a struct with proper error handling (unknown fields, bad syntax, too large, etc.)
	- `readIDParam(r)` — extracts `:id` from URL path and converts to int64
6. **`cmd/server/errors.go`**
	- `logError(r, err)` — logs error with request method + URL for context
	- `errorResponse(w, r, status, message)` — sends `{ "error": { "message": "..." } }`
	- `serverErrorResponse(w, r, err)` — 500 + logs the actual error
	- `notFoundResponse(w, r)` — 404
	- `badRequestResponse(w, r, err)` — 400
	- `failedValidationResponse(w, r, errors)` — 422 with field-level errors
7. **`cmd/server/healthcheck.go`**
	- `func (app *application) healthcheckHandler(w, r)` — returns status, environment, version (from `internal/vcs/`)


**HOW to verify**:

```bash

make run

# In another terminal:

curl localhost:4000/v1/healthcheck

# Should return: {"status":"available","system_info":{"environment":"development","version":"..."}}

```


**WHAT comes next**: The server runs and responds. Now connect it to the database so you can start storing data.

---


### Step 3: Database Connection & Migrations Setup



**WHAT you're doing**: Connecting the server to Postgres, setting up the migration tool, and running your first migration (the users table).


**WHY**: Every feature from here on needs a database. Setting up migrations now means your schema changes are version-controlled, repeatable, and reversible. You'll never manually run `CREATE TABLE` in psql again.



**Tasks**:

1. **Install `golang-migrate`** CLI tool (for running migrations from Makefile)


```bash

go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

```


2. **Add DB connection to `main.go`**
	- Open a `pgxpool.Pool` with the DSN from config
	- Set `MaxConns`, `MinConns`, `MaxConnIdleTime` from config
	- Ping the database to verify the connection works
	- Pass the pool into `data.NewModels(pool)` (created in next step)
	- `defer pool.Close()` for cleanup on shutdown
3. **Create `internal/data/models.go`**
	- Define the `Models` struct (empty for now — will hold `UserModel`, `CollectionModel`, etc.)
	- `func NewModels(pool *pgxpool.Pool) Models` — constructor that initializes all model types with the pool
	- Store `Models` on the `application` struct
4. **Add `models` field to `application` struct in `server.go`**

5. **Create migration 001: `migrations/000001_create_users_table.up.sql`**

```sql

CREATE EXTENSION IF NOT EXISTS citext;



CREATE TABLE IF NOT EXISTS users (
	id BIGSERIAL PRIMARY KEY,
	email CITEXT UNIQUE NOT NULL,
	password_hash BYTEA NOT NULL,
	first_name TEXT NOT NULL DEFAULT '',
	last_name TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	version INTEGER NOT NULL DEFAULT 1
);

```



And `000001_create_users_table.down.sql`:

```sql

DROP TABLE IF EXISTS users;

```


6. **Add `migrate-up` and `migrate-down` targets to Makefile**


```makefile
migrate-up:
	migrate -path ./migrations -database $(SUMMERIZER_DB_DSN) up
migrate-down:
	migrate -path ./migrations -database $(SUMMERIZER_DB_DSN) down 1

```



7. **Run the migration**: `make migrate-up`


**HOW to verify**:

```bash

make migrate-up

# Connect to DB and check:

psql $SUMMERIZER_DB_DSN -c "\dt"

# Should list: users

# Server should start without DB connection errors:

make run

```



**WHAT comes next**: The database has a users table. Now build the registration endpoint to put users in it.



---



### Step 4: User Registration

  **WHAT you're doing**: Building the user data model, password hashing, validation, and the `POST /v1/users` registration endpoint.

**WHY**: Users are the root entity — everything (collections, sources, chunks) belongs to a user. You need users before you can build anything that requires ownership or authentication.



**Tasks**:
1. **Create `internal/validator/validator.go`**
	- A `Validator` struct with a map of field → error message
	- Helper methods: `Check(ok bool, key, message)`, `Valid() bool`
	- Standalone functions: `NotBlank(value)`, `MinChars(value, n)`, `MaxChars(value, n)`, `Matches(value, regex)`, `In(value, safelist)`, `Unique(values)`
	- The Edwards pattern: create a validator, call `Check()` for each rule, then `if !v.Valid() { failedValidationResponse(...) }`

> **WHY a validator package**: Validation logic is reused across every endpoint. Centralizing it avoids duplicating "is email valid?" checks everywhere. It also produces consistent error messages.


2. **Create `internal/data/users.go`**
	- `User` struct with all fields from the schema
	- A `password` helper type that wraps bcrypt: `Set(plaintext)` hashes it, `Matches(plaintext)` compares. The plain text password is NEVER stored anywhere.
	- `UserModel` struct with `*pgxpool.Pool`
	- Methods:
		- `Insert(user *User) error` — INSERT with RETURNING id, created_at, version
		- `GetByEmail(email string) (*User, error)` — SELECT by email
		- `Get(id int64) (*User, error)` — SELECT by ID
		- `Update(user *User) error` — UPDATE with `WHERE version = $n` for optimistic locking


> **WHY optimistic locking**: If two requests try to update the same user simultaneously, one will fail with "edit conflict" instead of silently overwriting the other's changes. The `version` column increments on every update, and the WHERE clause ensures you're updating the version you read.


3. **Add `UserModel` to the `Models` struct** in `models.go`

4. **Create `cmd/server/user.go`**
	- `registerUserHandler` — decode JSON body (`email`, `password`, `first_name`, `last_name`), validate all fields, hash password, insert user, return 201 with user JSON (NEVER return the password hash)
5. **Register the route** in `router.go`:

```go
router.HandlerFunc(http.MethodPost, "/v1/users", app.registerUserHandler)
```


**HOW to verify**:

```bash

curl -X POST localhost:4000/v1/users \

-H "Content-Type: application/json" \

-d '{"email":"test@example.com","password":"pa55word123","first_name":"Test","last_name":"User"}'

# Should return 201 with user JSON (no password_hash in response)



# Try duplicate email:

curl -X POST localhost:4000/v1/users \

-H "Content-Type: application/json" \

-d '{"email":"test@example.com","password":"pa55word123","first_name":"Test","last_name":"User"}'

# Should return 422 with "email: a user with this email address already exists"

```


**WHAT comes next**: Users can register. Now they need to log in and get a token to access protected endpoints.

---

### Step 5: Authentication (JWT)

**WHAT you're doing**: Building the login endpoint that returns a JWT, and the auth middleware that validates JWTs on every request.


**WHY**: Without auth, anyone can access any collection. JWT is stateless — the server doesn't need to store sessions. The token contains the user ID, and the middleware extracts it on every request.


**Tasks**:

1. **Create `cmd/server/auth.go`**
	- `loginHandler` — accept `email` + `password`, verify credentials via `app.models.Users.GetByEmail()` + `password.Matches()`, generate JWT with custom claims (user ID, email, expiry), return token
	- JWT creation: use `golang-jwt/jwt/v5` to create a token signed with HS256 and the secret from config. Set expiry to `config.jwt.expiry` (e.g., 24 hours)
	- `refreshHandler` — accept a valid token, issue a new one with fresh expiry (optional, can defer)

> **WHY HS256**: Symmetric signing — one secret key signs and verifies. Simpler than RSA for a single-service architecture. If you later add multiple services that need to verify tokens, switch to RS256 (asymmetric).

2. **Create `cmd/server/context.go`**
	- Define a custom context key type (avoids key collisions)
	- `contextSetUser(r, *User) *http.Request` — stores user in request context
	- `contextGetUser(r) *User` — retrieves user from context
	- Define `AnonymousUser` — a sentinel `User` with `ID: 0`. If no auth token is provided, set this as the context user.
3. **Add `authenticate` middleware to `cmd/server/middleware.go`**
	- Extract `Authorization` header
	- If missing → set `AnonymousUser` in context, continue (some endpoints like healthcheck don't need auth)
	- If present → parse `Bearer <token>`, validate JWT signature + expiry, extract claims, load full user from DB via `app.models.Users.Get(claims.UserID)`, set in context
	- On invalid token → return 401
4. **Add `requireAuthenticatedUser` middleware**
	- Checks `contextGetUser(r)` is not anonymous
	- If anonymous → 401 "you must be authenticated to access this resource"
	- This wraps individual handler functions (since httprouter doesn't have route groups)
5. **Wire up in `router.go`**:

```go

router.HandlerFunc(http.MethodPost, "/v1/auth/login", app.loginHandler)
// authenticate middleware goes in the global chain (in routes() return)
```

6. **Add `authenticate` to the middleware chain** in `routes()`:

```go

return app.recoverPanic(app.authenticate(router))

```


**HOW to verify**:
```bash

# Login:

curl -X POST localhost:4000/v1/auth/login \

-H "Content-Type: application/json" \

-d '{"email":"test@example.com","password":"pa55word123"}'

# Should return: {"access_token":"eyJhbG...","expires_in":86400}



# Use token on a protected endpoint (healthcheck still works without token):

curl localhost:4000/v1/healthcheck

# Should return 200 (no auth needed)



# Future protected endpoints will need:

# -H "Authorization: Bearer eyJhbG..."

```

**WHAT comes next**: Users can register and log in. Now build the core domain objects they'll interact with — collections and sources.


---

### Step 6: Collection CRUD

**WHAT you're doing**: Building the Collection data model and all CRUD endpoints — create, read, update, delete, list with pagination.

**WHY**: Collections are the container for everything. A user groups related sources into a collection ("Machine Learning Basics") so they can query that specific knowledge base later. This is also your first fully protected CRUD — good practice for ownership enforcement patterns.



**Tasks**:
1. **Run migration 002**: `create_collections_table` (schema in plan v2)
2. **Create `internal/data/collections.go`**
	- `Collection` struct matching the schema
	- `CollectionModel` struct with `*pgxpool.Pool`
	- Methods:
		- `Insert(collection *Collection) error` — INSERT RETURNING id, created_at, version
		- `Get(id int64) (*Collection, error)` — SELECT by ID. Return `ErrRecordNotFound` if no rows.
		- `Update(collection *Collection) error` — UPDATE with `WHERE id = $1 AND version = $2`. If 0 rows affected → return `ErrEditConflict`.
		- `Delete(id int64) error` — DELETE by ID
		- `ListByUser(userID int64, filters Filters) ([]*Collection, Metadata, error)` — SELECT with pagination, returns total count for metadata

> **WHY `ErrRecordNotFound` and `ErrEditConflict`**: Custom sentinel errors let handlers translate DB outcomes into HTTP status codes cleanly. `ErrRecordNotFound` → 404, `ErrEditConflict` → 409. Without these, you'd be checking `sql.ErrNoRows` in handlers, which leaks DB implementation details.


3. **Create `internal/data/filters.go`**
	- `Filters` struct: `Page int`, `PageSize int`
	- `func (f Filters) limit() int` → returns `PageSize`
	- `func (f Filters) offset() int` → returns `(Page - 1) * PageSize`
	- `Metadata` struct: `CurrentPage`, `PageSize`, `FirstPage`, `LastPage`, `TotalRecords`
	- `func calculateMetadata(totalRecords, page, pageSize int) Metadata`
	- Validation: page ≥ 1, page_size between 1-100
4. **Add `CollectionModel` to `Models` struct** and add `ErrRecordNotFound`, `ErrEditConflict` as package-level vars in `models.go`

5. **Create `cmd/server/collection.go`** with handlers:
	- `createCollectionHandler` — validate title (not blank, ≤200 chars), insert, return 201
	- `showCollectionHandler` — get by ID, check ownership, return 200
	- `updateCollectionHandler` — partial update via PATCH (only update provided fields), check ownership + version, return 200
	- `deleteCollectionHandler` — check ownership, delete, return 200 with confirmation message
	- `listCollectionsHandler` — parse `page` + `page_size` from query string, list by user, return with metadata
6. **Add ownership check helper**
	- In each handler, after fetching the collection, verify `collection.UserID == contextGetUser(r).ID`
	- If not → `notFoundResponse` (return 404, not 403)
7. **Register routes** in `router.go` — all wrapped with `app.requireAuthenticatedUser()`


**HOW to verify**:

```bash

TOKEN="Bearer eyJhbG..." # from login

# Create:

curl -X POST localhost:4000/v1/collections \

-H "Authorization: $TOKEN" \

-H "Content-Type: application/json" \

-d '{"title":"Go Concurrency","description":"Resources about Go concurrency patterns"}'

# 201 with collection JSON

# List:

curl localhost:4000/v1/collections -H "Authorization: $TOKEN"

# 200 with paginated list



# Update:

curl -X PATCH localhost:4000/v1/collections/1 \

-H "Authorization: $TOKEN" \

-H "Content-Type: application/json" \

-d '{"description":"Updated description"}'

# 200



# Delete:

curl -X DELETE localhost:4000/v1/collections/1 -H "Authorization: $TOKEN"

# 200



# Access another user's collection:

# Register user2, login, try GET /v1/collections/1 → 404

```



**WHAT comes next**: Collections exist. Now add the ability to put sources (URLs) into them.



---

### Step 7: Source CRUD


**WHAT you're doing**: Building the Source data model and endpoints. When a source is created, it's stored with `status=pending` — actual processing comes later.


**WHY**: This decouples "accepting work" from "doing work." The HTTP handler responds instantly (202 Accepted) while the background worker (Step 8) picks up the job later. This is a fundamental a`sync pattern in backend systems.



**Tasks**:
1. **Run migration 003**: `create_sources_table` (schema in plan v2)
2. **Create `internal/data/sources.go`**
	- `Source` struct matching the schema
	- `SourceModel` with methods:
	- `Insert(source *Source) error`
	- `Get(id int64) (*Source, error)`
	- `Delete(id int64) error`
	- `ListByCollection(collectionID int64, filters Filters) ([]*Source, Metadata, error)`
	- `ClaimPending(limit int) ([]*Source, error)` — the `SELECT ... FOR UPDATE SKIP LOCKED` query. This is used by the worker pool in Step 8. Implement it now so the data layer is complete.
	- `UpdateStatus(id int64, status, currentStep, stepError string, retryCount int, nextRetryAt *time.Time) error` — updates processing state. Used by the pipeline in Step 9.


> **WHY `ClaimPending` lives in the data layer**: It's a database query. All DB queries belong in `internal/data/`. The worker pool calls this method — it doesn't write SQL directly.


3. **Add `SourceModel` to `Models` struct**

4. **Create URL type detection helper** (in `cmd/server/helpers.go` or `source.go`)
	- Given a URL string, detect source type:
	- Contains `youtube.com/watch`, `youtu.be/`, `youtube.com/embed` → `youtube`
	- Path ends with `.pdf` → `pdf`
	- Everything else → `web`
5. **Create `cmd/server/source.go`** with handlers:
	- `addSourceHandler` — validate URL, detect type, verify collection ownership, insert with `status=pending`, return **202 Accepted**
	- `listSourcesHandler` — list by collection with pagination, supports optional `?status=completed` filter
	- `showSourceHandler` — show full details (including `current_step`, `step_error`, `retry_count`)
	- `deleteSourceHandler` — verify ownership (via collection), delete source
6. **Register routes** in `router.go`

**HOW to verify**:


```bash

# Add a source:

curl -X POST localhost:4000/v1/collections/1/sources \

-H "Authorization: $TOKEN" \

-H "Content-Type: application/json" \

-d '{"url":"https://go.dev/doc/effective_go"}'

# 202 Accepted, source has status=pending



# Check status:

curl localhost:4000/v1/sources/1 -H "Authorization: $TOKEN"

# status: "pending", current_step: null



# Add YouTube source:

curl -X POST localhost:4000/v1/collections/1/sources \

-H "Authorization: $TOKEN" \

-H "Content-Type: application/json" \

-d '{"url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ"}'

# source_type should be "youtube"



# Try duplicate URL in same collection:

# Should return 422 or 409 — unique constraint violation

```


**WHAT comes next**: Sources are in the DB with `status=pending`. Nobody is processing them yet. Time to build the background worker that picks them up.


---

### Step 8: Background Worker Pool

**WHAT you're doing**: Building a pool of goroutines that continuously polls the database for pending sources and dispatches them to the ingestion pipeline.


**WHY**: You can't process sources inside the HTTP handler — that would block the response for minutes (fetching, chunking, embedding). Instead, the handler inserts a row and returns immediately. Workers running in background goroutines pick up pending work and process it asynchronously. This is the same pattern used by Sidekiq (Ruby), Celery (Python), and every production job queue.

  **What you'll learn**: Go channels, goroutine lifecycle management, `context.Context` for cancellation, `sync.WaitGroup` for coordinating shutdown, and the `SELECT ... FOR UPDATE SKIP LOCKED` pattern for distributed job claiming.


**Tasks**:

1. **Create `internal/worker/pool.go`**
	- `Pool` struct: holds worker count, poll interval, data models ref, logger, cancel func, wait group
		- `func NewPool(count int, interval time.Duration, models data.Models, logger *slog.Logger) *Pool`
		- `func (p *Pool) Start(ctx context.Context)` — launches N goroutines, each running `p.run(ctx)`
		- `func (p *Pool) Shutdown()` — cancels context, waits for all workers to finish current job via `WaitGroup.Wait()`
2. **Implement `run(ctx context.Context)` — the worker loop**:

```go
defer wg.Done()
defer recover()  // don't let a panic kill the process

for {
    select {
    case <-ctx.Done():
        return  // shutdown signal received
    case <-time.After(pollInterval):
        sources := models.Sources.ClaimPending(1)  // claim 1 job at a time
        for _, source := range sources {
            p.process(ctx, source)
        }
    }
}
```


> **WHY `defer recover()`**: If a panic occurs inside the ingestion pipeline (bad input, nil pointer, etc.), this prevents the entire application from crashing. The worker logs the panic and continues to the next iteration. Without this, one bad PDF could take down your whole server.


3. **Implement `process(ctx context.Context, source *data.Source)`** — for now, just a stub:
	- Log "processing source ID=X type=Y"
	- Update status to `ingesting`
	- Sleep 5 seconds (simulate work)
	- Update status to `completed`
	- This stub will be replaced with the real pipeline in Step 9
4. **Wire up in `main.go`**:
	- Create `worker.NewPool(...)` after opening the DB pool
	- Call `pool.Start(ctx)` in a goroutine
	- On shutdown, call `pool.Shutdown()` before closing the DB pool
5. **Add to `application` struct** so handlers could theoretically interact with it (e.g., future "retry source" endpoint)



**HOW to verify**:


```bash

# Start server:

make run

# Add a source via curl (from Step 7)

# Watch server logs — within 5 seconds you should see:

# "processing source ID=1 type=web"

# Check the source status:

curl localhost:4000/v1/sources/1 -H "Authorization: $TOKEN"

# status should be "completed" (from stub)



# Add 5 sources rapidly, verify workers process them concurrently (if worker count > 1)

```



**WHAT comes next**: The worker picks up jobs and marks them complete (with fake processing). Now replace the stub with real content fetching.



---



### Step 9: Web Page Ingestion Pipeline


**WHAT you're doing**: Building the real ingestion pipeline — starting with web pages. Fetch HTML → extract readable text → split into chunks. Embedding comes in Step 12.


**WHY web first**: Web pages are the simplest source type. No authentication, no binary formats, no fragile APIs. Getting one source type working end-to-end validates the entire pipeline architecture before you add complexity with YouTube and PDFs.


**Tasks**:

1. **Create the pipeline orchestrator: `internal/ingestion/pipeline.go`**
	- `Pipeline` struct — holds references to fetchers, chunker, embedder (nil for now), models
	- `func (p *Pipeline) Process(ctx context.Context, source *data.Source) error`
	- Check `source.CurrentStep` — if resuming, skip completed steps
	- Run steps in order: fetch → clean → chunk → (embed — Step 12) → store
	- After each step, update `current_step` in DB (checkpoint)
	- On error: update status to `failed`, record `step_error`, calculate next retry time
	- On success: update status to `completed`


> **WHY checkpoint after each step**: If the server crashes between chunking and embedding, the checkpoint tells the worker to resume from embedding — not re-fetch the entire page. This is called **idempotent step execution**.


2. **Create the web fetcher: `internal/ingestion/fetcher/web.go`**
	- `WebFetcher` struct implementing `ContentFetcher` interface
	- `Fetch(ctx, url)`:
		- Create `http.Client` with 30s timeout
		- GET the URL
		- Check status code is 200
		- Check `Content-Type` contains `text/html`
		- Read body with `io.LimitReader(resp.Body, 5*1024*1024)` — 5MB max
		- Return `RawContent{Body: htmlBytes, ContentType: "text/html", Title: ""}`


3. **Add text extraction (clean step)** — can live in the pipeline or a separate cleaner:
	- Use `go-shiori/go-readability` to extract article content from HTML
	- It returns: title, text content, excerpt
	- Fallback: if readability returns empty, use `goquery` to extract all `<p>` text
4. **Create the chunker: `internal/ingestion/chunker/chunker.go`**
	- `TextChunker` implementing `Chunker` interface
	- `Chunk(content, opts)`:
	- `opts` specifies: `MaxTokens` (500), `Overlap` (50)
	- Split text by paragraphs first (double newline), then by sentences if a paragraph exceeds MaxTokens
	- Estimate token count: `len(strings.Fields(text))` × 1.33 (rough approximation)
	- Each `ChunkResult`: `Content`, `Index`, `TokenCount`, `Metadata`

> **WHY split by paragraphs first**: Splitting at arbitrary character positions breaks sentences mid-word. Splitting by paragraph preserves semantic boundaries. Only split smaller when a paragraph exceeds the chunk size limit.

5. **Store chunks (without embeddings for now)**
	- Create `internal/data/chunks.go`:
	- `Chunk` struct matching the schema
	- `ChunkModel` with `BulkInsert(chunks []*Chunk) error` — uses `pgx.CopyFrom` for fast bulk insert
	- `DeleteBySource(sourceID int64) error` — needed for re-processing
	- `SearchByEmbedding(...)` — stub for now, implemented in Step 13
	- Run migration 004: `create_chunks_table` (without the HNSW index for now — add it in Step 12 when embeddings exist)
6. **Replace the stub in worker pool** — call `pipeline.Process(ctx, source)` instead of sleeping


**HOW to verify**:

```bash

# Add a real web source:

curl -X POST localhost:4000/v1/collections/1/sources \

-H "Authorization: $TOKEN" \

-H "Content-Type: application/json" \

-d '{"url":"https://go.dev/doc/effective_go"}'



# Wait for worker to process it (watch logs)

# Check source status:

curl localhost:4000/v1/sources/2 -H "Authorization: $TOKEN"

# status: "completed"



# Check chunks exist:

psql $DB -c "SELECT count(*), avg(token_count) FROM chunks WHERE source_id = 2;"

# Should show chunk count > 0, avg token count around 300-500

# Embedding column will be NULL — that's expected until Step 12

```


**WHAT comes next**: Web ingestion works (minus embeddings). Add YouTube and PDF support.

---

### Step 10: YouTube Transcript Ingestion

**WHAT you're doing**: Adding a YouTube fetcher that extracts transcripts from video URLs.

  **WHY**: YouTube videos are a key source type for learners. The transcript is essentially a text document you can chunk and search like any other content.

**Tasks**:
1. **Create `internal/ingestion/fetcher/youtube.go`**
	- `YouTubeFetcher` implementing `ContentFetcher`
	- Extract video ID from URL using regex (handle `youtube.com/watch?v=`, `youtu.be/`, `youtube.com/embed/`)
	- Use `kkdai/youtube/v2`:

```go

client := youtube.Client{}

video, _ := client.GetVideoContext(ctx, videoID)

transcript, err := client.GetTranscript(video, "en")

```

   - Concatenate transcript segments into full text, preserving timestamps in metadata
   - Handle errors gracefully:
   - `ErrTranscriptDisabled` → return a clear error, do NOT retry
   - Network errors → return error, pipeline will retry


2. **Update pipeline to route by source type**
	- In `pipeline.Process()`, select the fetcher based on `source.SourceType`:

```go

switch source.SourceType {
	case "web":
		 fetcher = p.webFetcher
	case "youtube":
		 fetcher = p.youtubeFetcher
	case "pdf":
		 fetcher = p.pdfFetcher
}
```


3. **Add YouTube-specific chunking**: transcript segments group naturally by time. Split by ~30-second windows first, then by token count. Store `start_time_ms` and `end_time_ms` in chunk metadata.

**HOW to verify**:

```bash

# Add a YouTube source (use a video you know has captions):

curl -X POST localhost:4000/v1/collections/1/sources \

-H "Authorization: $TOKEN" \

-H "Content-Type: application/json" \

-d '{"url":"https://www.youtube.com/watch?v=<VIDEO_WITH_CAPTIONS>"}'



# Check processing logs and final status

# Chunks should have metadata with start_time_ms/end_time_ms

```


> **WARNING**: YouTube transcript extraction is fragile. The library may break when YouTube updates its internal APIs. Document this as "best-effort" and handle failures gracefully. Don't spend days debugging YouTube API changes — that's expected maintenance.


**WHAT comes next**: Add PDF support to complete the three source types.

---

### Step 11: PDF Ingestion


**WHAT you're doing**: Adding a PDF fetcher that downloads PDFs and extracts text page-by-page.


**WHY**: PDFs are the third source type. Unlike web and YouTube, PDFs are binary files that need to be downloaded to disk first, then parsed with a specialized library.


**Tasks**:
1. **Create `internal/ingestion/fetcher/pdf.go`**
	- `PDFFetcher` implementing `ContentFetcher`
	- Download PDF to temp file: `os.CreateTemp("", "summerizer-*.pdf")`
	- Validate size ≤ 20MB using `io.LimitReader`
	- Validate Content-Type is `application/pdf`
	- Extract text using `pdfcpu`:

```go

// pdfcpu has various extraction methods

// Extract text content per page

```

   - `defer os.Remove(tmpFile.Name())` — always clean up the temp file
   - Return text content with page numbers in metadata


2. **Add PDF-specific chunking**: Split by page boundaries first (each page is a natural unit), then by token count within large pages. Store `page_number` in chunk metadata.

3. **Update pipeline** to handle `source.SourceType == "pdf"`


**HOW to verify**:

```bash

# Add a PDF source (use a publicly available PDF):

curl -X POST localhost:4000/v1/collections/1/sources \

-H "Authorization: $TOKEN" \

-H "Content-Type: application/json" \

-d '{"url":"https://example.com/some-document.pdf"}'



# Verify chunks have page_number in metadata

```


**WHAT comes next**: All three source types produce chunks (text + metadata). Now add the critical piece — embeddings — that makes those chunks searchable by meaning.

---

### Step 12: Embedding Integration


**WHAT you're doing**: Implementing the `Embedder` interface to generate vector embeddings from text, and integrating it into the ingestion pipeline.


**WHY**: Embeddings are what make semantic search possible. A chunk of text like "Go uses goroutines for concurrency" gets converted into a vector of 768 numbers. When a user asks "how does Go handle parallelism?", their question also becomes a vector — and pgvector finds the chunks whose vectors are closest (most semantically similar).


**This is the core of RAG. Without embeddings, you'd be limited to keyword search.**


**Tasks**:
1. **Define `RawContent`, `ChunkOptions`, `ChunkResult` types** in `internal/data/` if not already done:

```go
type RawContent struct {
	Body []byte
	ContentType string
	Title string
	Metadata map[string]any
}
```


2. **Implement the `Embedder` interface** — create `internal/search/embedder.go`:
	- Use `sashabaranov/go-openai` as the client (works with OpenAI, Ollama, HuggingFace-compatible APIs)
	- Constructor takes: base URL, API key, model name, dimensions
	- `Embed(ctx, texts)` — calls the embeddings API, returns `[][]float32`
	- `Dimensions()` — returns the configured vector dimension (e.g., 768)
	- Handle rate limits: if the API returns 429, implement retry with backoff


> **WHY `sashabaranov/go-openai` for everything**: The OpenAI embeddings API format (`POST /v1/embeddings`) has become the universal standard. Ollama, vLLM, LiteLLM, and many HuggingFace inference servers expose this same endpoint. One client, many backends.



3. **Add embedder config** to `main.go` config struct:

```go

embedder struct {
	baseURL string // e.g., "http://localhost:11434/v1" for Ollama
	apiKey string
	model string // e.g., "nomic-embed-text"
	dimensions int // e.g., 768
}

```


4. **Integrate into the pipeline** — add the embed step:
	- After chunking, collect all chunk texts
	- Call `embedder.Embed(ctx, chunkTexts)` — batch all at once
	- Assign each embedding to its corresponding chunk
	- Store chunks with embeddings via `BulkInsert`
5. **Add HNSW index migration** (004 or update the existing chunks migration):

```sql

CREATE INDEX idx_chunks_embedding_hnsw ON chunks

USING hnsw (embedding vector_cosine_ops);

```


> **WHY add the index now**: Without the HNSW index, vector search would do a full table scan — O(n) for every query. The index enables approximate nearest neighbor search in O(log n).

6. **Register pgvector types** in `main.go` when creating the DB pool:

```go

// After creating the pool, register pgvector types

// pgvector-go provides RegisterTypes() for pgx

```


**HOW to verify**:

```bash

# Ensure your embedding provider is running (e.g., Ollama with a model pulled)

# ollama pull nomic-embed-text



# Re-add a source (or add a new one) — this time the pipeline should generate embeddings

# Check:

psql $DB -c "SELECT id, chunk_index, token_count, embedding IS NOT NULL as has_embedding FROM chunks WHERE source_id = X;"

# has_embedding should be true for all chunks



# Check embedding dimensions:

psql $DB -c "SELECT vector_dims(embedding) FROM chunks LIMIT 1;"

# Should match your model's dimensions (e.g., 768)

```



**WHAT comes next**: Chunks have embeddings. Now build the search endpoint that uses them.

---

### Step 13: Vector Search Endpoint


**WHAT you're doing**: Building `POST /v1/collections/:id/search` — embed a query, find the most similar chunks, return them ranked by relevance.


**WHY**: This is the "retrieval" in RAG. Before involving an LLM (which costs money and adds latency), you need to verify that vector search alone returns relevant results. If the search returns garbage, no LLM can fix that.


**Tasks**:

1. **Implement `SearchByEmbedding` in `internal/data/chunks.go`**:


```go

func (m ChunkModel) SearchByEmbedding(
	ctx context.Context,
	collectionID int64,
	embedding pgvector.Vector,
	topK int
) ([]*ChunkSearchResult, error){}

```


- Runs the cosine similarity query:

```sql

SELECT c.id, c.content, c.metadata, c.embedding <=> $1 AS distance,
	s.url AS source_url, s.title AS source_title
	FROM chunks c
	JOIN sources s ON c.source_id = s.id
	WHERE s.collection_id = $2 AND s.status = 'completed'
	ORDER BY c.embedding <=> $1
	LIMIT $3

```

- Returns `ChunkSearchResult` structs with content, distance, source info, metadata



2. **Create `internal/search/search.go`** (or add to existing):
	- `SearchService` struct — holds `Embedder` and `data.Models`
	- `func (s *SearchService) Search(ctx, collectionID, query, topK) ([]SearchResult, error)`:
3. Embed the query text: `s.embedder.Embed(ctx, []string{query})`

4. Call `s.models.Chunks.SearchByEmbedding(ctx, collectionID, embedding, topK)`

5. Return results

6. **Create `cmd/server/search.go`** with `searchCollectionHandler`:
	- `POST /v1/collections/:id/search`
	- Body: `{ "query": "...", "top_k": 5 }` (top_k defaults to 5, max 20)
	- Verify collection ownership
	- Call search service
	- Return results as JSON
7. **Register route** in `router.go`


**HOW to verify**:


```bash

# Make sure you have a collection with processed sources (from Steps 9-12)


# Search:

curl -X POST localhost:4000/v1/collections/1/search \

-H "Authorization: $TOKEN" \

-H "Content-Type: application/json" \

-d '{"query":"how does Go handle concurrency?","top_k":5}'



# Should return chunks ranked by relevance (lowest distance = most relevant):

# { "results": [{ "content": "...", "distance": 0.23, "source_url": "...", ... }] }



# Verify HNSW index is used:

psql $DB -c "EXPLAIN ANALYZE SELECT ... <your search query>"

# Should show "Index Scan using idx_chunks_embedding_hnsw"

```



**WHAT comes next**: Search works. Now add the LLM layer that takes these results and generates a coherent answer.

---

### Step 14: LLM Answer Generation



**WHAT you're doing**: Building `POST /v1/collections/:id/ask` — the full RAG flow. Embed the question, retrieve relevant chunks, construct a prompt, call an LLM, return the answer.


**WHY**: This is the final user-facing feature. Everything built so far feeds into this one endpoint. The user asks a question in natural language and gets an answer that's grounded in their own collected sources.



**Tasks**:

1. **Implement `LLMClient` interface** in `internal/search/llm.go`:
	- Use `sashabaranov/go-openai` client
	- `GenerateAnswer(ctx, systemPrompt, userPrompt)`:
	- Create a chat completion request with system message + user message
	- Call the API
	- Return the response text
2. **Add LLM config** to `main.go`:

```go

llm struct {
	baseURL string // "https://api.openai.com/v1" or "http://localhost:11434/v1"
	apiKey string
	model string // "gpt-4o-mini" or "llama3"
}

```


3. **Build the RAG flow** in `internal/search/search.go`:

	- `func (s *SearchService) Ask(ctx, collectionID, question, topK) (*AnswerResult, error)`:
		1. Embed the question (reuse `Embed`)
		2. Retrieve top-k chunks via `SearchByEmbedding`
		3. Build the prompt:
			- System message: "You are a helpful study assistant. Answer based ONLY on the provided context. If the context doesn't have enough info, say so. Cite sources by title."
		- Context block: numbered chunks with source titles
		- User question
4. Call `LLMClient.GenerateAnswer(ctx, systemPrompt, fullPrompt)`

5. Return `AnswerResult{Answer, Sources}` — extract which sources were cited



> **WHY "ONLY on the provided context"**: This instruction grounds the LLM. Without it, the model might answer from its training data, which defeats the purpose of having curated sources. The user wants answers from THEIR materials, not from the internet.



4. **Create `askCollectionHandler`** in `cmd/server/search.go`:

- `POST /v1/collections/:id/ask`
	- Body: `{ "question": "..." }`
	- Verify collection ownership
	- Call `searchService.Ask(...)`
	- Return: `{ "answer": "...", "sources": [{ "title": "...", "url": "..." }] }`

4. **Register route**


**HOW to verify**:

```bash

# Ensure LLM is available (Ollama running, or OpenAI key configured)



curl -X POST localhost:4000/v1/collections/1/ask \

-H "Authorization: $TOKEN" \

-H "Content-Type: application/json" \

-d '{"question":"How does Go handle concurrency?"}'



# Should return a coherent answer that references your ingested sources:

# { "answer": "According to the sources...", "sources": [...] }

```


**WHAT comes next**: The core product works end-to-end. Now polish it for production quality.



---

### Step 15: Polish & Hardening



**WHAT you're doing**: Adding production-quality features that separate "it works" from "it's well-engineered." These are the things interviewers and code reviewers look for.


**WHY**: A working demo proves you can code. Production polish proves you can engineer. Rate limiting, graceful shutdown, structured logging, and tests demonstrate that you think about real-world concerns — not just the happy path.


**Tasks** (do these in any order — they're independent):

1. **Rate Limiting Middleware** (`cmd/server/middleware.go`)
	- Create per-IP rate limiters using `golang.org/x/time/rate` and `sync.Map`
	- Return 429 Too Many Requests when exceeded
	- Background goroutine cleans up stale entries every 3 minutes
	- Configurable via `config.limiter.rps` and `config.limiter.burst`
2. **Request ID Middleware**
	- Generate a random hex string (16 chars) per request
	- Add to response as `X-Request-ID` header
	- Inject into logger: every log line includes the request ID
	- When creating a source, store the request ID on the source row for traceability
3. **CORS Middleware**
	- Read allowed origins from config (`config.cors.trustedOrigins []string`)
	- Handle preflight OPTIONS requests
	- Set appropriate `Access-Control-Allow-*` headers
4. **Structured Logging**
	- Switch from `slog.TextHandler` to `slog.JSONHandler` in production
	- Log every request: method, path, status, duration, request_id
	- Log worker events: job claimed, step started, step completed/failed, duration
5. **Content Limits**
	- Check source count per collection before inserting (max 50)
	- Check collection count per user before inserting (max 20)
	- Already have size limits in fetchers (5MB web, 20MB PDF)
6. **User Activation** (optional, defer further if focused on core)
	- Add `activated` bool to users, `internal/mailer/` package
	- Registration sends activation email, `PUT /v1/users/activated` activates
	- `requireActivatedUser` middleware on protected routes
7. **Graceful Shutdown** (should already be partially implemented)
	- On SIGINT/SIGTERM: stop HTTP listener → drain in-flight requests (30s timeout) → shut down worker pool (workers finish current job) → close DB pool → exit 0
	- Verify: send a request, immediately `kill` the process — the request should complete before exit
8. **Tests**
	- **Unit tests**: validator, chunker, URL type detection, JWT creation/parsing
	- **Integration tests**: data model methods against a real Postgres (use `testcontainers-go` or a test DB)
	- **Handler tests**: `httptest.NewRecorder()` + mock models

- Focus on: auth flow, ownership enforcement, pipeline step transitions, search query correctness


9. **README.md**
	- Architecture diagram (Mermaid or ASCII)
	- How to run: Docker Compose → migrate → run
	- API reference with curl examples
	- What is RAG — brief explanation
	- Design decisions — why this stack, why this architecture
	- What would change at scale — message queues, read replicas, embedding cache, CDN for PDFs


**HOW to verify**: Run `make audit` — all linting, vetting, and tests pass. Load test with `hey -n 200 -c 20 http://localhost:4000/v1/healthcheck` — rate limiter kicks in.



---

## 4. API Reference

### Public Endpoints (no auth)

| Method | Path              | Description              |
| ------ | ----------------- | ------------------------ |
| `GET`  | `/v1/healthcheck` | Service status + version |

### Auth Endpoints

| Method | Path               | Request Body                                 | Response                       | Status |
| ------ | ------------------ | -------------------------------------------- | ------------------------------ | ------ |
| `POST` | `/v1/users`        | `{ email, password, first_name, last_name }` | User object                    | 201    |
| `POST` | `/v1/auth/login`   | `{ email, password }`                        | `{ access_token, expires_in }` | 200    |
| `POST` | `/v1/auth/refresh` | — (uses existing token)                      | `{ access_token, expires_in }` | 200    |

### Collection Endpoints (auth required)

| Method   | Path                  | Request Body               | Response                      | Status |
| -------- | --------------------- | -------------------------- | ----------------------------- | ------ |
| `POST`   | `/v1/collections`     | `{ title, description }`   | Collection object             | 201    |
| `GET`    | `/v1/collections`     | —                          | `{ metadata, collections[] }` | 200    |
| `GET`    | `/v1/collections/:id` | —                          | Collection object             | 200    |
| `PATCH`  | `/v1/collections/:id` | `{ title?, description? }` | Collection object             | 200    |
| `DELETE` | `/v1/collections/:id` | —                          | `{ message }`                 | 200    |

### Source Endpoints (auth required)

|Method|Path|Request Body|Response|Status|
|---|---|---|---|---|
|`POST`|`/v1/collections/:id/sources`|`{ url }`|Source object|**202**|
|`GET`|`/v1/collections/:id/sources`|—|`{ metadata, sources[] }`|200|
|`GET`|`/v1/sources/:id`|—|Source object with processing status|200|
|`DELETE`|`/v1/sources/:id`|—|`{ message }`|200|

### Search & Ask Endpoints (auth required)

|Method|Path|Request Body|Response|Status|
|---|---|---|---|---|
|`POST`|`/v1/collections/:id/search`|`{ query, top_k? }`|`{ results[] }`|200|
|`POST`|`/v1/collections/:id/ask`|`{ question }`|`{ answer, sources[] }`|200|

### Error Response Format

All errors follow this envelope:

```json
{
  "error": {
    "message": "human-readable error description"
  }
}
```

Validation errors include field details:

```json
{
  "error": {
    "message": "validation failed",
    "fields": {
      "title": "must not be blank",
      "url": "must be a valid URL"
    }
  }
}
```

### HTTP Status Codes Used

|Code|Meaning|When|
|---|---|---|
|200|OK|Successful read/update/delete|
|201|Created|Successful resource creation|
|202|Accepted|Source added (processing is async)|
|400|Bad Request|Malformed JSON, invalid input|
|401|Unauthorized|Missing or invalid auth token|
|404|Not Found|Resource doesn't exist, or belongs to another user|
|409|Conflict|Optimistic locking conflict (stale version)|
|422|Unprocessable Entity|Validation failed|
|429|Too Many Requests|Rate limit exceeded|
|500|Internal Server Error|Unexpected server error|

---

## 5. Database Schema

See [plan-summerizer-v2.prompt.md](https://file+.vscode-resource.vscode-cdn.net/home/alif/dev/go_related/summerizer/.github/prompts/plan-summerizer-v2.prompt.md) — "Database Schema Overview" section at the bottom.

Migrations created in order:

1. `000001_create_users_table` — users + citext extension
2. `000002_create_collections_table` — collections + user FK + index
3. `000003_create_sources_table` — sources + collection FK + indexes (including partial index for worker polling)
4. `000004_create_chunks_table` — chunks + source FK + vector extension + HNSW index

---

## 6. Dependencies

|Package|Purpose|Phase Added|
|---|---|---|
|`jackc/pgx/v5`|Postgres driver + connection pool|Step 3|
|`julienschmidt/httprouter`|HTTP routing|Step 2|
|`golang-jwt/jwt/v5`|JWT creation + validation|Step 5|
|`golang.org/x/crypto`|bcrypt password hashing|Step 4|
|`pgvector/pgvector-go`|pgvector type support for pgx|Step 12|
|`PuerkitoBio/goquery`|HTML DOM parsing for web scraping|Step 9|
|`go-shiori/go-readability`|Article text extraction from HTML|Step 9|
|`kkdai/youtube/v2`|YouTube transcript extraction|Step 10|
|`pdfcpu/pdfcpu`|PDF text extraction (pure Go)|Step 11|
|`sashabaranov/go-openai`|OpenAI-compatible API client (embedding + LLM)|Step 12|
|`golang-migrate/migrate/v4`|SQL schema migrations|Step 3|
|`golang.org/x/time`|Rate limiting (token bucket)|Step 15|

> **Install dependencies only when the step requires them.** Don't dump all of these into `go.mod` at Step 1. Add each one as you reach the step that needs it. This keeps your dependency tree clean and makes it obvious why each dependency exists.

---

## 7. Glossary

|Term|Meaning|
|---|---|
|**RAG**|Retrieval-Augmented Generation — find relevant context from a knowledge base, then use an LLM to generate an answer using that context. Reduces hallucination by grounding answers in real sources.|
|**Embedding**|A fixed-length vector of floats (e.g., 768 numbers) that captures the semantic meaning of text. Similar texts have similar vectors. Used for similarity search.|
|**Vector Search**|Finding the most similar vectors to a query vector. pgvector does this in Postgres using indexes like HNSW.|
|**HNSW**|Hierarchical Navigable Small World — an index structure for approximate nearest neighbor search. Trades perfect accuracy for massive speed. pgvector supports it natively.|
|**Cosine Distance**|A measure of angle between two vectors. 0 = identical direction (most similar), 2 = opposite direction (least similar). pgvector uses the `<=>` operator.|
|**Chunk**|A piece of text split from a larger document. Embedding models have token limits, so large documents must be split. Chunks should be large enough to be meaningful but small enough to be specific.|
|**Token**|The basic unit AI models work with. Roughly: 1 token ≈ 4 characters ≈ 0.75 words in English.|
|**Optimistic Locking**|A concurrency control strategy. Read a record with a version number, update it with `WHERE version = N`, increment version. If someone else updated it first, the WHERE clause matches 0 rows → conflict detected.|
|**`FOR UPDATE SKIP LOCKED`**|A Postgres locking clause for job queues. `FOR UPDATE` locks the selected row so no other transaction can grab it. `SKIP LOCKED` skips rows already locked by another worker, instead of waiting. Gives you safe concurrent job claiming.|
|**Graceful Shutdown**|Shutting down a server by (1) stopping new connections, (2) finishing in-flight work, (3) closing resources. Prevents data corruption and stuck-in-progress jobs.|
|**Idempotent**|An operation that produces the same result if executed multiple times. Critical for retry logic — re-running a failed step shouldn't create duplicate chunks.|