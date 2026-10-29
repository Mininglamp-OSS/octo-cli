---
name: octo-shared
version: 0.5.0
description: Shared knowledge for using the octo CLI — authentication, unified gateway config, output envelopes, universal flags, error handling, and common patterns. Load before invoking any octo domain skill.
metadata:
  requires:
    bins: ["octo-cli"]
---

# octo-shared — CLI fundamentals for AI Agents

`octo-cli` exposes the Octo ecosystem through active service commands, including messaging, groups, threads, files, bot, events, Drive, docs, HTML, Marketplace and Loop. Every service command is generated from an embedded OpenAPI registry; output is a JSON envelope by default and is designed for agents.

> **The `matter` domain is temporarily withheld** while its backend API stabilizes — `octo-cli matter ...` is not registered and the `octo-matter` skill is not listed. Do not emit `matter` commands until it is re-enabled. The examples below use other domains.

**Bots cannot delete documents, even documents they created or own/administer.**
Do not invoke `docs delete`, `html rm`, or equivalent raw `api DELETE` calls to
delete a document. Ask a human with document admin permission to delete it in
Octo instead. Do not switch credentials/routes or clear the document's contents
as a workaround. This rule concerns whole documents, not normal editing or
otherwise-permitted comment, version, asset, or sheet row/column removal.

## Stored-secret download links over MCP

For a stored alias or ID, use `get_secret_download_url` and follow
[the private client-file workflow](secret-download.md). Download directly to
an exclusive private file; never fetch it as text, open/read/cat it into model
context, or echo it from scripts. The temporary URL is sensitive too. This
feature requires private shared Redis and an explicitly routed public HTTPS
MCP download service, including for stdio publishers. Rotation does not revoke
issued snapshots. The linked reference covers configuration, bounds, retention
and client-history limits.

## 1. Authentication

Bots authenticate with a bearer token. There is no interactive user login — but besides the two bot tokens (`app_*`, `bf_*`) there is a third kind, a **user API key** (`uk_*`), which carries a real person's identity and is used for `message search` and the whole `drive` domain. Two ways to supply any of them:

**Stored profile (recommended).** A human (or provisioning step) logs the token in once; it is encrypted at rest under `~/.octo-cli`, and the raw token never appears in any command line, shell history, or transcript afterward:

```bash
# Operator setup (token read from a hidden prompt, or --with-token < file):
octo-cli auth login --bot-id cli_xxxxxxxx          # robot id you got when creating the bot
echo "$TOKEN" | octo-cli auth login --bot-id cli_xxxxxxxx --with-token   # non-interactive

# A user API key (uk_) is stored the same way — encrypted profile, bot_kind shows
# user_key. Give it a friendly profile name for message search and Drive:
echo "$UK_TOKEN" | octo-cli auth login --profile alice-search-drive --with-token
```

Then, at runtime, select which bot to act as — the agent passes its own **robot id**, which it knows:

```bash
octo-cli --bot-id cli_xxxxxxxx group list         # or env OCTO_BOT_ID=cli_xxxxxxxx
octo-cli --profile myname group list              # or by the friendly profile name
```

With exactly one stored profile, the selector is optional. With **two or more, you must pass `--bot-id` or `--profile`** — omitting it is a hard error (the CLI never guesses which identity to use).

**Environment credential.** Outside an explicit `--bot-id` / `--profile`
selection, an environment credential overrides stored profiles. `OCTO_TOKEN`
is canonical; `OCTO_BOT_TOKEN` is a fully compatible lower-priority alias:

```bash
export OCTO_TOKEN=app_xxxxxxxxxxxxxxxxxxxx      # App Bot
export OCTO_TOKEN=bf_xxxxxxxxxxxxxxxxxxxxx       # User Bot
export OCTO_TOKEN=uk_xxxxxxxxxxxxxxxxxxxxx       # User API key (real person; message search and Drive)
```

> `OCTO_BOT_ID` is not a secret. Without an environment credential it selects a
> stored profile; an environment credential takes precedence over that implicit
> selector.

The registry enforces per-domain token-kind gates where a domain declares them. An incompatible credential is refused locally before HTTP with `TOKEN_KIND_NOT_ALLOWED` (`validation`, exit 2). `message search` also rejects `app_*` locally. Drive accepts App Bot, User Bot and user API key credentials:

