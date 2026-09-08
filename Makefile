# Service Scheduler — developer entry points.
# All targets run from the repository root.

GO            ?= go
BIN           := bin/server
PKG           := ./...
DATABASE_URL  ?= postgres://scheduler:scheduler@localhost:5432/scheduler?sslmode=disable

.PHONY: help build run test test-unit test-integration lint fmt vet tidy up down logs clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

build: ## Compile the server binary
	$(GO) build -o $(BIN) ./cmd/server

run: ## Run the server locally (expects DATABASE_URL; seeds reference data)
	DATABASE_URL=$(DATABASE_URL) APP_SEED=true $(GO) run ./cmd/server

test: ## Run all tests (unit + integration; integration needs Docker for testcontainers)
	$(GO) test -race -count=1 $(PKG)

test-unit: ## Run only the fast, Docker-free tests
	$(GO) test -race -short -count=1 $(PKG)

test-integration: ## Run only the testcontainers-backed tests
	$(GO) test -race -count=1 ./internal/repository/... ./internal/service/... ./internal/httpapi/...

lint: vet ## Static analysis (golangci-lint if installed, otherwise go vet + gofmt check)
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; else echo "golangci-lint not installed; ran go vet + gofmt only"; fi
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt: files need formatting" && exit 1)

vet: ## go vet
	$(GO) vet $(PKG)

fmt: ## gofmt all sources
	gofmt -w .

tidy: ## go mod tidy
	$(GO) mod tidy

up: ## Start Postgres and the API with docker compose
	docker compose up --build -d

down: ## Stop and remove the compose stack (including the database volume)
	docker compose down -v

logs: ## Tail compose logs
	docker compose logs -f

clean: ## Remove build output
	rm -rf bin coverage.out
