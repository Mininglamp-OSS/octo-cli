# octo-cli

[![CI](https://github.com/Mininglamp-OSS/octo-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/Mininglamp-OSS/octo-cli/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/Mininglamp-OSS/octo-cli.svg)](https://pkg.go.dev/github.com/Mininglamp-OSS/octo-cli)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)

`octo-cli` is the command-line interface for the **Octo ecosystem** — a thin,
single-binary REST client designed for **AI Agent Bots** to call via `exec`
from agent runtimes (OpenClaw, Claude Code, and similar). Every invocation
emits a structured JSON envelope on stdout; errors go to stderr with a
deterministic taxonomy. There is no interactive I/O.

## Architecture

octo-cli is **metadata-driven**. The command tree is auto-registered at startup
from OpenAPI 3.x specs embedded into the binary. Adding or changing an endpoint
means editing a spec, not the code.

```
OpenAPI specs  ──►  Registry  ──►  Service Engine  ──►  Factory  ──►  Client  ──►  Output
(embedded)         (parsed)       (cobra commands)    (DI)          (HTTP)       (envelope)
```

Key properties:

- **Thin client.** All business logic lives in backend services (matters,
  dmworkim). The CLI is transport, validation, and formatting.
- **Unified gateway routing.** Each operation declares its complete
  module-qualified path and uses `OCTO_API_BASE_URL`.
- **Factory DI.** `internal/cmdutil.Factory` is the dependency container.
  No mutable package-level globals; tests inject stubs through `ConfigFunc`
  / `CredentialFunc` / `ClientFunc` / `RegistryFunc`.
- **Agent-first output.** A stable JSON envelope with identity, data,
  pagination, and rate-limit metadata; a small fixed error taxonomy.

## Domains

| Domain    | Ops | Purpose                                                        |
|-----------|-----|----------------------------------------------------------------|
| `docs`    | 32  | Documents, spreadsheets & whiteboards — lifecycle, full-text search, body content, sheet cells (paged read), board scenes, members, comments, versions, attachments |
| `html`    | 20  | Interactive HTML documents (octo-doc, **separate backend** from `docs`) — publish immutable versions, drafts, per-doc share codes & per-uid grants, media assets, inline comments, agent element read/replace |
| `drive`   | 43  | Network drive — spaces & members, folder/file tree, full-text search, two-phase blob upload & signed download, online-document mounts, share links, invites, IM-attachment transfer. Plus 3 composite commands (`upload file`, `download file`, `share create`) for 46 leaves total |
| `matter`  | 14  | Todos/tasks — **temporarily withheld** while the backend API stabilizes |
| `summary` | 4   | Personal-bot summaries — create owner-only summaries from explicit sources, then discover/read/cite. **Temporarily withheld** while the create backend (Mininglamp-OSS/octo-smart-summary#181) is merged, deployed, and enabled |
| `group`   | 9   | Groups — list, get, members, metadata; create/update (User Bot)|
| `thread`  | 8   | Threads — create, list, get, members, join/leave, metadata     |
| `bot`     | 6   | Bot lifecycle — register, user-info, space-members, heartbeat  |
| `message` | 10  | Messaging — send, edit, sync, read-receipt; search (search/all/files/media/around/groups, in-channel or cross-channel) |
| `file`    | 4   | Files — upload, download, credentials, presigned URLs          |
| `event`   | 2   | Event polling — list, ack                                      |
| `loop`    | 126 | Fleet control plane — tasks, executions, experts, expert teams, workspaces, runtimes, projects, skills, automations, attachments, comments, labels, and related resources |

## Installation

### npm

For Node-based agent runtimes (OpenClaw, etc.):

```bash
npm install -g @mininglamp-oss/octo-cli
```

The npm package resolves the matching platform sub-package, which already
contains the prebuilt binary. Install does not download binaries from GitHub.

### Go install

```bash
go install github.com/Mininglamp-OSS/octo-cli/cmd/octo-cli@latest
```

### Homebrew (coming soon)

```bash
brew install Mininglamp-OSS/tap/octo-cli
```

### GitHub Releases

Download the latest binary for your platform from
[GitHub Releases](https://github.com/Mininglamp-OSS/octo-cli/releases):

```bash
# Archives are named octo-cli_<version>_<os>_<arch>.tar.gz for every platform,
# including Windows. Pick the one for your platform and substitute <version>
# (e.g. 0.5.0):
curl -LO https://github.com/Mininglamp-OSS/octo-cli/releases/download/v<version>/octo-cli_<version>_linux_amd64.tar.gz
tar xzf octo-cli_<version>_linux_amd64.tar.gz
sudo mv octo-cli /usr/local/bin/
```

Windows release archives are `.tar.gz` as well; Windows 10+ includes `tar.exe`.

### install.sh

```bash
curl -fsSL https://raw.githubusercontent.com/Mininglamp-OSS/octo-cli/main/install.sh | sh
```

## Quick Start

```bash
# Authenticate as a bot.
export OCTO_BOT_TOKEN="bf_your_user_bot_token"
# Optional for test or self-hosted deployments; production is the default.
# export OCTO_API_BASE_URL="https://im-test.deepminer.com.cn"

# NOTE: the `matter` domain is temporarily withheld (backend API stabilizing).

# Messaging
octo-cli message send --data '{"channel_id":"chat-1","channel_type":1,"payload":{"type":1,"content":"hi"}}'
octo-cli message edit --data '{"message_id":"m-1","channel_id":"chat-1","channel_type":1,"content_edit":"{\"type\":1,\"content\":\"updated\"}"}'

# Message search (User Bot bf_ or user API key uk_ token; App Bot app_ is rejected locally).
octo-cli message search --chat-id chat-1 --keyword "quarterly report"   # in-channel
octo-cli message search --keyword "quarterly report"                    # cross-channel (mixed feed)
octo-cli message search files --chat-id chat-1 --keyword "*.pdf"        # files in a channel
octo-cli message search groups --keyword "quarterly report"            # which channels matched (L1)
octo-cli message search all --keyword "budget" --on-behalf-of u-alice  # OBO: as a real person

# Groups and threads
octo-cli group list
octo-cli group members group-abc
octo-cli thread list group-abc
octo-cli thread create group-abc --name "design review"

# Files
octo-cli file upload --file ./report.pdf
octo-cli file download abc123 --jq '.data.url'

# Agent Mail — policy-aware send; the server may accept it or save a Draft
# depending on the mailbox's current outbound mode.
octo-cli mail message send-intent \
  --to recipient@example.com --subject "Status update" --text "Ready." \
  --idempotency-key "send-example-001"
octo-cli mail thread get T123
octo-cli mail draft list
# Draft update replaces the entire Draft. Read it first and resend every field
# that must remain; omitted cc/bcc/text/html/attachments are removed.
octo-cli mail message read E123
octo-cli mail draft update E123 --draft-version 1 \
  --to recipient@example.com --cc teammate@example.com \
  --bcc archive@example.com --subject "Updated draft" --text "Updated body"
# Retaining attachments requires a complete attachments array containing the
# exact base64 content, supplied through --data.

# Send an ordinary human-authored Draft without --draft-version.
octo-cli mail draft send E120
# Send an Agent-prepared Draft with its current version.
octo-cli mail draft send E124 --draft-version 2
octo-cli mail draft delete E125

# Docs — create/list/search, then read and incrementally edit the live body.
octo-cli docs create --title "Design notes"
octo-cli docs list --sort updatedAt:desc
octo-cli docs search --keyword "quarterly plan" --doc-type doc --page-all
octo-cli docs get doc-123
octo-cli docs content get doc-123          # returns the body + base version token
octo-cli docs import doc-123 --file ./notes.md      # replaces a doc from .md/.markdown/.docx
octo-cli docs export doc-123 --export-format pdf -o ./notes.pdf
octo-cli docs members set doc-123 --data '{"uid":"u-1","role":"writer"}'

# Fleet/Loop uses the same gateway under /fleet/api/v1.
octo-cli loop task list --workspace-id <workspace-id>
octo-cli docs comments add doc-123 --data '{"body":"looks good"}'

# HTML documents registered in docs-backend use the same search endpoint.
# Resolve a hit's HTML document reference with docs get, then continue in the separate html domain.
octo-cli docs search --keyword "interactive roadmap" --doc-type html --page-all
octo-cli docs get html-doc-id              # returns octoDocSlug for HTML documents
octo-cli html get <octoDocSlug>

# Spreadsheets — read the live cells + base version, then batch-edit under If-Match.
octo-cli docs sheet get sheet-9                      # whole sheet + base version token
octo-cli docs import sheet-9 --file ./report.xlsx    # imports the first visible worksheet
octo-cli docs export sheet-9 --export-format xlsx -o ./report.xlsx
octo-cli docs sheet get sheet-9 --limit 500          # page a large sheet; follow --cursor <nextCursor>
octo-cli docs sheet edit sheet-9 --base-version "<token>" \
  --data '{"cells":{"default!0:0":{"v":"hi"},"default!1:0":null}}'

# Whiteboards — read the live scene + base version, then upsert/delete elements under If-Match.
octo-cli docs scene get board-7                       # elements (z-order) + files + base version token
octo-cli docs scene edit board-7 --base-version "<token>" \
  --data '{"elements":[{"id":"e1","type":"rectangle","version":4}],"deletedElementIds":["e2"],"files":{}}'
octo-cli docs import board-7 --file ./board.excalidraw              # merge (default): preserves existing elements
octo-cli docs import board-7 --file ./board.excalidraw --mode replace # explicit overwrite; backend safety snapshot + concurrency protection
octo-cli docs export board-7 --export-format png -o ./board.png

# HTML docs (octo-doc) — a SEPARATE backend from `docs`. Publish self-contained
# interactive HTML as immutable versions, then edit a single stamped artifact.
# Canonical create has no doc reference: omit --slug. The CLI generates a key.
octo-cli html publish --html '<h1>hi</h1>' --title 'Launch page' \
  --mount-type group --group-no <group_no>
# Save data.slug from the publish response. For a new document data.slug == data.doc_id, mounted or
# unmounted. Every later operation uses data.slug. Old documents keep their legacy
# slug as data.slug; do not infer this from mount_type or doc_id being non-empty.
# To republish, pass --slug <doc-ref> and omit --idempotency-key. An unknown
# legacy slug is rejected and cannot create a document.
octo-cli html list
octo-cli html versions <doc-ref>
octo-cli html draft create --html '<h1>wip</h1>' --title 'WIP page'
octo-cli html draft save <doc-ref> --html '<h1>wip</h1>'             # then: html draft promote <doc-ref>
# --title sets the display name (metadata, never identity) on publish/draft create/save/promote.
octo-cli html share <doc-ref>                                        # mint a reader share code
octo-cli html grant add <doc-ref> --data '{"uid":"u-1"}'             # per-uid authorization
octo-cli html element get --slug <doc-ref> --aid <content-hash>      # wire flag remains named slug
octo-cli html element replace --slug <doc-ref> --aid <content-hash> --new-html '<p>new</p>'

# Discover the API — fully offline, specs are embedded.
octo-cli schema --list              # all operations across all domains
octo-cli schema --list message      # operations in one domain
octo-cli schema message.send        # request/response schema for one op
octo-cli config show                # resolved config (token masked)
octo-cli auth update --api-base-url https://octo.example # persist endpoint for the active profile

# Generic passthrough for ops that aren't auto-registered.
octo-cli api GET  /v1/messages --params '{"chat_id":"chat-1"}'
octo-cli api POST /v1/messages --data @body.json
```

## Authentication

`octo-cli` is bot-only — there is no interactive user login. The token comes
from `OCTO_TOKEN` (preferred) or `OCTO_BOT_TOKEN`, and carries an **App Bot**
(`app_*`), a **User Bot** (`bf_*`), a **user API key** (`uk_*`, a real-person
identity used for message search and drive), or a short-lived **Loop task
credential** (`octo_loop_*`):

| Prefix  | Type         | DM  | Group read | Group write | Thread | Voice | Search |
|---------|--------------|-----|------------|-------------|--------|-------|--------|
| `app_*` | App Bot      | yes | yes        | **no**      | **no** | **no**| **no** |
| `bf_*`  | User Bot     | yes | yes        | yes         | yes    | yes   | yes    |
| `uk_*`  | User API key | —   | —          | —           | —      | —     | yes    |
| `octo_loop_*` | Loop task credential | Fleet policy | Fleet policy | Fleet policy | — | — | — |

(`drive` accepts all three prefixes: `uk_*` acts as the real person, `bf_*` /
`app_*` as the bot. A bot still has to be added as a member of a shared drive
space, exactly like a person.)

The CLI does not enforce capability locally; the backend rejects unsupported
operations with `FORBIDDEN`. Two local exceptions: an `app_*` token running
`message search`, and a credential whose kind a domain's spec does not allow
(`TOKEN_KIND_NOT_ALLOWED`) — both are `validation` errors raised before any
request. `uk_*` tokens are routed to `/v1/user/*`; a `bf_*` token can search as
a real person with `--on-behalf-of <uid>` (OBO, requires an active grant).

### Token variables

Two variables supply the token, in this order:

| Priority | Source | Notes |
|---|---|---|
| 1 | stored profile | `octo-cli auth login`; select with `--bot-id` / `--profile` |
| 2 | `OCTO_TOKEN` | preferred variable; any of the three token kinds |
| 3 | `OCTO_BOT_TOKEN` | long-standing variable, fully supported |

`OCTO_TOKEN` lets you run a single command as a different identity without
disturbing an existing setup:

```bash
OCTO_TOKEN="$UK_KEY" octo-cli drive space list      # acts as the real person
```

The success envelope's `identity.source` names the variable actually used
(`env:OCTO_TOKEN` or `env:OCTO_BOT_TOKEN`), so a mix-up is visible.

### API Base URL

All backend services are accessed through a single API base URL.
The value is a gateway origin (`http(s)://host[:port]`) rather than a
service-specific path; query strings, fragments, credentials, and API paths are
rejected.

| Var                 | Purpose                                                  |
|---------------------|----------------------------------------------------------|
| `OCTO_TOKEN`        | Token (`app_*`, `bf_*`, or `uk_*`). Preferred; wins over `OCTO_BOT_TOKEN`. |
| `OCTO_BOT_TOKEN`    | Token (`app_*`, `bf_*`, `uk_*`, or `octo_loop_*`). Used when `OCTO_TOKEN` is unset. |
| `OCTO_CREDENTIAL_MODE` | Credential policy; set to `task` only for daemon-launched Loop tasks. |
| `OCTO_API_BASE_URL`  | Optional API base URL override; defaults to `https://im.deepminer.com.cn`. |
| `OCTO_BOT_ID`       | Select/assert the bot credential by robot id (see `--bot-id`). |
| `OCTO_CONFIG_DIR`   | Override the config/credential directory (default `~/.octo-cli`). |
| `OCTO_SPACE_ID`     | Space context for platform-scoped bots.                  |
| `OCTO_FORMAT`       | Default output format (`json` \| `table` \| `csv` \| `ndjson`). |

Daemon-launched tasks set `OCTO_CREDENTIAL_MODE=task` and must also run with an
isolated `OCTO_CONFIG_DIR` that contains no host profiles. The mode flag selects
the restricted CLI policy but is not a security boundary against a process that
can rewrite its own environment. In task mode, use the injected
`OCTO_BOT_TOKEN`; `auth` and `config` diagnostics are intentionally unavailable.

## Output

Every successful invocation prints a JSON envelope on stdout:

```json
{
  "ok": true,
  "identity": "bot",
  "data": { ... },
  "_pagination": { "has_more": true, "next_cursor": "..." },
  "_rate_limit": { "remaining": 99, "reset": 1730000000 }
}
```

Every failure prints an error envelope on **stderr** and exits non-zero:

```json
{
  "ok": false,
  "error": {
    "type": "validation",
    "code": "VALIDATION_ERROR",
    "message": "title is required",
    "hint": "check params with `octo-cli schema <op>`",
    "detail": { ... }
  }
}
```

Exit codes: `3` auth, `2` validation/config, `1` everything else.

Some failures are raised locally, before any request is sent: a missing required
body field or one violating `minItems` (`VALIDATION_ERROR`), a value outside a
spec-declared `enum` (`ENUM_NOT_ALLOWED`), and a malformed or out-of-range
`uint64` id. These checks apply to the whole resolved body — promoted flags and
`--data` alike — and to query parameters, so a bad value costs no round trip.

### Universal flags

| Flag            | Purpose                                                         |
|-----------------|-----------------------------------------------------------------|
| `--format`      | `json` (default) \| `table` \| `csv` \| `ndjson`                |
| `--jq`, `-q`    | Apply a jq expression to the envelope before formatting         |
| `--dry-run`     | Print the resolved request instead of sending it                |
| `--verbose`     | Log request/response trace to stderr                            |
| `--timeout`     | Per-request deadline, e.g. `30s`, `2m`                          |
| `--no-retry`    | Disable retry on transient failures                             |
| `--space`       | Override `OCTO_SPACE_ID` for one call                           |
| `--page-all`    | Walk pages until `has_more=false`, emit one merged array        |
| `--page-limit`  | Hard cap on pages fetched with `--page-all` (default 10)        |

### Examples

```bash
# Dry-run to inspect the resolved request — no side effects.
octo-cli message send --data '{"channel_id":"chat-1","channel_type":1,"payload":{"type":1,"content":"Hello"}}' --dry-run

# Extract a single field with jq.
octo-cli group list --jq '.data[0].id'

# Auto-paginate any list operation that reports a cursor.
octo-cli group list --page-all --page-limit 20

# Tabular output for human eyes.
octo-cli group list --format table
```

## Agent Skills

Machine-readable usage docs for AI Agents live under [`skills/`](./skills/):

- [`octo-shared`](./skills/octo-shared/SKILL.md) — fundamentals (auth,
  output, flags, error taxonomy). Load first.
- `octo-matter` — matter (todo/task) domain. **Temporarily withheld** while the
  backend API stabilizes (not listed by `octo-cli skills`).
- [`octo-messaging`](./skills/octo-messaging/SKILL.md) — messages, groups,
  threads, event polling.
- [`octo-files`](./skills/octo-files/SKILL.md) — files and bot housekeeping.
- [`octo-drive`](./skills/octo-drive/SKILL.md) — network drive: spaces and
  members, folder/file tree, upload/download, online-document mounts, share
  links, invites, IM-attachment transfer.
- [`octo-docs`](./skills/octo-docs/SKILL.md) — documents: lifecycle plus
  progressive-disclosure references. `SKILL.md` is a slim router; task detail
  lives in sibling files loaded on demand: `sheet.md` (spreadsheets), `doc.md`
  (rich-text body), `board.md` (whiteboard), and `common.md` (comments,
  versions, members/sharing, attachments).
- [`octo-marketplace`](./skills/octo-marketplace/SKILL.md) — search, install,
  publish, and update Marketplace Skills and MCP server listings, plus Experts
  (专家) and Squads (专家团).
- [`octo-html`](./skills/octo-html/SKILL.md) — HTML docs (octo-doc, a
  **separate backend** from `octo-docs`): publish immutable versions, drafts,
  share codes & per-uid grants, media assets, inline comments, agent element
  read/replace.
- [`octo-mail`](./skills/octo-mail/SKILL.md) — Agent Mail authorization,
  mailbox access, message handling, drafts, attachments, and delivery status.
- [`octo-summary`](./skills/octo-summary/SKILL.md) — create owner-only summaries
  from explicit sources, then discover, read, and cite summaries visible to the
  personal Agent's human owner. **Temporarily withheld** while the create
  backend (Mininglamp-OSS/octo-smart-summary#181) is merged, deployed, and enabled
  (not listed by `octo-cli skills`).

These docs are also **embedded in the binary**, so a released `octo-cli` ships them:

```bash
octo-cli skills                       # list embedded skills
octo-cli skills octo-mail             # load the official Agent Mail guide
octo-cli skills octo-docs             # print one skill (SKILL.md + its references)
octo-cli skills --install ~/.config/octo/skills   # write every skill (SKILL.md + references) to a dir
```

## Shell Completion

```bash
octo-cli completion bash   > /etc/bash_completion.d/octo-cli
octo-cli completion zsh    > "${fpath[1]}/_octo-cli"
octo-cli completion fish   > ~/.config/fish/completions/octo-cli.fish
```

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md). TL;DR: add or change an endpoint
by editing a spec in `internal/registry/specs/`, not Go code.

## License

[Apache-2.0](./LICENSE)
