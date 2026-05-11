---
name: octo-shared
version: 0.4.0
description: Shared knowledge for using the octo CLI — authentication, multi-service config, output envelopes, universal flags, error handling, and common patterns. Load before invoking any octo domain skill.
metadata:
  requires:
    bins: ["octo"]
---

# octo-shared — CLI fundamentals for AI Agents

`octo` is a thin REST client that exposes the Octo ecosystem (matters, messaging, groups, threads, files, bot, events) as a single binary. Every service command is auto-generated from an embedded OpenAPI registry; output is a JSON envelope designed to be parsed by agents.

## 1. Authentication

Bots authenticate with a bearer token passed as an env var. There is no user login.

```bash
export OCTO_BOT_TOKEN=app_xxxxxxxxxxxxxxxxxxxx      # App Bot (DM-only)
# or
export OCTO_BOT_TOKEN=bf_xxxxxxxxxxxxxxxxxxxxx       # User Bot (full access)
```

Token prefix determines capability — the CLI does NOT enforce this locally; the backend rejects unsupported operations with `FORBIDDEN`.

| Prefix  | Type     | DM msg | Group read | Group write | Thread | Voice |
|---------|----------|--------|------------|-------------|--------|-------|
| `app_*` | App Bot  | yes    | yes        | **no**      | **no** | **no**|
| `bf_*`  | User Bot | yes    | yes        | yes         | yes    | yes   |

Before acting, inspect `octo config show` to confirm the token in use.

## 2. Multi-service configuration

Different Octo services run at different URLs. Set whichever you need:

```bash
export OCTO_API_BASE_URL=https://api.example.com   # unified API base URL for all services

export OCTO_SPACE_ID=space_xxx                     # only for platform-scoped bots
export OCTO_FORMAT=json                            # default output format
```

Routing: all services go through `OCTO_API_BASE_URL`. The `--service` flag on `octo api` is for documentation only — all traffic routes to the same gateway.

## 3. Output: the JSON envelope

Every successful invocation prints a single JSON object to stdout:

```json
{
  "ok": true,
  "identity": "bot",
  "data": { ... or [...] },
  "_pagination": { "has_more": true, "next_cursor": "..." },
  "_rate_limit": { "remaining": 99, "reset": 1730000000 }
}
```

Every failure prints an error envelope to **stderr** and exits non-zero:

```json
{
  "ok": false,
  "error": {
    "type": "validation",
    "code": "VALIDATION_ERROR",
    "message": "title is required",
    "hint": "check params with `octo schema <op>`",
    "detail": { ...original backend payload... }
  }
}
```

Parse `ok` first. On failure, branch on `error.type` (a small fixed taxonomy) or `error.code` (a string, may come straight from the backend).

Backends differ in their raw error shape. The CLI normalizes both:
- **matters** (structured): `{error:{code, message, details}}` → passes through into `detail` unchanged.
- **dmworkim** (flat): `{msg, status}` → mapped to `code`/`message` via HTTP status.

## 4. Universal flags

These flags work on every command (they are root-level persistent flags):

| Flag            | Purpose                                                                 |
|-----------------|-------------------------------------------------------------------------|
| `--format`      | `json` (default) · `table` · `csv` · `ndjson`                           |
| `--jq`, `-q`    | Apply a jq expression to the success envelope before formatting         |
| `--dry-run`     | Print the resolved request instead of sending it                        |
| `--verbose`     | Log request/response trace to stderr                                    |
| `--timeout`     | Per-request deadline, e.g. `30s`, `2m`                                  |
| `--no-retry`    | Disable the default retry-on-transient policy                           |
| `--space`       | Override `OCTO_SPACE_ID` for this invocation                            |

Paginated operations additionally expose:

| Flag           | Purpose                                                  |
|----------------|----------------------------------------------------------|
| `--page-all`   | Walk pages until `has_more=false`, emit one merged array |
| `--page-limit` | Hard cap on pages fetched with `--page-all` (default 10) |

## 5. Error taxonomy and exit codes

| `error.type`   | Exit | Typical `error.code`                         |
|----------------|------|----------------------------------------------|
| `auth_error`   | 3    | `UNAUTHORIZED`, `AUTH_UNAVAILABLE`           |
| `validation`   | 2    | `VALIDATION_ERROR`, `PAYLOAD_TOO_LARGE`      |
| `config`       | 2    | missing env vars                             |
| `permission`   | 1    | `FORBIDDEN`, `SPACE_FORBIDDEN`               |
| `rate_limited` | 1    | `RATE_LIMITED`                               |
| `network`      | 1    | `NETWORK_ERROR`, `UPSTREAM_UNAVAILABLE`      |
| `api_error`    | 1    | `MATTER_NOT_FOUND`, `NOT_FOUND`, `INTERNAL_ERROR` |
| `internal`     | 1    | CLI-side bug                                 |

Agents should switch on **`error.code` first** (specific, deterministic), then `error.type` (broad), then `exit_code` (coarse).

The `hint` field is a one-line next action meant for an agent: follow it literally where it applies. E.g. `MATTER_NOT_FOUND` → "verify ID with `octo matters list`".

## 6. Input patterns

### Promoted flags vs `--data`

Simple top-level body fields auto-promote to typed flags (strings, integers, booleans, `[]string`). For objects, arrays-of-objects, or when sending a large payload, use `--data`:

```bash
octo matter create --title "Fix login" --assignee me --assignee alice
octo matter create --data '{"title":"Fix login","assignees":["me","alice"]}'
octo matter create --data @body.json
octo some-cmd --data @-            # read JSON from stdin
```

Explicit flags override fields set in `--data`. The `--data` escape hatch exists on every non-multipart command.

### Piping with `--jq`

```bash
octo matter list --status open --jq '.data[].id' | xargs -I{} octo matter get {}
```

### Paginating

```bash
octo matter list --status open --page-all --page-limit 20
```

The merged output drops `_pagination` — you get a flat `data` array.

### Dry-run for agent self-verification

```bash
octo matter create --title foo --dry-run
```

Prints the exact HTTP request body and URL, emits no side effect.

## 7. Discovering the API

The registry is embedded in the binary — no network needed:

```bash
octo schema --list                # all services + operation IDs
octo schema --list matter         # operations in one domain
octo schema matter.create         # full request/response schema
octo config show                  # resolved config (token masked)
```

When an operation isn't auto-registered yet or you need low-level control:

```bash
octo api GET  /api/v1/matters --params '{"status":"open"}'
octo api POST /api/v1/matters --data @body.json
```

## 8. Domain skills

Once these fundamentals are understood, load the skill for the domain you need:

- `octo-matter` — matters (todos/tasks), assignees, channels, timeline, AI extract
- `octo-messaging` — message send/edit/sync/read-receipt, groups, threads, events
- `octo-files` — file upload/download, presigned credentials, bot housekeeping
