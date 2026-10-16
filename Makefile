# Signalbox development tasks. Run `make help` for the list.

GO      ?= go
NPM     ?= npm
PKG     := github.com/CemTbi/Signalbox
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(PKG)/internal/buildinfo.version=$(VERSION) \
	-X $(PKG)/internal/buildinfo.commit=$(COMMIT) \
	-X $(PKG)/internal/buildinfo.date=$(DATE)

.DEFAULT_GOAL := help

.PHONY: help
help: ## List the available tasks
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build bin/signalbox and bin/signalbox-server
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

# The database that `make dev-db` starts. Override it to use another one.
SIGNALBOX_DATABASE_URL ?= postgres://signalbox:signalbox@localhost:5432/signalbox?sslmode=disable
export SIGNALBOX_DATABASE_URL
COMPOSE := docker compose -f deploy/compose/compose.yaml

.PHONY: dev-db
dev-db: ## Start PostgreSQL on localhost:5432 for `make run`
	$(COMPOSE) -f deploy/compose/compose.dev.yaml up -d --wait postgres

.PHONY: run
run: ## Run the server on :8080 with readable logs (needs `make dev-db`)
	$(GO) run ./cmd/signalbox-server serve --log-format text

.PHONY: test
test: ## Run Go tests with the race detector (database tests need Docker)
	$(GO) test -race ./...

.PHONY: test-short
test-short: ## Run Go tests without the performance budgets
	$(GO) test -short ./...

.PHONY: lint
lint: ## Lint Go code and Signalbox's own API spec
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run
	$(GO) run ./cmd/signalbox lint --fail-on warning api/openapi.yaml

.PHONY: fmt
fmt: ## Format Go code
	gofmt -w .

.PHONY: web
web: ## Build the web app into the server binary's embed directory
	$(NPM) --prefix web ci
	$(NPM) --prefix web run build

.PHONY: web-check
web-check: ## Lint, type-check and test the web app
	$(NPM) --prefix web run lint
	$(NPM) --prefix web run typecheck
	$(NPM) --prefix web test

.PHONY: generate
generate: ## Regenerate the TypeScript API types from api/openapi.yaml
	$(NPM) --prefix web run generate:api

.PHONY: image
image: ## Build the signalbox-server container image
	docker build -f deploy/docker/Dockerfile -t signalbox-server:dev .

.PHONY: up
up: ## Start Signalbox, PostgreSQL and ClickHouse with Docker Compose
	$(COMPOSE) up -d --build

.PHONY: down
down: ## Stop the Docker Compose stack
	$(COMPOSE) down

.PHONY: clean
clean: ## Remove build output
	rm -rf bin dist