| Prefix  | Type         | DM msg | Group read | Group write | Thread | Voice | Search | Drive |
|---------|--------------|--------|------------|-------------|--------|-------|--------|-------|
| `app_*` | App Bot      | yes    | yes        | only `group md-update` when member and `bot_admin` | **no** | **no** | **no** | yes |
| `bf_*`  | User Bot     | yes    | yes        | yes         | yes    | yes   | yes    | yes   |
| `uk_*`  | User API key | —      | —          | —           | —      | —     | yes    | yes   |

App Bot group-read access does not imply general group-write access: `group md-update` is the sole exception, and only when the bot is a group member with `bot_admin`. `uk_*` represents a real person and is routed for `message search` to `/v1/user/*`; Drive user-key requests route to `/v1/user/drive/*`, while bot credentials use `/v1/bot/drive/*`. This does not authorize a user key for other domains. `bf_*` can also search on behalf of a person with `--on-behalf-of <uid>`. `identity.bot_kind` reflects the kind: `app_bot`, `user_bot`, or `user_key`.

Outside `OCTO_CREDENTIAL_MODE=task`, inspect `octo-cli auth status` (or
`octo-cli config show`) to confirm the active identity. Daemon task mode does
not expose profile diagnostics; use the `identity` echoed by each success
envelope and report authentication failures to the daemon instead (see §3).

For task workflow and collaboration in a daemon execution, select
`OCTO_CREDENTIAL_MODE=task` for each CLI invocation. It reads only the injected
`OCTO_TASK_TOKEN`; independent business operations use ordinary authentication.
Do not export mode for the session or switch identity to retry an auth failure.
Follow the daemon's scoped workflow examples. Outside task mode, the existing
environment and profile credential rules remain unchanged.

## 2. Unified gateway configuration

All Octo domains use one gateway. Environment credentials and profiles that
record no URL fall back to `OCTO_API_BASE_URL`; set it for test or self-hosted
deployments:

```bash
# Production defaults to https://im.deepminer.com.cn. Override for test or
# self-hosted deployments:
export OCTO_API_BASE_URL=https://im-test.deepminer.com.cn

export OCTO_SPACE_ID=space_xxx                     # only for platform-scoped bots
export OCTO_FORMAT=json                            # default output format
```

Routing: all services go through one gateway. A stored profile's
`api_base_url` wins over `OCTO_API_BASE_URL`, which wins over the production
default `https://im.deepminer.com.cn` — so a profile that records a test origin
keeps using it after `export OCTO_API_BASE_URL=...`. Change that profile with
`octo-cli auth update --api-base-url <origin>` instead, or remove the stored
origin with `--clear-api-base-url`. `config show` and `auth status` report the
winning layer as `api_base_url_source` (`profile`, `env`, or `default`). On
`octo-cli api`, `--service loop` selects
Loop Public API handling, adds `Accept: application/vnd.octo+json`, and uses the
Public API error parser. The gateway remains unified.

## 3. Output: the JSON envelope

The standard success envelope is the default JSON output (`--format json`, with no `--jq`). Selecting `table`, `csv`, or `ndjson`, or applying a jq projection, can change stdout's shape; help and shell completion render text.

```json
{
  "ok": true,
  "identity": { "type": "bot", "profile": "prod", "robot_id": "cli_xxx", "bot_kind": "app_bot", "source": "profile:prod" },
  "data": { ... or [...] },
  "_pagination": { "has_more": true, "next_cursor": "..." },
  "_rate_limit": { "remaining": 99, "reset": 1730000000 }
}
```

`identity` echoes the bot the command actually ran as — check it to catch acting as the wrong identity. It is **always an object**: a stored profile fills in `profile` / `robot_id` / `source: "profile:<name>"`; a raw environment credential reports the alias that supplied it (`source: "env:OCTO_TOKEN"` or `source: "env:OCTO_BOT_TOKEN"`) and has no verified `profile`/`robot_id`; a command that resolves no credential (e.g. `version`) yields the minimal `{ "type": "bot" }`.

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

`error.code: bot_delete_forbidden` (`error.type: permission`, HTTP 403) is a
terminal policy denial, not missing membership or a transient error. Do not
retry, request a higher bot role, or change `uid`, owner, Space, credentials, or
routes; ask a human document admin to perform the deletion. Do not claim success,
even if the document was already deleted.

