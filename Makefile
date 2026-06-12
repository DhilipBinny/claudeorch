# claudeorch — Makefile for local development.
#
# Targets:
#   make build          Build the binary to ./bin/claudeorch (injects version metadata via ldflags).
#   make test           Run unit tests.
#   make test-integration  Run unit + integration tests.
#   make vet            Run `go vet`.
#   make fmt            Run `gofmt -w` on all Go sources.
#   make fmt-check      Verify no files need reformatting (CI-friendly).
#   make check          Run vet + fmt-check + test.
#   make clean          Remove build artifacts.
#
# These mirror the verification gates in the design doc.

BINARY      := claudeorch
BIN_DIR     := bin
PKG         := ./cmd/claudeorch

# Build metadata — populated from git when available, falls back to placeholders.
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS     := -s -w \
               -X main.Version=$(VERSION) \
               -X main.Commit=$(COMMIT) \
               -X main.BuildDate=$(BUILD_DATE)

.PHONY: help build test test-integration vet fmt fmt-check check clean

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) 2>/dev/null | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-22s %s\n", $$1, $$2}'
	@echo ""
	@echo "Run 'make check' before committing."

build: ## Build the claudeorch binary into ./bin/
	@mkdir -p $(BIN_DIR)
	go build -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(PKG)
	@echo "→ $(BIN_DIR)/$(BINARY) (version $(VERSION))"

test: ## Run unit tests
	go test -race ./...

test-integration: ## Run unit + integration tests
	go test -race ./... && go test -race -tags=integration ./tests/integration/...

vet: ## Run go vet
	go vet ./...

fmt: ## Run gofmt -w on all sources
	gofmt -w .

fmt-check: ## Verify no files need reformatting
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Files need reformatting (run 'make fmt'):"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

check: vet fmt-check test ## Run vet + fmt-check + test

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)
