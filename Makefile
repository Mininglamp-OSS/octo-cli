.PHONY: build test lint fmt vet clean ci hooks hooks-install tools help

# Build metadata is injected at link time. Release builds override VERSION.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/Mininglamp-OSS/octo-cli/cmd.Version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o bin/octo-cli ./cmd/octo-cli

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run

fmt:
	@out=$$(gofmt -l . | grep -v vendor || true); \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

clean:
	rm -rf bin/

ci: fmt vet lint test build

# Install & activate git hooks (lefthook). Run once after cloning.
hooks: hooks-install
hooks-install:
	@command -v lefthook >/dev/null 2>&1 || { \
	  echo "lefthook not found. Install one of:"; \
	  echo "  brew install lefthook"; \
	  echo "  go install github.com/evilmartians/lefthook@latest"; \
	  exit 1; }
	lefthook install

# Install optional dev tools the hooks/CI use (golangci-lint, gci).
tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install github.com/daixiang0/gci@latest

help:
	@echo "Targets:"
	@echo "  build   build ./bin/octo-cli with version from git"
	@echo "  test    go test -race -count=1 ./..."
	@echo "  lint    golangci-lint run"
	@echo "  fmt     fail if any Go file needs gofmt"
	@echo "  vet     go vet ./..."
	@echo "  ci      fmt + vet + lint + test + build (what CI runs)"
	@echo "  hooks   install & activate git hooks (lefthook)"
	@echo "  tools   install optional dev tools (golangci-lint, gci)"
	@echo "  clean   remove ./bin"
	@echo ""
	@echo "Add a new service domain: write spec → embed → auto-register"
	@echo "  1. Create internal/registry/specs/<domain>.json (OpenAPI 3.x)"
	@echo "  2. Rebuild — //go:embed picks up the new spec automatically"
	@echo "  3. Done — all operations auto-registered."