Backends differ in their raw error shape. The CLI normalizes both:
- **matters** (structured): `{error:{code, message, details}}` → passes through into `detail` unchanged.
- **dmworkim** (flat): `{msg, status}` → mapped to `code`/`message` via HTTP status.

## 4. Universal flags

These flags work on every command (they are root-level persistent flags):

| Flag            | Purpose                                                                 |
|-----------------|-------------------------------------------------------------------------|
| `--format`      | `json` (default) · `table` · `csv` · `ndjson`                           |
| `--jq`, `-q`    | Apply a jq expression to the success envelope before formatting         |
| `--dry-run`     | Describe the resolved request instead of sending it for request-backed commands; see §6 for exceptions |
| `--verbose`     | Log request/response trace to stderr                                    |
| `--timeout`     | Per-request deadline, e.g. `30s`, `2m`                                  |
| `--no-retry`    | Disable retries, including multipart PUTs, failure-triggered re-signing and completion; URLs expired before the first PUT may still be signed |
| `--space`       | Override `OCTO_SPACE_ID` for this invocation                            |
| `--bot-id`      | Select/assert the stored credential by robot id (env `OCTO_BOT_ID`)     |
| `--profile`     | Select the stored credential by profile name                            |

Cursor-paginated operations that register the convenience flags expose:

| Flag           | Purpose                                                  |
|----------------|----------------------------------------------------------|
| `--page-all`   | Walk pages until `has_more=false`, emit one merged array |
| `--page-limit` | Hard cap on pages fetched with `--page-all` (default 10) |

## 5. Error taxonomy and exit codes

| `error.type`   | Exit | Typical `error.code`                         |
|----------------|------|----------------------------------------------|
| `auth_error`   | 3    | `UNAUTHORIZED`                                |
| `validation`   | 2    | `VALIDATION_ERROR`, `PAYLOAD_TOO_LARGE`      |
| `config`       | 2    | missing env vars                             |
| `permission`   | 1    | `FORBIDDEN`, `SPACE_FORBIDDEN`               |
| `rate_limited` | 1    | `RATE_LIMITED`                               |
| `network`      | 1    | `AUTH_UNAVAILABLE`, `NETWORK_ERROR`, `UPSTREAM_UNAVAILABLE` |
| `api_error`    | 1    | `MATTER_NOT_FOUND`, `NOT_FOUND`, `INTERNAL_ERROR` |
| `internal`     | 1    | CLI-side bug                                 |

Agents should switch on **`error.code` first** (specific, deterministic), then `error.type` (broad), then `exit_code` (coarse).

The `hint` field is a one-line next action meant for an agent: follow it literally where it applies. E.g. `MATTER_NOT_FOUND` → "inspect the operation schema with `octo-cli schema matter.get`; matter commands are currently withheld".

## 6. Input patterns

### Promoted flags vs `--data`

Simple top-level body fields auto-promote to typed flags (strings, integers, numbers, booleans, `[]string`). For objects, arrays-of-objects, or when sending a large payload, use `--data`:

```bash
octo-cli thread create group-abc --name "design review"
octo-cli message send --data '{"channel_id":"chat-1","channel_type":1,"payload":{"type":1,"content":"hi"}}'
octo-cli message send --data @body.json
octo-cli some-cmd --data @-            # read JSON from stdin
```

The `--data` escape hatch is available on commands with a JSON request body, not on every non-multipart operation. Check command help or the operation schema; handwritten commands may have different input shapes.

### Piping with `--jq`

```bash
groups=$(octo-cli group list --format json --jq '[.data[].group_no]')
while IFS= read -r group_no; do
  octo-cli group get "$group_no"
done < <(jq -r '.[]' <<<"$groups")
```

### Paginating

```bash
octo-cli docs search --keyword "spec" --page-all --page-limit 20
```

Use `--page-all` only on operations that expose it. It walks cursor pages until `has_more=false`; page-number operations such as Loop task lists use their own page/page-size flags.

### Dry-run for agent self-verification

```bash
octo-cli message send --data '{"channel_id":"chat-1","channel_type":1,"payload":{"type":1,"content":"foo"}}' --dry-run
```

