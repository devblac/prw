.PHONY: build test coverage lint clean install help

# Build variables
BINARY_NAME=prw
BUILD_DIR=bin
VERSION?=dev
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS=-ldflags "-X github.com/devblac/prw/internal/version.Version=$(VERSION) -X github.com/devblac/prw/internal/version.Commit=$(COMMIT)"

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the binary
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/prw
	@echo "Built: $(BUILD_DIR)/$(BINARY_NAME)"

test: ## Run tests
	@echo "Running tests..."
	go test -v ./...

coverage: ## Run tests with coverage
	@echo "Running tests with coverage..."
	go test -coverprofile=coverage.out ./...
	@echo ""
	@echo "Coverage summary:"
	@go tool cover -func=coverage.out | grep total:
	@echo ""
	@echo "For detailed HTML coverage report, run: go tool cover -html=coverage.out"

lint: ## Run linters
	@echo "Running go vet..."
	@go vet ./...
	@echo "go vet passed!"
	@if command -v golangci-lint >/dev/null 2>&1; then \
		echo "Running golangci-lint..."; \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, skipping (install from https://golangci-lint.run/)"; \
	fi

clean: ## Clean build artifacts
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out
	@echo "Cleaned!"

install: build ## Install the binary to $GOPATH/bin
	@echo "Installing to $(GOPATH)/bin..."
	@cp $(BUILD_DIR)/$(BINARY_NAME) $(GOPATH)/bin/
	@echo "Installed!"

fmt: ## Format code
	@echo "Formatting code..."
	@go fmt ./...

tidy: ## Tidy go.mod
	@echo "Tidying go.mod..."
	@go mod tidy

all: clean lint test build ## Run clean, lint, test, and build
