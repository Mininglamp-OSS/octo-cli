# Octo CLI — Architecture Design Document

> Version: 3.1 (Final)
> Author: 陈皮皮 (PM)
> Reviewer: 齐静春 (Architect)
> Date: 2026-05-10
> Status: Post R4 review — ready for approval

---

## 1. Executive Summary

Octo CLI is the command-line interface for the Octo ecosystem. Its **primary consumers are AI Agent Bots**. Every architectural decision is evaluated through this lens: structured output, deterministic error handling, and machine-parseable responses take priority over interactive UX.

### Design Principles

1. **Agent-first**: Every error message will be parsed by an AI to decide its next action
2. **Non-interactive**: CLI never reads stdin for prompts or confirmations. All decisions via flags
3. **Metadata-driven**: OpenAPI specs generate commands; hand-written code only for multi-step workflows
4. **Thin client**: All business logic lives server-side; CLI is transport + formatting
5. **Bot-only identity**: Authenticates as App Bot (app_ token) via dmworkim; no user-auth complexity
6. **Zero-surprise output**: stdout is structured data, stderr is everything else

### Key Decisions

| Decision | Rationale |
|----------|-----------|
| OpenAPI 3.x as spec format | Backend can produce OpenAPI; custom format = double work |
| Body top-level fields auto-promote to flags | Agent builds `--title x` more reliably than `--data '{...}'` |
| Bot-only identity (App Bot) | Primary consumer is Agent Bot; eliminates user-auth complexity |
| Factory DI over globals | Testability + extensibility |
| JSON envelope output | Agent parses structured errors to decide next action |
| Single command syntax | No dual syntax coexistence; avoids Agent confusion |
| Minimal dependencies | stdlib for HTTP/JSON/test; only add deps with clear ROI |
| No interactive I/O | No prompts, no confirmations — flags only |

---

## 2. Backend Reality (from code)

Findings from reading dmworkim develop + todos (matters) main — constraints the CLI architecture must respect.

### 2.1 Service Discovery

The Octo ecosystem is a constellation of services exposed through one gateway:

| Service | Gateway path | Module | Current State |
|---------|--------------|--------|---------------|
| **Matters** (todo/task) | `/api/v1/matters` | `github.com/dmwork-org/matters` | 9.8K LOC, production |
| **Bot API** (messaging) | `/v1/bot/*` | `dmworkim/modules/bot_api` | Production |
| **App Bot** (bot mgmt) | `/v1/admin/app_bot` | `dmworkim/modules/app_bot` | Production |
| Future services | TBD | TBD | — |

**Architectural implication**: Octo CLI uses one `OCTO_API_BASE_URL`.
Each service domain owns a module-qualified path in its embedded OpenAPI spec.

### 2.2 Authentication Model

From dmworkim `modules/bot_api/auth.go` and matters `internal/auth/middleware.go`:

**Two token families** (routed by prefix):
- `bf_*` — User Bot (legacy, robot table)
- `app_*` — App Bot (app_bot table, scope: platform or space)

**CLI targets App Bot only** (app_ tokens). Auth flow:
```
CLI → Authorization: Bearer app_xxxx → Backend
Backend → dmworkim POST /v1/auth/verify-bot → {bot_uid, owner_uid, space_id}
```

**Space context**:
- App Bot with `scope=space`: space_id is resolved from bot registration (server-side)
- App Bot with `scope=platform`: requires `X-Space-Id` header per request
- Matters service always requires space context (either from bot or header)

**CLI implication**: credential provider must supply both `token` and `space_id`. For space-scoped bots, space_id is automatic. For platform-scoped bots, CLI needs `--space` flag or config.

### 2.3 Matters API Shape (actual)

From `internal/handler/router.go` — the real routes (NOT what current octo-cli implements):

```
POST   /api/v1/matters                          # Create matter
GET    /api/v1/matters                           # List matters (cursor pagination)
POST   /api/v1/matters/extract                   # AI: extract matter from chat messages
GET    /api/v1/matters/:id                       # Get matter detail
PUT    /api/v1/matters/:id                       # Update matter
PUT    /api/v1/matters/:id/status                # Transition status
DELETE /api/v1/matters/:id                        # Soft delete
POST   /api/v1/matters/:id/assignees             # Add assignee
DELETE /api/v1/matters/:id/assignees/:uid         # Remove assignee
POST   /api/v1/matters/:id/channels              # Link channel
DELETE /api/v1/matters/:id/channels/:channel_id   # Unlink channel
POST   /api/v1/matters/:id/timeline              # Add timeline entry
GET    /api/v1/matters/:id/timeline              # List timeline entries
DELETE /api/v1/matters/:id/timeline/:entry_id     # Delete timeline entry
```

