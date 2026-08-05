---
name: octo-shared
version: 0.5.0
description: Shared knowledge for using the octo CLI — authentication, unified gateway config, output envelopes, universal flags, error handling, and common patterns. Load before invoking any octo domain skill.
metadata:
  requires:
    bins: ["octo-cli"]
---

# octo-shared — CLI fundamentals for AI Agents

`octo-cli` is a thin REST client that exposes the Octo ecosystem (matters, messaging, groups, threads, files, bot, events, docs, html) as a single binary. Every service command is auto-generated from an embedded OpenAPI registry; output is a JSON envelope designed to be parsed by agents.

> **The `matter` domain is temporarily withheld** while its backend API stabilizes — `octo-cli matter ...` is not registered and the `octo-matter` skill is not listed. Do not emit `matter` commands until it is re-enabled. The examples below use other domains.

## 1. Authentication

Bots authenticate with a bearer token. There is no interactive user login — but besides the two bot tokens (`app_*`, `bf_*`) there is a third kind, a **user API key** (`uk_*`), which carries a real person's identity and is used mainly for `message search`. Two ways to supply any of them:

**Stored profile (recommended).** A human (or provisioning step) logs the token in once; it is encrypted at rest under `~/.octo-cli`, and the raw token never appears in any command line, shell history, or transcript afterward:

```bash
# Operator setup (token read from a hidden prompt, or --with-token < file):
octo-cli auth login --bot-id cli_xxxxxxxx          # robot id you got when creating the bot
echo "$TOKEN" | octo-cli auth login --bot-id cli_xxxxxxxx --with-token   # non-interactive

# A user API key (uk_) is stored the same way — encrypted profile, bot_kind shows
# user_key. Use a friendly --profile name since it has no robot id:
echo "$UK_TOKEN" | octo-cli auth login --profile alice-search --with-token
```

Then, at runtime, select which bot to act as — the agent passes its own **robot id**, which it knows:

```bash
octo-cli --bot-id cli_xxxxxxxx matter list         # or env OCTO_BOT_ID=cli_xxxxxxxx
octo-cli --profile myname matter list              # or by the friendly profile name
```

With exactly one stored profile, the selector is optional. With **two or more, you must pass `--bot-id` or `--profile`** — omitting it is a hard error (the CLI never guesses which identity to use).

**Env token (fallback).** When no profile is stored, the raw token is read from `OCTO_BOT_TOKEN`:

```bash
export OCTO_BOT_TOKEN=app_xxxxxxxxxxxxxxxxxxxx      # App Bot (DM-only)
export OCTO_BOT_TOKEN=bf_xxxxxxxxxxxxxxxxxxxxx       # User Bot (full access)
export OCTO_BOT_TOKEN=uk_xxxxxxxxxxxxxxxxxxxxx       # User API key (real person; message search)
```

> `OCTO_BOT_ID` is a selector (a robot id), not a secret; `OCTO_BOT_TOKEN` is the secret. If `OCTO_BOT_ID` names no stored profile the command fails — it never falls back to silently using `OCTO_BOT_TOKEN` under that id.

Token prefix determines capability — the CLI does NOT enforce this locally (the backend rejects unsupported operations with `FORBIDDEN`), with **one exception**: an `app_*` token running `message search` is rejected locally with a `validation` error before any request.

| Prefix  | Type         | DM msg | Group read | Group write | Thread | Voice | Search |
|---------|--------------|--------|------------|-------------|--------|-------|--------|
| `app_*` | App Bot      | yes    | yes        | **no**      | **no** | **no**| **no** |
| `bf_*`  | User Bot     | yes    | yes        | yes         | yes    | yes   | yes    |
| `uk_*`  | User API key | —      | —          | —           | —      | —     | yes    |

`uk_*` carries a real person's identity and is meaningful mainly for `message search` (routed to `/v1/user/*`); for other domains use a bot token. `bf_*` can also search on behalf of a person with `--on-behalf-of <uid>`. `identity.bot_kind` reflects the kind: `app_bot`, `user_bot`, or `user_key`.

Before acting, inspect `octo-cli auth status` (or `octo-cli config show`) to confirm the active identity. Every success envelope also echoes it under `identity` (see §3).

## 2. Unified gateway configuration

All Octo domains use one gateway. Override it only for test or self-hosted
deployments:

```bash
# Production defaults to https://im.deepminer.com.cn. Override for test or
# self-hosted deployments:
export OCTO_API_BASE_URL=https://im-test.deepminer.com.cn

export OCTO_SPACE_ID=space_xxx                     # only for platform-scoped bots
export OCTO_FORMAT=json                            # default output format
```

