.PHONY: build test lint fmt vet clean ci help

# Build metadata is injected at link time. Release builds override VERSION.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/Mininglamp-OSS/octo-cli/cmd.Version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o bin/octo ./cmd/octo

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

help:
	@echo "Targets:"
	@echo "  build   build ./bin/octo with version from git"
	@echo "  test    go test -race -count=1 ./..."
	@echo "  lint    golangci-lint run"
	@echo "  fmt     fail if any Go file needs gofmt"
	@echo "  vet     go vet ./..."
	@echo "  ci      fmt + vet + lint + test + build (what CI runs)"
	@echo "  clean   remove ./bin"
	@echo ""
	@echo "Add a new service domain: write spec → embed → auto-register"
	@echo "  1. Create internal/registry/specs/<domain>.json (OpenAPI 3.x)"
	@echo "  2. Rebuild — //go:embed picks up the new spec automatically"
	@echo "  3. Done — all operations auto-registered."
