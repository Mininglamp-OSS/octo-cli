# Changelog

All notable changes to `octo-cli` are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **`octo-cli docs search`** — permission-scoped full-text search across online
  documents, sheets, boards, and mounted HTML documents registered in docs-backend
  via `POST /v1/bot/docs/search`. Supports repeatable `--doc-type` filters, manual
  cursor continuation, and `--page-all`. Resolve an HTML result's `octoDocSlug`
  with `docs get` before continuing in the separate `html` domain.
  The metadata-driven pagination engine now supports custom item/cursor paths,
  explicit cursor-based has-more inference, POST body cursors, and opt-in
  repeated-cursor detection with a `PAGINATION_LOOP` error.
- **`octo-cli summary` domain** — `summary create|list|get|result` lets a
  personal Agent create owner-only summaries from explicit authorized sources,
  then discover and cite summaries visible to its human owner. The embedded
  spec targets the gateway's `/summary/api/v1/bot/*` mount, suppresses client
  space headers, and ships with the `octo-summary` Skill. Listing
  filters title or topic; result reads include citation metadata while the
  backend omits surrounding-message context for bot requests.
  **Currently withheld** behind `x-octo-disabled` (skill `disabled: true`):
  the commands and skill are not listed by the CLI **until the Summary
  backend create route (Mininglamp-OSS/octo-smart-summary#181) is merged and
  deployed, and the create feature
  is switched on via `BOT_SUMMARY_CREATE_ENABLED=1`**; `octo-cli schema
  summary.*` still introspects. Flip both flags in a one-line follow-up
  once the backend is live.
- **`octo-cli message search` family** (6 subcommands) — full-text message and
  file search: `message search` (messages), `search all` (messages + files),
  `search files`, `search media` (images/videos, in-channel only, no keyword),
  `search around` (context window around an anchor message, in-channel only),
  and `search groups` (cross-channel aggregated overview of which channels
  matched). `--chat-id` decides scope: with it, in-channel; without it,
  `search`/`all`/`files` route to their cross-channel `_search_global_*`
  endpoints (plain `search` cross-channel degrades to a mixed messages+files
  feed). Three token subjects: `bf_` (User Bot, searches as the bot),
  `bf_ --on-behalf-of <uid>` (OBO — searches as that real person, requires an
  active grant), and `uk_` (user API key — real-person identity, routed to
  `/v1/user/*`). `app_` (App Bot) tokens are **rejected locally** with a
  `validation` error before any request. Path routing (chat-id → global,
  `uk_` → `/v1/user`) is done CLI-side in `internal/client/search_route.go`;
  the `uk_` prefix is now a recognized credential kind (`user_key`).
- **`octo-cli html` domain** (20 operations) — an agent-facing CLI for the
  octo-doc interactive-HTML document service, a **separate backend** from
  `docs`. Covers the full lifecycle: `publish` immutable versions, `list` /
  `get` / `versions` / `rm`, author `draft save|promote`, `share` / `unshare`
  reader codes, per-uid `grant add|list|rm`, media `asset add|ls|rm`, inline
  `comment list|add`, agent `element get|replace`, and `reply`. Generated from
  the embedded `internal/registry/specs/html.json` OpenAPI spec; ships with the
  `octo-html` skill and CLAUDE.md command-tree docs.
- **`octo-cli docs share get|set`** — program the space-level share scope of a
  document. `docs share get <docId>` reads the current `{docId, shareScope,
  shareRole}` (needs reader); `docs share set <docId> --scope
  restricted|anyone_in_space [--role read|edit]` changes it (needs admin). The
  scope enum is `restricted` / `anyone_in_space` and the role enum is `read` /
  `edit`; a valid `--role` is required with `--scope anyone_in_space` and is
  ignored (stored as `read`) with `--scope restricted`. This targets the
  planned `/v1/bot/docs/{docId}/share` endpoint (backend lands in
  octo-docs-backend#68); merge this after that endpoint ships. Backed by a
  new `x-octo-flag` alias on request-body properties, so the clean `--scope` /
  `--role` flags front the `shareScope` / `shareRole` wire keys.

### Changed
- **`octo-cli html` now documents both identifier modes** — a first publish
  mounted to a group, space, or thread synchronously registers and returns a
  canonical `doc_id`; later non-publish operations use it. An unmounted or
  empty-mount publish remains unregistered legacy compatibility, returns an
  empty/no `doc_id`, and continues to use its slug. Query/body wire keys remain
  `slug`; path argument help now says `doc-id` without changing request paths.
- **Generated service commands now reject incomplete JSON bodies locally** —
  request-schema `required` fields and nested `minItems` constraints are
  validated after merging `--data` with promoted body flags, before any HTTP
  request is sent. Optional request bodies remain optional.
- **Renamed the binary and CLI command from `octo` to `octo-cli`** for
  consistency with the repository and Go module name. This affects every
  install path: release archives are now `octo-cli_<version>_<os>_<arch>`,
  `go install` builds `./cmd/octo-cli`, the Homebrew formula and `install.sh`
  install an `octo-cli` binary, and all commands are invoked as
  `octo-cli <command>`. **Breaking:** scripts, aliases, and shell completions
  that call `octo` must switch to `octo-cli`.

## [0.5.0] — 2026-05-28

### Added
- **`octo skills` command** for embedded skill discovery — lists or
  extracts the Agent skills bundled in the binary (`octo-shared`,
  `octo-messaging`, `octo-files`, `octo-matter`). Useful for agent
  runtimes that load skills from `octo` at startup. (#16)
- **Encrypted credential profiles** via `octo auth login | status |
  logout | list` — bot tokens now live in `~/.octo-cli` (plaintext
  `config.json` metadata + AES-256-GCM `credentials.enc` token store)
  keyed by `--profile` / `--bot-id` (env `OCTO_BOT_ID`). `OCTO_BOT_TOKEN`
  remains a fallback. Each success envelope echoes the active
  `identity` (`{profile, robot_id, bot_kind, source}`) so agents can
  detect credential misuse. (#17)

### Changed
- **Matter domain withheld** behind a new `x-octo-disabled` spec flag —
  the spec stays embedded (`octo schema matter.*` still introspects it)
  but the command subtree and the `octo-matter` skill are hidden until
  the backend Matter API stabilises. Flip the spec flag and the skill
  frontmatter to re-enable. (#19)
- **Service-domain parent commands** (`octo thread`, `octo group`,
  `octo file`, …) now reject unknown subcommands with
  `unknown subcommand %q for %q` and exit 2 instead of silently printing
  help and exiting 0. Required by the `thread delete` removal so
  automation can detect a missing operation; also catches typos like
  `octo group lisst`. Parent help (`octo thread`, `octo thread --help`)
  still works without a token; only leaf operations require
  authentication.

### Removed
- **`octo thread delete`** — withdrew the thread-deletion command.
  Threads expose no bot-accessible archive or soft-close path, so a
  hard delete was inconsistent with the convention that bots get no
  destructive operations. The backend route is untouched; only the CLI
  command and its `thread.delete` spec entry are removed.

### Fixed
- **`octo file presigned`** and other `bot_api` specs realigned with
  octo-server — `file.presigned` now declares the required `fileSize`
  query param (previous calls failed with `HTTP 400 fileSize 参数必填`).
  Several other spec drifts also corrected. (#14)
- **Auth gate on service parents** no longer fires for parent help or
  unknown-subcommand handling — the `unknown subcommand` envelope and
  `octo thread --help` now work without a bot token, restoring the
  safety property of the `thread delete` removal.

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

[Unreleased]: https://github.com/Mininglamp-OSS/octo-cli/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/Mininglamp-OSS/octo-cli/compare/555c959...v0.5.0
[0.4.0]: https://github.com/Mininglamp-OSS/octo-cli/tree/555c959
