# Changelog

All notable changes to `octo-cli` are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Removed
- `octo thread delete` — withdrew the thread-deletion command. Threads expose no
  bot-accessible archive or soft-close path, so a hard delete was inconsistent
  with the convention that bots get no destructive operations. The backend route
  is untouched; only the CLI command and its `thread.delete` spec entry are
  removed. Command/operation totals drop 51/48 → 50/47.

### Changed
- Service-domain parent commands (`octo thread`, `octo group`, `octo file`, …)
  now reject unknown subcommands with `unknown subcommand %q for %q` and exit 2
  instead of silently printing help and exiting 0. Required by the `thread
  delete` removal above so automation can detect a missing operation; also
  catches typos like `octo group lisst`. Parent help (`octo thread`,
  `octo thread --help`) is unchanged and still works without a token; only
  leaf operations require authentication.

## [0.4.0] — 2026-05

### Architectural overhaul — metadata-driven command tree

This release replaces the hand-written command layer with an auto-generated
one, redesigns the output contract around a JSON envelope, and adopts
Factory-based DI throughout.

### Added
- **Metadata-driven service engine** (`cmd/service`) — every service command
  is auto-registered at startup from embedded OpenAPI 3.x specs. Adding or
  changing an endpoint is a spec edit, no Go code change.
- **Seven domain specs** embedded via `//go:embed`: `matter` (14),
  `message` (4), `group` (9), `thread` (9), `file` (4), `bot` (6),
  `event` (2) — 48 operations total.
- **JSON envelope I/O** (`internal/output`) — stable
  `{ok, identity, data, _pagination, _rate_limit}` success shape and
  `{ok:false, error:{type, code, message, hint, detail}}` error shape.
- **Error taxonomy + exit codes** — `auth_error` → 3, `validation`/`config`
  → 2, all others → 1.
- **Factory DI** (`internal/cmdutil.Factory`) — `ConfigFunc`,
  `CredentialFunc`, `ClientFunc`, `RegistryFunc` accessors; no mutable
  package-level globals.
- **Multi-service routing** — each operation selects its base URL from
  `x-octo-base-url` (matters, dmworkim, or the `OCTO_API_URL` fallback).
- **Universal flags** — `--format`, `--jq`, `--dry-run`, `--verbose`,
  `--timeout`, `--no-retry`, `--space`, plus `--page-all` / `--page-limit`
  on paginated operations.
- **Retry with exponential backoff + jitter** and `Retry-After` handling
  (client).
- **Multipart upload** and **binary/redirect response** handling for the
  `file.*` operations.
- **`octo schema` discovery command** — list and inspect the embedded spec
  registry fully offline.
- **`octo api` generic passthrough** — raw `METHOD /path` against any
  configured service.
- **`octo config show`** — resolved config with the bot token masked.
- **Agent skills** under `skills/` — `octo-shared`, `octo-matter`,
  `octo-messaging`, `octo-files` with YAML frontmatter.
- **Shell completion** via cobra's built-in `completion` command (bash,
  zsh, fish, powershell).
- **CI**: gofmt, vet, golangci-lint, `go test -race`, coverage, build,
  `go mod tidy` drift check, Go 1.24.x.

### Changed
- **Bot-only identity model.** `OCTO_BOT_TOKEN` carries an `app_*` (App
  Bot) or `bf_*` (User Bot) token; user login is removed.
- **Agent-first output.** Default format is `json`; no interactive prompts;
  stable machine-parseable envelope on every path.

### Removed
- Legacy hand-written `todo`, `goal`, `assign`, `comment`, and `attachment`
  subcommand trees. All equivalent functionality is now under `octo matter`
  and auto-registered from the spec.

[Unreleased]: https://github.com/Mininglamp-OSS/octo-cli/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/Mininglamp-OSS/octo-cli/releases/tag/v0.4.0
