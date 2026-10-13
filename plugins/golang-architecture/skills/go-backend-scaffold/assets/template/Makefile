BINARY  := goservice
PKG     := ./...
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-10s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: run
run: ## Run locally, loading .env when present
	@set -a; [ -f .env ] && . ./.env; set +a; go run ./cmd/server

.PHONY: build
build: ## Build a static, stripped binary into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/$(BINARY) ./cmd/server

.PHONY: test
test: ## Run all tests with the race detector
	go test -race -count=1 $(PKG)

.PHONY: cover
cover: ## Run tests and print total coverage
	go test -race -count=1 -coverprofile=coverage.out $(PKG)
	@go tool cover -func=coverage.out | tail -1

.PHONY: lint
lint: ## Run golangci-lint (v2)
	golangci-lint run

.PHONY: fmt
fmt: ## Format all Go code
	gofmt -w .

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy

.PHONY: verify
verify: ## What CI runs: formatting, vet, module integrity, race tests, build
	@test -z "$$(gofmt -l .)" || (echo "files need gofmt:"; gofmt -l .; exit 1)
	go vet $(PKG)
	go mod verify
	$(MAKE) --no-print-directory test build

.PHONY: docker
docker: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY):$(VERSION) .