For ordinary request-backed operations, this prints the resolved request body and URL without sending HTTP. Some local mutations explicitly refuse dry-run: `auth login`, `auth update`, `auth logout`, and `skills --install` return validation errors rather than writing local state. Portable scene export is a live-read exception: `octo-cli docs scene export <docId> --image-format excalidraw -o board.excalidraw --dry-run` performs a scene GET, but does not write the output file.

## 7. Discovering the API

The registry is embedded in the binary — no network needed:

```bash
octo-cli schema --list                   # operations by service
octo-cli schema --list docs              # operation summaries/descriptions
octo-cli schema docs.list                # detail plus request/response schemas
octo-cli docs list --help                # generated operation description + flags
octo-cli skills                          # list active skill guides
octo-cli skills octo-drive               # show a guide and its references
octo-cli config show                     # resolved config (token masked)
octo-cli auth status                     # active identity (whoami)
octo-cli auth list                       # stored profile metadata (no tokens)
```

`skills` without a name lists active guides; `skills <name>` prints the selected guide and its references.

For operator-only profile setup or maintenance (never put token contents in argv):

```bash
octo-cli auth login --profile prod --token-file /secure/path/token.txt --api-base-url https://im-test.deepminer.com.cn
octo-cli auth update --profile prod --api-base-url https://im.deepminer.com.cn
octo-cli auth logout --profile stale
octo-cli skills --install <dir>
```

`--token-file` and `--with-token` are mutually exclusive. `auth login --api-base-url` stores the gateway origin with the profile; `auth update --api-base-url` changes an existing profile's origin without re-entering or rewriting its token. `auth logout` removes a profile, and `skills --install` writes skill files locally. These local mutations require operator intent and reject `--dry-run`.

When an operation isn't auto-registered yet or you need low-level control:

```bash
octo-cli api GET  /api/v1/messages --params '{"chat_id":"chat-1"}'
octo-cli api POST /api/v1/messages --data @body.json
octo-cli api GET  /fleet/api/v1/custom --workspace-id <workspace-uuid>
```

For a workspace-scoped passthrough endpoint, pass `--workspace-id` explicitly;
the CLI sends it as `X-Workspace-ID` and does not infer a workspace.

## 8. Domain skills

Once these fundamentals are understood, load the skill for the domain you need:

- `octo-matter` — matters (todos/tasks), assignees, channels, timeline, AI extract — **temporarily withheld** (backend API stabilizing; not currently loadable)
- `octo-messaging` — message send/edit/sync/read-receipt, groups, threads, events
- `octo-files` — file upload/download, presigned credentials, bot housekeeping
- `octo-drive` — personal and shared file storage, folders, documents, sharing, invitations, and transfers
- `octo-loop` — Loop Public API tasks, runtimes, experts, activity and usage
- `octo-marketplace` — plugins, skills, MCP probes, uploads and review requests
- `octo-docs` — docs domain (CRDT/Yjs): documents, spreadsheets, whiteboard scenes, members/sharing, comments, versions, attachments
- `octo-html` — HTML docs domain (octo-doc, a DIFFERENT backend from octo-docs): self-contained interactive HTML documents, share codes, media assets, comments, agent element read/replace
- `octo-summary` — create owner-only summaries from explicit sources, then discover, read, and cite summaries visible to the personal Agent's human owner — **temporarily withheld** (create backend at Mininglamp-OSS/octo-smart-summary#181 not yet merged/deployed/enabled; not currently loadable)

## 9. CLI maintenance

When the operator requests a CLI update, globally installed npm, Bun and pnpm
copies support:

```bash
octo-cli upgrade --dry-run
octo-cli upgrade
```

Dry run verifies the owning global installation and reports its manager,
version and paths without registry access or mutation. Upgrade uses that same
manager's native mirror, scoped-registry and authentication settings to install
the stable `latest` release by exact version. Equal or newer versions are
successful no-ops. Success is reported only after checking the installed
package, platform binary and fixed global command.

This maintenance command requires no Octo credential; identity and Space
selectors do not choose the installation. Existing `--format` and `--jq`
controls apply. Loop task mode rejects upgrades. Raw binaries, source links,
project-local and temporary installs need their original installation method.
Manager failures stop the operation, and a failed install may already have
changed files; follow the structured error rather than switching managers.