**Key differences from current octo-cli**:
- Naming: **"matters"** not "todos" (Go module is `github.com/dmwork-org/matters`)
- Status values: **open / done / archived** (not open/closed)
- No goals API (goals were removed; matters is flat)
- New resources: **timeline** (replaces comments), **channels**, **extract** (LLM)
- Pagination: cursor-based with `{"data": [...], "pagination": {"has_more": bool, "next_cursor": "..."}}`

### 2.4 Backend Error Format

From `internal/handler/resp.go`:

```json
{"error": {"code": "VALIDATION_ERROR", "message": "...", "details": {"field": "title"}}}
{"error": {"code": "MATTER_NOT_FOUND", "message": "matter not found"}}
{"error": {"code": "FORBIDDEN", "message": "forbidden"}}
{"error": {"code": "UNAUTHORIZED", "message": "invalid bot token"}}
{"error": {"code": "AUTH_UNAVAILABLE", "message": "failed to reach auth service"}}
```

Error codes are **string constants**, not integers. CLI envelope must map these to its own taxonomy while preserving the original in `detail`.

### 2.5 Bot Identity & Visibility

From `internal/handler/resp.go` — `effectiveCallerUIDs()`:
- Bot gets write power equivalent to its owner
- `related_uids` = [bot_uid] for visibility queries
- Bot acting on behalf of owner: `actor_uid == owner_uid` (LLM path)

CLI doesn't need to know these details, but error messages about "forbidden" may relate to bot lacking owner-equivalent permissions — hint should reflect this.

---

## 3. Architecture Overview

### 3.1 Dependency Graph

```
┌────────────────────────────────────────┐
│          Command Layer                 │
│                                        │
│  ┌──────────────┐  ┌───────────────┐   │
│  │ Service Cmds │  │ Shortcut Cmds │   │
│  │ (auto-gen)   │  │ (manual,      │   │
│  │              │  │  deferred)    │   │
│  └──────┬───────┘  └───────┬───────┘   │
└─────────┼───────────────────┼──────────┘
          │                   │
          ▼                   ▼
┌────────────────────────────────────────┐
│          Factory (DI Container)        │
│                                        │
│  Lazy-cached providers:                │
│  ├─ Config()                           │
│  ├─ Client()                           │
│  ├─ Credential (Provider Chain)        │
│  ├─ IOStreams (In/Out/ErrOut)          │
│  └─ Registry (spec data)              │
└───┬──────────┬──────────┬─────────────┘
    │          │          │
    ▼          ▼          ▼
┌────────┐ ┌────────┐ ┌────────┐
│client/ │ │cred/   │ │output/ │  ← Leaf packages
│        │ │        │ │        │
│DoAPI() │ │Resolve │ │Envlope │
│Retry   │ │Chain   │ │Format  │
│Paginate│ │Bot-only│ │Errors  │
│Timeout │ │        │ │jq      │
└────────┘ └────────┘ └────────┘
                         ▲
                         │ ZERO internal deps
┌─────────┐
│registry/ │  ← Pure data loader, no business logic
└──────────┘
```

**Package dependency constraints (enforced by go vet + CI):**
- `output/` imports only stdlib — leaf package, any package can depend on it
- `client/` depends on `output/` + `credential/`
- `cmdutil/` (Factory) is the hub — no leaf depends on it
- **No circular dependencies. Ever.**

### 3.2 Unified Gateway Routing

```go
// Config exposes one gateway; OpenAPI paths carry module namespaces.
type Config struct {
    APIBaseURL string // OCTO_API_BASE_URL
}
```

Service commands resolve URLs as `OCTO_API_BASE_URL + operation path`.
Module namespaces such as `/market/api`, `/docs-html`, and `/fleet/api` live
in the embedded OpenAPI paths.

### 3.3 Workspace Context Convention

Fleet accepts more than one workspace selector at its Web compatibility
boundary, but authorization and business logic always operate on the resolved
workspace UUID. Each client surface must use one explicit convention:

| Caller | Workspace input | Rationale |
|--------|-----------------|-----------|
| Octo Web human session | `X-Workspace-Slug: <slug>` | Matches the selected workspace and human-readable deep links; Fleet resolves it to a UUID. |
| CLI and Public API automation | `X-Workspace-ID: <uuid>` | Stable, unambiguous OpenAPI contract that matches resource IDs and credential bindings. |
| `/v1/workspaces/{id}` resources | UUID path parameter | The resource path is the authoritative workspace selector. |
| Credential-bound task execution | Server-verified binding | Fleet replaces or ignores client workspace selection so a task cannot widen its scope. |

The `--workspace-id` flag on `octo-cli` commands therefore accepts a workspace
UUID only. It must never place a slug or display name in `X-Workspace-ID`.
Callers obtain the UUID from `octo-cli loop workspace list` and pass it
explicitly; there is no environment fallback.

Clients should not send both `X-Workspace-Slug` and `X-Workspace-ID`. Fleet's
Web resolver gives slug precedence, so mismatched values are ambiguous and can
select or reject a different workspace than the caller intended. If slug
support is added to the CLI later, it must be a separately named
`--workspace-slug` flag, mutually exclusive with `--workspace-id`, and first be
declared as part of the Public API specification.

---

## 4. Output Protocol

### 4.1 JSON Envelope

```jsonc
// Success
{"ok": true, "data": { ... }, "identity": "bot"}

// Error — wraps backend's error structure into CLI taxonomy
{
  "ok": false,
  "error": {
    "type": "api_error",
    "code": "MATTER_NOT_FOUND",
    "message": "matter not found",
    "hint": "verify the matter ID with `octo-cli matters list`",
    "detail": {"code": "MATTER_NOT_FOUND", "message": "matter not found"}
  }
}
```

### 4.2 Non-Interactive Principle

CLI never reads stdin interactively. There are no prompts and no confirmation
gates — every decision is expressed via flags. Destructive operations
(`high-risk-write`) execute immediately; callers are expected to validate
before invoking.

### 4.3 Error Mapping (Backend → CLI)

| Backend Code | HTTP | CLI Type | CLI Hint |
|-------------|------|----------|----------|
| `UNAUTHORIZED` | 401 | `auth_error` | "check OCTO_TOKEN / OCTO_BOT_TOKEN; bot may be unpublished" |
| `AUTH_UNAVAILABLE` | 503 | `network` | "auth service unreachable; retry later" |
| `VALIDATION_ERROR` | 400 | `validation` | "check params with `octo-cli schema <op>`" |
| `MATTER_NOT_FOUND` | 404 | `api_error` | "verify ID with `octo-cli matters list`" |
| `NOT_FOUND` | 404 | `api_error` | "resource not found" |
| `ASSIGNEE_NOT_FOUND` | 404 | `api_error` | "assignee not in space or invalid UID" |
| `FORBIDDEN` | 403 | `permission` | "bot lacks permission; check space membership" |
| `BOT_WORKSPACE_MEMBERSHIP_REQUIRED` | 403 | `permission` | "ask a Workspace owner or admin to add this Bot in Workspace Members" |
| `SPACE_FORBIDDEN` | 403 | `permission` | "bot not a member of this space" |
| `DUPLICATE_ASSIGNEE` | 409 | `validation` | "already assigned; check current assignees" |
| `RATE_LIMITED` | 429 | `rate_limited` | "server-side rate limit; retry after cooldown" |
| `UPSTREAM_UNAVAILABLE` | 503 | `network` | "upstream dependency unavailable; retry later" |
| `INTERNAL_ERROR` | 500 | `api_error` | "internal server error; retry or report" |
| `PAYLOAD_TOO_LARGE` | 413 | `validation` | "request body exceeds 1MB limit" |

### 4.4 Pagination Envelope Flattening

Backend paginated responses use: `{"data": [...], "pagination": {"has_more": true, "next_cursor": "xxx"}}`

Naive wrapping produces `data.data` nesting. CLI flattens:

```jsonc
// Single-page: flatten data+pagination into envelope
{"ok": true, "data": [...], "_pagination": {"has_more": true, "next_cursor": "xxx"}}

// --page-all: all pages merged, no _pagination (engine exhausted all pages)
{"ok": true, "data": [/* all items */]}

// Non-paginated response: pass through as-is
{"ok": true, "data": { ... }}
```

