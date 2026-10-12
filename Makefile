# Developer entry points. Requires Go 1.26+ and Docker (for the database).
TEST_DB ?= postgres://gcalm:gcalm@localhost:5432/gcalm_test?sslmode=disable
DEV_DB  ?= postgres://gcalm:gcalm@localhost:5432/gcalm?sslmode=disable
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: help up db run test test-int lint fmt build docker hero clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-10s %s\n", $$1, $$2}'

up: ## Full local stack with demo data (docker compose)
	docker compose up --build

db: ## Start only PostgreSQL and create the test database
	docker compose up -d db
	@sleep 3
	-docker compose exec -T db psql -U gcalm -c "CREATE DATABASE gcalm_test" >/dev/null 2>&1

run: ## Run the portal locally against the compose database (fake Google, demo data)
	DATABASE_URL=$(DEV_DB) DEMO_SEED=true INPROCESS_SCHEDULER=true go run ./cmd/gcalm

test: ## Unit tests (integration tests are skipped)
	go test ./...

test-int: ## Unit + integration tests (needs `make db`)
	TEST_DATABASE_URL=$(TEST_DB) go test -race -p 1 -count=1 ./...

lint: ## gofmt + go vet
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "gofmt needed" && exit 1)
	go vet ./...

fmt: ## Format the code
	gofmt -w .

build: ## Build the binaries into ./bin
	CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION)" -o bin/gcalm ./cmd/gcalm
	CGO_ENABLED=0 go build -o bin/edsim ./cmd/edsim

docker: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t gcalm:$(VERSION) .

hero: ## Regenerate the isometric hero illustration
	node scripts/gen-hero.mjs > internal/web/static/img/hero-laptop.svg

clean: ## Remove build outputs and local fake state
	rm -rf bin .dev
