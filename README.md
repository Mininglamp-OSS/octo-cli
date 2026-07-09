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

octo-cli is **metadata-driven**. The entire command tree — 71 operations
across 8 domains — is auto-registered at startup from OpenAPI 3.x specs
embedded into the binary. Adding or changing an endpoint means editing a
spec, not the code.

```
OpenAPI specs  ──►  Registry  ──►  Service Engine  ──►  Factory  ──►  Client  ──►  Output
(embedded)         (parsed)       (cobra commands)    (DI)          (HTTP)       (envelope)
```

Key properties:

- **Thin client.** All business logic lives in backend services (matters,
  dmworkim). The CLI is transport, validation, and formatting.
- **Multi-backend routing.** Each operation declares its base URL via
  `x-octo-base-url`; the client selects the correct service per call.
- **Factory DI.** `internal/cmdutil.Factory` is the dependency container.
  No mutable package-level globals; tests inject stubs through `ConfigFunc`
  / `CredentialFunc` / `ClientFunc` / `RegistryFunc`.
- **Agent-first output.** A stable JSON envelope with identity, data,
  pagination, and rate-limit metadata; a small fixed error taxonomy.

## Domains

| Domain    | Ops | Purpose                                                        |
|-----------|-----|----------------------------------------------------------------|
| `docs`    | 24  | Documents — lifecycle, body content, members, comments, versions, attachments |
| `matter`  | 14  | Todos/tasks — **temporarily withheld** while the backend API stabilizes |
| `group`   | 9   | Groups — list, get, members, metadata; create/update (User Bot)|
| `thread`  | 8   | Threads — create, list, get, members, join/leave, metadata     |
| `bot`     | 6   | Bot lifecycle — register, user-info, space-members, heartbeat  |
| `message` | 4   | Messaging — send, edit, sync, read-receipt                     |
| `file`    | 4   | Files — upload, download, credentials, presigned URLs          |
| `event`   | 2   | Event polling — list, ack                                      |

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
export OCTO_API_BASE_URL="https://api.example.com"

# NOTE: the `matter` domain is temporarily withheld (backend API stabilizing).

# Messaging
octo-cli message send --data '{"chat_id":"chat-1","text":"hi"}'
octo-cli message edit --data '{"msg_id":"m-1","text":"updated"}'

# Groups and threads
octo-cli group list
octo-cli group members group-abc
octo-cli thread list --chat-id chat-1
octo-cli thread create --chat-id chat-1 --name "design review"

# Files
octo-cli file upload --file ./report.pdf
octo-cli file download abc123 --jq '.data.url'

# Docs — create/list, then read and incrementally edit the live body.
octo-cli docs create --title "Design notes"
octo-cli docs list --sort updatedAt:desc
octo-cli docs get doc-123
octo-cli docs content get doc-123          # returns the body + base version token
octo-cli docs members set doc-123 --data '{"uid":"u-1","role":"writer"}'
octo-cli docs comments add doc-123 --data '{"body":"looks good"}'

# Discover the API — fully offline, specs are embedded.
octo-cli schema --list              # all operations across all domains
octo-cli schema --list message      # operations in one domain
octo-cli schema message.send        # request/response schema for one op
octo-cli config show                # resolved config (token masked)

# Generic passthrough for ops that aren't auto-registered.
octo-cli api GET  /v1/messages --params '{"chat_id":"chat-1"}'
octo-cli api POST /v1/messages --data @body.json
```

## Authentication

`octo-cli` is bot-only — there is no user login. `OCTO_BOT_TOKEN` carries either
an **App Bot** (`app_*`) or **User Bot** (`bf_*`) token:

| Prefix  | Type     | DM | Group read | Group write | Thread | Voice |
|---------|----------|----|------------|-------------|--------|-------|
| `app_*` | App Bot  | yes | yes       | **no**      | **no** | **no**|
| `bf_*`  | User Bot | yes | yes       | yes         | yes    | yes   |

The CLI does not enforce capability locally; the backend rejects
unsupported operations with `FORBIDDEN`.

### API Base URL

All backend services are accessed through a single API base URL.

| Var                 | Purpose                                                  |
|---------------------|----------------------------------------------------------|
| `OCTO_BOT_TOKEN`    | Bot token (`app_*` or `bf_*`). Required.                 |
| `OCTO_API_BASE_URL`  | Unified API base URL for all services. Required.          |
| `OCTO_SPACE_ID`     | Space context for platform-scoped bots.                  |
| `OCTO_FORMAT`       | Default output format (`json` \| `table` \| `csv` \| `ndjson`). |

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
octo-cli message send --data '{"chat_id":"chat-1","text":"Hello"}' --dry-run

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
- [`octo-docs`](./skills/octo-docs/SKILL.md) — documents: lifecycle, live body
  editing, members/sharing, comments, versions, and attachment metadata.

These docs are also **embedded in the binary**, so a released `octo-cli` ships them:

```bash
octo-cli skills                       # list embedded skills
octo-cli skills octo-messaging        # print one skill (name, description, content)
octo-cli skills --install ~/.config/octo/skills   # write all SKILL.md to a dir
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