Detection: backend response is object with `data` (array) + `pagination` (object) keys → flatten. Otherwise pass through. `_pagination` uses underscore prefix consistent with `_rate_limit` and `_notice`.

### 4.5 Rate Limit Metadata

```jsonc
{"ok": true, "data": [...], "_rate_limit": {"remaining": 47, "limit": 100, "reset_at": "..."}}
```

### 4.6 Notice Channel

```jsonc
{"ok": true, "data": {...}, "_notice": {"update": {"current": "0.3.0", "latest": "0.4.0"}}}
```

### 4.7 Envelope Migration

Current v0.x outputs raw server JSON. Break immediately — only internal Agent consumers exist. One-shot migration, no compat layer needed.

---

## 5. Metadata Engine

### 5.1 Spec Format

**OpenAPI 3.x** with `x-octo-*` extensions:

| Extension | Purpose | Example |
|-----------|---------|---------|
| `x-octo-service` | CLI domain name | `"matters"` |
| `x-octo-base-url` | Gateway URL env var | `"OCTO_API_BASE_URL"` |
| `x-octo-risk` | Operation risk | `"read"` / `"write"` / `"high-risk-write"` |
| `x-octo-pagination` | Cursor config | `{"cursorParam":"cursor","cursorField":"pagination.next_cursor"}` |
| `x-octo-cli-name` | Override command name | `"close"` (for transition endpoint) |
| `x-octo-positional` | Body fields → positional args | `["content"]` |
| `x-octo-space-header` | Inject X-Space-Id | `true` (default for all service commands) |
| `x-octo-status-values` | Valid status enum for domain | `["open","done","archived"]` |

**Response schemas required** — Agent needs field names for --jq and decision-making.

### 5.2 operationId → Command Mapping Rules

| Rule | Mechanism |
|------|-----------|
| 1. Service from `x-octo-service` | Top-level subcommand |
| 2. Verb from operationId suffix | Subcommand |
| 3. Path params → positional args | Max 2; 3rd+ becomes named flag |
| 4. Query params → flags | Auto-registered from spec |
| 5a. Body top-level flat fields → individual flags | string/int/bool/string-array auto-promoted |
| 5b. Complex/nested body → --data | JSON blob for objects, arrays of objects |
| 6. Sub-resource via dot in operationId | Max depth 2 |
| 7. Override via x-octo-cli-name | Escape hatch; >20% overrides → revisit rules |

**Body field auto-promotion (Rule 5a):**
- When individual flags AND `--data` both provided, flags take precedence (merge into data)
- Makes `octo-cli matters create --title "Deploy" --assignee user-1` work from engine alone

### 5.3 Spec Versioning

| Scenario | Behavior |
|----------|----------|
| Cached minor > embedded | Overlay (additive) |
| Cached major > embedded | Warn in `_notice` |
| Unknown x-octo-* extension | Ignore (forward-compatible) |

### 5.4 OpenAPI Parsing

**Runtime**: Minimal — `map[string]any` via stdlib JSON. No heavy deps.
**Build/CI**: kin-openapi in test code only validates spec legality. Not shipped in binary.

### 5.5 Response Schema Tolerance

Postel's Law: strict in sending, liberal in receiving.
- CLI never rejects responses for schema mismatch
- Unknown fields passed through; missing fields → zero-values
- `--verbose` warns about mismatches on stderr

---

## 6. API Client

### 6.1 Unified Gateway Client

```go
type APIClient struct {
    APIBaseURL string             // unified gateway
    HTTP       *http.Client
    Credential *credential.CredentialProvider
    Retry      RetryConfig
    SpaceID    string            // injected from credential or --space flag
    ErrOut     io.Writer
}

func (c *APIClient) DoAPI(ctx context.Context, service string, req Request) (json.RawMessage, error)
```

The operation path selects the module behind the gateway. X-Space-Id is
injected automatically when x-octo-space-header is set.

### 6.2 Retry & Timeout

| Config | Default | Override |
|--------|---------|---------|
| Max retries | 3 | `--no-retry` |
| Base delay | 500ms | — |
| Max delay | 10s | — |
| Request timeout | 30s | `--timeout <duration>` |
| Retryable codes | 429, 502, 503, 504 | — |
| Retry-After | Respected, NOT capped by max delay | — |

### 6.3 Input Resolution

