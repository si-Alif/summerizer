#Include env variables from .envrc file
include .envrc

# =================================================================
# HELPER COMMANDS
# =================================================================

## help : print this help message
.PHONY: help
help :
	@echo 'Usage:'
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'


# Create a new confirmation
.PHONY: confirm
confirm :
	@echo -n 'Are you sure you want to proceed? (y/n): ' && read ans && [ $${ans:-N} = y ]


# =================================================================
# DOCKER COMMANDS
# =================================================================

## db/start : start the database container
.PHONY: db/start
db/start:
	docker compose up -d
	@echo "Waiting for Postgres to be ready..."
	@until docker compose exec db pg_isready -U summerizer > /dev/null 2>&1; do sleep 0.5; done
	@echo "Postgres is ready."

## db/stop : stop the database container (data preserved)
.PHONY: db/stop
db/stop:
	docker compose down

## db/destroy : stop and delete all database data
.PHONY: db/destroy
db/destroy: confirm
	docker compose down -v
	@echo "Database data destroyed."

## db/logs : show database container logs
.PHONY: db/logs
db/logs:
	docker compose logs -f db

# =================================================================
# DEVELOPMENT COMMANDS
# =================================================================

## run/api : run the cmd/api application
.PHONY: run/api
run/api:
	@go run ./cmd/api -db-dsn=${SUMMERIZER_DB_DSN}

## db/psql : connect to the Greenlight database using psql
.PHONY: db/psql
db/psql:
	psql ${SUMMERIZER_DB_DSN}

## db/migrations/up : apply all up migrations
.PHONY: db/migrations/up
db/migrations/up: confirm
	@echo "Running up migrations..."
	migrate -path ./migrations -database ${SUMMERIZER_DB_DSN} up

## db/migrations/new name=$1 : create a new migration file with the given name
.PHONY: db/migrations/new
db/migrations/new :
	@echo "Creating new migration for ${name}..."
	migrate create -seq -ext=.sql -dir=./migrations ${name}

## db/migrations/down n=$1 : roll back n migrations
.PHONY: db/migrations/down
db/migrations/down:
	@echo 'Rolling back $(n) migration(s)...'
	migrate -path ./migrations -database ${SUMMERIZER_DB_DSN} down $(n)

## db/migrations/force v=$1 : force migration version (use after dirty state)
.PHONY: db/migrations/force
db/migrations/force:
	@echo 'Forcing migration version to $(v)...'
	migrate -path ./migrations -database ${SUMMERIZER_DB_DSN} force $(v)

## db/migrations/version : show current migration version
.PHONY: db/migrations/version
db/migrations/version:
	migrate -path ./migrations -database ${SUMMERIZER_DB_DSN} version

# =================================================================
# QUALITY CONTROL COMMANDS
# =================================================================

## tidy : tidy module dependencies, verify, vendor, and format code
.PHONY : tidy
tidy :
	@echo "Tidying module dependencies..."
	go mod tidy
	@echo "Module dependencies tidied."
	@echo "Verifying module dependencies..."
	go mod verify
	@echo "Module dependencies verified."
	@echo "Vendoring module dependencies..."
	go mod vendor
	@echo "Module dependencies vendored."
	@echo "Formatting .go files..."
	go fmt ./...

## audit : audit module dependencies, vet code, and run tests
.PHONY : audit
audit :
	@echo "Auditing module dependencies for vulnerabilities..."
	go mod tidy -diff
	go mod verify
	@echo "Audit complete."
	@echo "Vetting code..."
	go vet ./...
	go tool staticcheck ./...
	@echo "Vetting complete."
	@echo "Running tests..."
	go test -race -vet=off ./...
	@echo "Tests complete."
	@echo "All quality control checks passed."

## build/api : build the cmd/api application
.PHONY : build/api
build/api :
	@echo "Building cmd/api application's local machine compatible binary build..."
	go build -ldflags='-s' -o ./bin/api ./cmd/api
	@echo "Local machine compatible build complete. Binary located at ./bin/api"
	@echo "Building cmd/api application's production compatible binary build(linux/amd64)..."
	GOOS=linux GOARCH=amd64 go build -ldflags='-s' -o ./bin/linux-amd64/api ./cmd/api
	@echo "Production compatible build complete. Binary located at ./bin/linux-amd64/api"