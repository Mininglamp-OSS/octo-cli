# Changelog

All notable changes to `octo-cli` are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **`octo-cli drive` domain** (45 commands) — network drive over octo-drive:
  spaces and members, folder/file tree and `browse`, blob registration,
  two-phase upload and signed download, online-document mounts, share links,
  invites, and IM-attachment transfer. 39 leaves are generated from the new
  `drive.json` spec (42 operations); six are hand-written because they are not a
  single request or take an argument shape the engine cannot express:
  `upload file` (prepare → presigned PUT → confirm, with best-effort cancel on
  failure), `download file` and `share download` (signed URL → atomic local
  write), `share create` (branches on blob vs mounted document),
  `share blob-create` (positional file id), and `share access`. Ships with the
  `octo-drive` Skill. There is deliberately no `drive org` subtree — the
  product's member picker is a frontend filter over the space roster, not a
  backend search.
  - **Dual identity, one command surface.** A `uk_*` user API key acts as the
    real person and routes to `/v1/user/drive/*`; `bf_*` / `app_*` act as the bot
    and route to `/v1/bot/drive/*`. The routing is spec metadata, not code, so the
    command tree is identical either way. A bot must still be added as a member of
    a shared space, exactly like a person.
  - **Share hand-over is the `share_url` and nothing else.** `share access` /
    `share download` take the whole link, parse it under strict same-origin rules,
    and call the configured API with the token they extracted — the link's host is
    never contacted. Both sides require a credential; there is no anonymous share
    surface. A document link is never downloadable.
  - **Lossless uint64 ids.** Drive file ids exceed both `float64`'s exact range
    and Go's `int`, so they are decimal strings on the CLI surface (validated in
    `[0, 2^64-1]`, sent as JSON integers, returned as decimal strings) and can be
    piped verbatim from one command into the next.
  - Presigned object-storage transfers run on a separate HTTP client that carries
    no Octo credential and no space header.
- **`OCTO_TOKEN`** — a preferred token variable accepting any of the three token
  kinds (`app_*`, `bf_*`, `uk_*`). Resolution order is now stored profile →
  `OCTO_TOKEN` → `OCTO_BOT_TOKEN`; `OCTO_BOT_TOKEN` keeps working exactly as
  before, so a setup that never sets `OCTO_TOKEN` is unaffected. The success
  envelope's `identity.source` names the variable actually used
  (`env:OCTO_TOKEN` or `env:OCTO_BOT_TOKEN`).
- **Generic spec extensions**, all opt-in with omit-means-unchanged semantics:
  `x-octo-allowed-token-kinds` (local credential-kind gate →
  `TOKEN_KIND_NOT_ALLOWED`, `validation`/exit 2, matching the existing
  message-search gate), `x-octo-mount-by-token-kind` (per-kind server mount,
  applied at the single path-assembly site in `cmd/service/run.go`),
  `x-octo-response-fields` (rename/duplicate response keys, so a backend DTO's
  ambiguous `id` can surface as `share_id` + `share_token`),
  `x-octo-lossless-id-fields` (uint64 response ids as decimal strings), and
  `x-octo-secret` (mask a value in `--verbose` / `--dry-run` without changing the
  wire). No pre-existing domain declares any of them.
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
- **A transfer may no longer connect to the local machine, on the first connection as
  well as on a redirect hop.** The `https`-or-loopback-`http` rule is now decided on the
  *resolved* address rather than on how the host is spelled, and the connection then goes
  to the address that was checked, so a second lookup cannot answer differently in
  between. Previously only redirect hops were judged and only by spelling, so a hostname
  with an `A` record on `127.0.0.1` passed. Other internal ranges stay reachable — only
  this machine does not. **Deployment consequence:** a remote `OCTO_API_BASE_URL` whose
  object storage resolves to the caller's own machine will no longer transfer; point
  `OCTO_API_BASE_URL` at loopback to restore the local setup. A configured HTTP(S) proxy
  is still honoured, and a proxy on the local machine is not refused: it is not the
  storage host. Where the proxy is also the only resolver, the target cannot be
  classified locally and the transfer proceeds unclassified, reported under `--verbose`.
- **`--data null` and `--params null` are now rejected instead of being treated as an
  absent value.** Both are valid JSON that decodes into a nil map, so `--data null` used
  to behave like "no body" (and, with a promoted body flag, crash — see Fixed) while
  `--params null` behaved like "no query parameters". Both are now
  `VALIDATION_ERROR` / exit 2 with a message naming the shape. Every other non-object
  shape (`true`, a number, a string, an array) was already rejected; these two were the
  gap. **This is a user-visible behaviour change**: a script passing `null` to mean
  "nothing" must omit the flag instead.
- **A presigned URL whose host is not in canonical ASCII form is refused.** `net/http`
  canonicalises a host with IDNA before dialling it while the resolver does not, so a
  non-ASCII spelling was checked as one string and connected to as another — the
  fullwidth spellings of `127.0.0.1` and `localhost` passed every rule and reached the
  local machine. An internationalised storage host must be presented in its A-label
  (`xn--…`) form, which is what DNS carries; the CLI does not perform the mapping itself,
  because the checked string and the dialled string then could not be guaranteed equal.
- **An unknown subcommand no longer echoes the word it did not recognise.** Every
  service domain's parent command now reports
  `unknown subcommand for "octo-cli drive share"; available: access, blob-create, create, download, list, revoke`
  instead of `unknown subcommand "<word>" for "octo-cli drive share"`. The common way
  to land there is an omitted verb rather than a mistyped one — `drive share <token>`
  instead of `drive share revoke <token>` — which put a share token into an error the
  caller never asked to see; listing the real subcommands is also more useful for an
  actual typo. Exit-code classification is unchanged (still `validation`, exit 2).