`--data` and `--params` accept: inline JSON, `@filepath`, `@-` (stdin).

---

## 7. Credential System

### 7.1 Bot-Only (App Bot)

```go
type BotCredential struct {
    Token    string  // app_xxxx (App Bot token)
    SpaceID  string  // resolved from bot registration or --space flag
    Source   string  // "env:OCTO_TOKEN" / "env:OCTO_BOT_TOKEN" / "config:prod"
}
```

### 7.2 Provider Chain

Phase 1: `Environment (OCTO_TOKEN → OCTO_BOT_TOKEN) → Error`
Phase 4a: `Environment → Config File → Error`
Future: `Environment → Config → Sidecar → Error`

### 7.3 Space Resolution

| Bot Scope | Space Source | CLI Behavior |
|-----------|-------------|-------------|
| space | Server resolves from bot registration | No --space needed |
| platform | Must be provided | `--space <id>` flag or `OCTO_SPACE_ID` env or config |

---

## 8. Shortcut Layer (Deferred)

Shortcuts = hand-written commands that compose multiple API calls or fix parameters into semantic aliases. With body-field auto-promotion (Rule 5a), most simple CRUD is ergonomic from the engine.

Needed for:
- Semantic aliases (e.g. `close` = transition + status:done)
- Multi-step workflows (extract → create with LLM)
- Cross-domain operations

**Deferred** until second domain proves the need. At minimum, Phase 2b needs a simple alias mechanism for status transitions.

### 8.1 Matters Domain — Key Backend Behaviors

Discovered from backend code, relevant to CLI alias design and Skill documentation:

- **Status transitions are unconstrained** — any valid status (open/done/archived) can transition to any other. No state machine. `close` (→done), `reopen` (→open), `archive` (→archived) are all equivalent transition calls with a fixed status value. CLI does NOT do status pre-checks.
- **`assignee_id=me` alias** — List endpoint accepts `"me"` as assignee_id, server replaces with caller UID. High-frequency Agent pattern; must be in spec and Skill.
- **extract auth constraint** — Bot path requires `creator_uid == bot's owner_uid`. Bot cannot create matters on behalf of arbitrary users. Must be in Skill.
- **attachments = timeline sub-resource** — No independent attachment endpoints. Attachments are part of timeline entry creation body.
- **goals removed** — Backend has no goals API. Current octo-cli goal commands (7 total) must be deleted in Phase 2b migration.

---

## 9. Universal Flags

| Flag | Purpose |
|------|---------|
| `--format <json\|table\|csv\|ndjson>` | Output format |
| `--jq <expr>` / `-q <expr>` | Filter output |
| `--dry-run` | Print request without executing |
| `--verbose` | Request/response trace to stderr |
| `--timeout <duration>` | Per-request timeout |
| `--no-retry` | Disable retry |
| `--page-all` | Auto-paginate |
| `--page-limit <n>` | Max pages (default: 10) |
| `--space <id>` | Space context (when bot is platform-scoped) |
| `--profile <name>` | Credential profile (Phase 4a) |

`octo-cli schema`:
- `--list` — list all operations (optionally filtered by domain)

---

## 10. `octo-cli api` Generic Command

Passthrough for API calls not in specs:
```bash
octo-cli api GET /api/v1/matters --params '{"status":"open"}'
octo-cli api GET /fleet/api/v1/custom --workspace-id <workspace-uuid>
```
Uses credential provider, outputs envelope, supports universal flags, and can
explicitly send workspace context as `X-Workspace-ID` with `--workspace-id`.
No spec consultation, no flag auto-gen, no --page-all.

---

## 11. Testing Strategy

### Three Layers

| Layer | What | How |
|-------|------|-----|
| **Unit** | Functions, error mapping, formatting | Table-driven, TestFactory |
| **Spec-driven integration** | Full CRUD per domain | OpenAPI spec + httptest mock → auto-generated test matrix |
| **E2E** | Real API round-trips | Self-contained, skipped without token |

### Spec-Driven Tests (Phase 2b)

Per operation auto-generate: valid request → 200, missing required → 400, bad auth → 401, --dry-run, --page-all.

Test data: request bodies from spec required fields + types (automated). Mock responses: schema-based generator + hand-written edge cases. Business logic boundaries: hand-written alongside e2e.

---

## 12. Implementation Phases

### Phase 1: Foundation (Week 1-2)

