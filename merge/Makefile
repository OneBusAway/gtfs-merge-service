.PHONY: all build test clean docker-build

# Variables
BINARY_NAME=gtfs-merge
DOCKER_IMAGE=oba-merge-service
GO=go
GOTEST=$(GO) test
GOBUILD=$(GO) build
GOCLEAN=$(GO) clean

# Default target
all: build

# Build the binary
build:
	$(GOBUILD) -o $(BINARY_NAME) -v ./cmd/gtfs-merge

# Clean build artifacts
clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)

# Run unit tests
test:
	$(GOTEST) ./...

# Run tests with coverage
test-coverage:
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Lint the code
lint:
	@which golangci-lint > /dev/null || (echo "golangci-lint not found, installing..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run ./...

# Format code
fmt:
	$(GO) fmt ./...

# Vet code
vet:
	$(GO) vet ./...

# Docker targets
docker-build:
	docker build --tag $(DOCKER_IMAGE) -f ../Dockerfile ..

# Install dependencies
deps:
	$(GO) mod download
	$(GO) mod tidy

# Check for outdated dependencies
deps-check:
	$(GO) list -u -m all

# Help target
help:
	@echo "Available targets:"
	@echo "  make build            - Build the binary"
	@echo "  make test             - Run unit tests"
	@echo "  make lint             - Lint the code"
	@echo "  make fmt              - Format the code"
	@echo "  make vet              - Vet the code"
	@echo "  make docker-build     - Build Docker image"
	@echo "  make clean            - Clean build artifacts"
	@echo "  make deps             - Install dependencies"
	@echo "  make help             - Show this help message"