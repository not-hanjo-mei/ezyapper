# EZyapper Makefile

.PHONY: all build clean test test-coverage test-race lint fmt run docker-build docker-up docker-down docker-logs deps deps-update vuln ci help

# Build variables
BINARY_NAME=ezyapper
BUILD_DIR=.
GO=go
GOFLAGS=-v

all: build

# Build the binary
build:
	$(GO) build $(GOFLAGS) -o $(BINARY_NAME) ./cmd/bot

# Build for multiple platforms
build-all:
	GOOS=linux GOARCH=amd64 $(GO) build -o $(BINARY_NAME)-linux-amd64 ./cmd/bot
	GOOS=windows GOARCH=amd64 $(GO) build -o $(BINARY_NAME)-windows-amd64.exe ./cmd/bot
	GOOS=darwin GOARCH=amd64 $(GO) build -o $(BINARY_NAME)-darwin-amd64 ./cmd/bot

# Clean build artifacts
clean:
	rm -f $(BINARY_NAME) $(BINARY_NAME)-*
	rm -f *.out *.test
	rm -rf coverage.html

# Run tests
test:
	$(GO) test -v ./...

# Run tests with coverage
test-coverage:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run tests with race detection
test-race:
	$(GO) test -race -v ./...

# Run linter (requires golangci-lint)
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, using go vet"; \
		$(GO) vet ./...; \
	fi

# Format code
fmt:
	$(GO) fmt ./...

# Run the bot (requires config.yaml)
run: build
	./$(BINARY_NAME) -config config.yaml

# Docker commands
docker-build:
	docker-compose build

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

docker-logs:
	docker-compose logs -f

# Install dependencies
deps:
	$(GO) mod download
	$(GO) mod tidy

# Update dependencies
deps-update:
	$(GO) get -u ./...
	$(GO) mod tidy

# Check for vulnerabilities (requires govulncheck)
vuln:
	@if command -v govulncheck >/dev/null 2>&1; then \
		govulncheck ./...; \
	else \
		echo "govulncheck not installed. Run: go install golang.org/x/vuln/cmd/govulncheck@latest"; \
	fi

# CI target (runs checks for CI pipeline)
ci: fmt lint test

# Help
help:
	@echo "Available targets:"
	@echo "  build         - Build the binary"
	@echo "  build-all     - Build for multiple platforms"
	@echo "  clean         - Clean build artifacts"
	@echo "  test          - Run tests"
	@echo "  test-coverage - Run tests with coverage report"
	@echo "  test-race     - Run tests with race detection"
	@echo "  lint          - Run linter"
	@echo "  fmt           - Format code"
	@echo "  run           - Build and run the bot"
	@echo "  docker-build  - Build Docker images"
	@echo "  docker-up     - Start Docker containers"
	@echo "  docker-down   - Stop Docker containers"
	@echo "  deps          - Install dependencies"
	@echo "  deps-update   - Update dependencies"
	@echo "  vuln          - Check for vulnerabilities"
	@echo "  ci            - Run CI checks"
	@echo "  help          - Show this help"