| # | Task | Effort |
|---|------|--------|
| 1.1 | ExitError + JSON envelope + rate-limit + backend error mapping | 1d |
| 1.2 | Factory DI + IOStreams | 1d |
| 1.3 | Migrate existing commands to Factory | 1d |
| 1.4 | context.Context + retry with backoff + Retry-After | 1d |
| 1.5 | --dry-run, --verbose, --timeout, --no-retry | 1d |
| 1.6 | --jq filter (gojq) | 0.5d |
| 1.7 | stdin input (@- / @file) + --space flag | 0.5d |
| 1.8 | Unified gateway and module-qualified path routing | 0.5d |
| 1.9 | Tests on TestFactory | 0.5d |

**Exit**: Existing commands work. Envelope output. Universal flags functional. Error mapping matches backend codes.

### Phase 2a: Spec Loader + Schema (Week 3)

| # | Task | Effort |
|---|------|--------|
| 2a.1 | Write matters spec in OpenAPI 3.x (with response schemas, from actual router.go) | 1.5d |
| 2a.2 | Minimal spec loader (JSON parse) | 1d |
| 2a.3 | `octo-cli schema <operation>` + `octo-cli schema --list` | 1d |
| 2a.4 | Spec version tracking + CI validation (kin-openapi test-only) | 0.5d |

**Exit**: `octo-cli schema --list` works. Spec matches actual backend routes. Go/no-go for 2b.

### Phase 2b: Auto-Registration Engine (Week 4-6)

~12 days, budget 2.5 weeks for edge cases.

| # | Task | Effort |
|---|------|--------|
| 2b.1 | operationId → command mapping engine | 2.5d |
| 2b.2 | Flag auto-registration (query→flags, body top-level→flags, path→positional) | 3d |
| 2b.3 | Pagination engine (--page-all, cursor-based per backend format) | 1d |
| 2b.4 | `octo-cli api` generic command | 0.5d |
| 2b.5 | Spec-driven test generator + mock response generator | 2.5d |
| 2b.6 | Migrate matters domain, delete all hand-written code (incl. 7 goal commands, 3 attachment commands) | 1d |
| 2b.7 | Minimal alias mechanism for status transitions (close/reopen/archive) | 0.5d |

**Exit**: Zero hand-written command code for matters. Spec-driven tests pass.

### Phase 3: Skills Documentation (Week 7)

| # | Task | Effort |
|---|------|--------|
| 3.1 | octo-shared skill (auth, errors, universal flags, envelope) | 0.5d |
| 3.2 | Matters domain skill (workflows, examples, error recovery) | 1d |
| 3.3 | Skill packaging | 0.5d |

**Exit**: All Skill examples match `octo-cli schema --list` output exactly.

### Phase 4a: Multi-Profile + Credential (Week 8)

| # | Task | Effort |
|---|------|--------|
| 4a.1 | Config file format (per-profile: token + gateway URL + space_id) | 1d |
| 4a.2 | `octo-cli config init` + `octo-cli config show` | 1d |
| 4a.3 | Config file credential provider + --profile | 1d |

### Phase 4b: Toolchain (Week 9)

| # | Task | Effort |
|---|------|--------|
| 4b.1 | Shell completion | 0.5d |
| 4b.2 | Self-update | 0.5d |
| 4b.3 | Installer improvements | 0.5d |

---

## 13. Dependencies

| Package | Purpose | Phase | Notes |
|---------|---------|-------|-------|
| `github.com/spf13/cobra` | CLI framework | existing | — |
| `github.com/itchyny/gojq` | jq filter | Phase 1 | Core Agent feature |
| `github.com/getkin/kin-openapi` | Spec validation | Phase 2a | **Test-only, NOT in binary** |

---

## 14. Open Questions

| # | Question | Decide By | Leaning |
|---|----------|-----------|---------|
| 1 | Can backend export OpenAPI? | Phase 2b | Hand-write first from router.go |
| 2 | Remote spec hot-update? | Post Phase 4 | Re-release is fine at current scale |
| 3 | Sidecar auth for sandboxed agents? | When daemon ships | Design slot exists |
| 4 | Should `octo-cli matters` be renamed to `octo todo` for UX? | Phase 2a | Match backend naming; alias if needed |
| 5 | Bot API (messaging) as second domain for validation? | Phase 2b acceptance | Good candidate — different service URL |
