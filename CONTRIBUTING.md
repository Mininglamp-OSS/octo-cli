# Contributing to octo-cli

Thanks for your interest in improving `octo`. This document covers what you
need to know to get a change merged.

## Prerequisites

- Go **1.24+** (see [`go.mod`](./go.mod))
- `make` (optional — targets are plain shell)
- `golangci-lint` for local lint runs (CI enforces the same config)

## Quick Start

```bash
git clone https://github.com/Mininglamp-OSS/octo-cli.git
cd octo-cli

make build            # builds ./bin/octo
make test             # go test -race -count=1 ./...
make lint             # golangci-lint run
make ci               # fmt + vet + lint + test + build (what CI runs)
```

## Architecture Overview

octo-cli is metadata-driven. The command tree is built at startup from
OpenAPI 3.x specs embedded via `//go:embed`:

```
internal/registry/specs/*.json      OpenAPI 3.x specs, embedded into the binary
    │
    ▼
internal/registry (Registry)        Parses specs, exposes operations by id
    │
    ▼
cmd/service (Service Engine)        Auto-registers one cobra command per operation
    │
    ▼
internal/cmdutil (Factory, DI)      Wires config + credential + client per command
    │
    ▼
internal/client (HTTP client)       Multi-service routing, retry, dry-run
    │
    ▼
internal/output (envelope)          Stable JSON envelope; error taxonomy
```

Design constraints:

- **`internal/output` is a leaf package** — it must not import any other
  `internal/*` package.
- **No mutable package-level globals.** Allowed `var` declarations:
  (a) ldflags-injected build metadata (`cmd/build.go`), (b) `//go:embed`
  file systems (`internal/registry`), and (c) immutable lookup tables
  (e.g. `backendErrorMapping`). Everything else flows through the Factory.
- **All text in English.**

## Add a New API Domain

Because the command tree is metadata-driven, adding a new domain is almost
entirely a spec change:

1. **Write an OpenAPI 3.x spec** at
   `internal/registry/specs/<domain>.json`. Use the existing specs as a
   template — note the `x-octo-*` extensions for base URL, pagination,
   risk, multipart, and binary responses.
2. **Embed it.** The file is picked up automatically by
   `//go:embed specs/*.json` in `internal/registry/loader.go`. No code
   change needed.
3. **Rebuild.** `make build`. Every operation is auto-registered with the
   right flags, required args, pagination, and risk gating.

Tests:

- Add table-driven tests for any non-trivial spec quirks.
- If an endpoint needs special handling (multipart, binary response),
  prefer extending the registry/service engine over a domain-specific
  code path.

## Code Style

- `gofmt` — run `gofmt -s -w .` before pushing.
- `go vet ./...` — must pass.
- `golangci-lint run` — CI uses `.golangci.yml`; keep it green.
- Errors wrap with `fmt.Errorf("context: %w", err)`. User-facing errors
  should be `*output.ExitError` so envelopes stay structured.
- External dependencies are limited to `cobra`, `gojq`, and the standard
  library.
- Only add comments for non-obvious *why* — avoid narrating *what* the code
  does when the names already carry that.

## Testing

- `go test -race -count=1 ./...` must pass locally before you open a PR.
- Every behavior change ships with a test. Table-driven tests preferred.
- Use Factory stubs (`ConfigFunc`, `CredentialFunc`, `ClientFunc`,
  `RegistryFunc`) — don't touch real environment or network in tests.

## Pull Request Process

1. Fork and create a topic branch from `main`.
2. Keep the change focused; split unrelated work into separate PRs.
3. Fill in the PR template. Link the issue.
4. Use **Conventional Commits** for commit messages and PR titles:
   - `feat: add matter.archive endpoint`
   - `fix: preserve ExitCode through retry exhaustion`
   - `docs: rewrite README`
   - `refactor: split service.go into four files`
   - `test: cover multipart edge cases`
   - `chore: bump golangci-lint to v1.61`
5. CI must be green: gofmt, vet, golangci-lint, `go test -race`, build,
   `go mod tidy` check.

## Reporting Security Issues

See [SECURITY.md](./SECURITY.md). Please do not file security issues in the
public tracker.

## License

By contributing you agree that your contribution will be licensed under the
project's [Apache-2.0 license](./LICENSE).