Routing: all services go through `OCTO_API_BASE_URL`. The `--service` flag on `octo-cli api` is for documentation only — all traffic routes to the same gateway.

## 3. Output: the JSON envelope

Every successful invocation prints a single JSON object to stdout:

```json
{
  "ok": true,
  "identity": { "type": "bot", "profile": "prod", "robot_id": "cli_xxx", "bot_kind": "app_bot", "source": "profile:prod" },
  "data": { ... or [...] },
  "_pagination": { "has_more": true, "next_cursor": "..." },
  "_rate_limit": { "remaining": 99, "reset": 1730000000 }
}
```

`identity` echoes the bot the command actually ran as — check it to catch acting as the wrong identity. It is **always an object**: a stored profile fills in `profile` / `robot_id` / `source: "profile:<name>"`; a raw `OCTO_BOT_TOKEN` yields `{ "type": "bot", "bot_kind": ..., "source": "env:OCTO_BOT_TOKEN" }` (no `profile`/`robot_id`); a command that resolves no credential (e.g. `version`) yields the minimal `{ "type": "bot" }`.

Every failure prints an error envelope to **stderr** and exits non-zero:

```json
{
  "ok": false,
  "error": {
    "type": "validation",
    "code": "VALIDATION_ERROR",
    "message": "title is required",
    "hint": "check params with `octo-cli schema <op>`",
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
| `--bot-id`      | Select/assert the stored credential by robot id (env `OCTO_BOT_ID`)     |
| `--profile`     | Select the stored credential by profile name                            |

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

The `hint` field is a one-line next action meant for an agent: follow it literally where it applies. E.g. `MATTER_NOT_FOUND` → "verify ID with `octo-cli matters list`".

## 6. Input patterns

### Promoted flags vs `--data`

Simple top-level body fields auto-promote to typed flags (strings, integers, booleans, `[]string`). For objects, arrays-of-objects, or when sending a large payload, use `--data`:

```bash
octo-cli thread create group-abc --name "design review"
octo-cli message send --data '{"channel_id":"chat-1","channel_type":1,"payload":{"type":1,"content":"hi"}}'
octo-cli message send --data @body.json
octo-cli some-cmd --data @-            # read JSON from stdin
```

Explicit flags override fields set in `--data`. The `--data` escape hatch exists on every non-multipart command.

### Piping with `--jq`

```bash
octo-cli group list --jq '.data[].id' | xargs -I{} octo-cli group get {}
```

### Paginating

```bash
octo-cli docs search --keyword "spec" --page-all --page-limit 20
```

`--page-all` applies to any list operation that reports a cursor in `_pagination`. The merged output drops `_pagination` — you get a flat `data` array.

### Dry-run for agent self-verification

```bash
octo-cli message send --data '{"channel_id":"chat-1","channel_type":1,"payload":{"type":1,"content":"foo"}}' --dry-run
```

Prints the exact HTTP request body and URL, emits no side effect.

## 7. Discovering the API

The registry is embedded in the binary — no network needed:

```bash
octo-cli schema --list                # all services + operation IDs
octo-cli schema --list message        # operations in one domain
octo-cli schema message.send          # full request/response schema
octo-cli config show                  # resolved config (token masked)
octo-cli auth status                  # active bot identity (whoami)
octo-cli auth list                    # stored profiles (no tokens)
```

When an operation isn't auto-registered yet or you need low-level control:

```bash
octo-cli api GET  /api/v1/messages --params '{"chat_id":"chat-1"}'
octo-cli api POST /api/v1/messages --data @body.json
```

## 8. Domain skills

Once these fundamentals are understood, load the skill for the domain you need:

- `octo-matter` — matters (todos/tasks), assignees, channels, timeline, AI extract — **temporarily withheld** (backend API stabilizing; not currently loadable)
- `octo-messaging` — message send/edit/sync/read-receipt, groups, threads, events
- `octo-files` — file upload/download, presigned credentials, bot housekeeping
- `octo-docs` — docs domain (CRDT/Yjs): documents, spreadsheets, whiteboard scenes, members/sharing, comments, versions, attachments
- `octo-html` — HTML docs domain (octo-doc, a DIFFERENT backend from octo-docs): self-contained interactive HTML documents, share codes, media assets, comments, agent element read/replace
- `octo-summary` — create owner-only summaries from explicit sources, then discover, read, and cite summaries visible to the personal Agent's human owner — **temporarily withheld** (create backend at Mininglamp-OSS/octo-smart-summary#181 not yet merged/deployed/enabled; not currently loadable)