- **Spec `enum` values are now enforced locally, before the request.** Enums were
  only rendered into `--help`; the value itself went to the backend unchecked, so
  `drive im-transfer create --im-channel-type 9` was forwarded despite the spec
  declaring `[1, 2, 5]`, and `-1` failed only as a backend decode error that
  leaked an internal struct name. The generic engine now rejects an out-of-set
  value with `ENUM_NOT_ALLOWED` (`validation`, exit 2, zero HTTP) and a hint
  listing the accepted values. Applies to every domain, on request-body fields
  (including nested and array-item enums, and values supplied through `--data`)
  and on query parameters alike. Comparison is by canonical form, so a value is
  accepted whether it arrives as an `int` flag, a `--data` JSON number, or a
  `json.Number` uint64. Only flags the caller actually set are checked, so an
  omitted optional enum field is untouched. Structural violations keep their
  existing `VALIDATION_ERROR` envelope. A non-scalar (object / array / null)
  against a scalar enum is rejected too, closing the last path by which a
  malformed value reached the backend and came back as an internal decode error.
  Because a spec enum narrower than the backend's real vocabulary would now
  refuse a call that used to work, `drive.doc.mount`'s `source` enum was
  corrected from `["user-mount"]` to `["user-mount", "docs-sync"]` (octo-drive's
  `docref.allowedMountSources` accepts both), and a regression test pins the
  drive enums against their backend allow-lists.
- **A path parameter may now declare `x-octo-flag`** to add an optional flag
  alternative to its positional slot. base64url ids legitimately start with `-`
  (about one in 64), which cobra parses as a flag before the command runs, so
  `drive share revoke -Ab3…` failed with "unknown shorthand flag" unless the
  caller knew to write `-- -Ab3…`. `share_id`, `invite_id` and `invite_token` now
  also accept `--share-id` / `--invite-id` / `--invite-token`; the `--` separator
  still works, positional parsing is unchanged, and supplying both forms for one
  slot is a validation error rather than a silent winner. Operations whose path
  params declare no `x-octo-flag` keep `cobra.ExactArgs`. A flag-parse failure on
  such a command now carries a hint naming both escapes. An empty flag value is
  refused, so `--share-id "$UNSET"` cannot address the collection URL.
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

### Fixed
- **`--data null` with a promoted body flag crashed the CLI** with
  `panic: assignment to entry in nil map`, on every service domain. JSON `null` decodes
  into a nil map without error, and the promoted-flag merge then wrote into it. The same
  unchecked-successful-decode is fixed in `docs import`, where a `null` response body
  from the backend hit the same write.
- **Documented that `drive blob create` now verifies the object.** octo-drive's
  low-level register path used to take "this object already exists" on trust, so
  `blob create --object-path does/not/exist.txt` returned a confirmed row that
  browsed and shared fine and only 404'd on download. The backend now probes
  storage and rejects an unknown key with `invalid_argument`, and rejects a
  `--size` that conflicts with the stored object (`--size 0` for a non-empty
  object included — 0 is a stated count, not an omission). Spec description,
  design doc and skill say so, and all three now note that a register-path row
  carries no persisted download URL — `share download` on one is `not_found`, so
  `upload file` is the command for a shareable blob — and that an inconclusive
  probe (storage unreachable) surfaces as a retryable 500 rather than
  `invalid_argument`.
- **Corrected the `drive` role contract in schema, docs and skill.** The shared
  `Role` description claimed "super_admin and custom cannot be granted through
  member add / invite create", which is wrong for `custom`: octo-drive accepts it
  on `member add` / `member set-role` (it is the lowest rank, below
  `preview_only`) and rejects it only on `invite create`. Grantability differs per
  surface, so the schema, `docs/octo-cli-design.md` and the `octo-drive` skill now
  carry an explicit per-surface matrix instead of one blanket sentence.
  `super_admin` is never grantable — it is bound to the creator at space creation.
- **Corrected `im_channel_type`'s documented consequence.** The schema, design doc
  and skill all said a wrong value composes a different idempotency source key and
  transfers the same attachment twice. octo-drive keys idempotency on (target
  space, `type=blob`, object path), not on `source_key`; the channel type selects
  the route the message is read through, and a wrong-but-accepted value resolves
  the same attachment and replays the same row. The unsupported duplicate-transfer
  claim is removed; the field stays required.
- **uint64 precision in output and `--dry-run`.** The envelope re-parse and the
  dry-run body echo both went through a plain `json.Unmarshal`, so any integer
  above 2^53 was rounded to a `float64` before reaching stdout — a drive file id
  would have been reported as a different number than the one actually sent. Both
  now decode with `json.Decoder.UseNumber`. Affects every domain; `--jq`,
  `--format table` and `--format csv` render the exact literal.
- **octo-drive error codes are now mapped.** octo-drive replies with
  `{"error":"<code>","message":"..."}` — the code is a bare string, not the nested
  object the matters family uses, and the text is under `message` rather than
  `msg` — so `ParseBackendError` matched neither existing layer and fell back to a
  raw status dump. A third parse layer recognises that shape and maps the
  lowercase codes (`permission_denied`, `password_required`, `wrong_password`,
  `share_expired`, `not_found`, `conflict`, `invalid_argument`, `unauthorized`,
  `auth_unavailable`, `internal`) to CLI taxonomy and hints. A lowercase code and
  its uppercase counterpart classify identically — an agent branches on the CLI
  taxonomy and exit code, never on which backend answered — so only the hint
  differs. Free-text `error` values are not promoted to codes, and the uppercase
  mapping plus every unknown-code fallback are unchanged.

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
