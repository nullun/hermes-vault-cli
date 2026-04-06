BINARY   := hermes
MODULE   := github.com/nullun/hermes-vault-cli
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all build install clean test vet fmt lint run help

all: build

## build: compile the binary to ./hermes
build:
	go build -trimpath $(LDFLAGS) -o $(BINARY) ./cmd/hermes

## install: install the binary to $(GOPATH)/bin
install:
	go install -trimpath $(LDFLAGS) ./cmd/hermes

## run: build and run with --help
run: build
	./$(BINARY) --help

## test: run all tests
test:
	go test ./...

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go source files
fmt:
	gofmt -w .

## lint: run golangci-lint
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not found. Install it from https://golangci-lint.run/"; \
		exit 1; \
	fi

## tidy: tidy and verify go modules
tidy:
	go mod tidy
	go mod verify

## clean: remove build artefacts
clean:
	rm -f $(BINARY)

## help: list available targets
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
