# CLAUDE.md — octo-cli project instructions

## What is octo-cli

`octo` is the command-line interface for the Octo ecosystem, built for **AI Agent Bots** to call via `exec` from agent runtimes (OpenClaw, Claude Code, etc.). Output is a JSON envelope; there is no interactive I/O.

## Architecture

- Go single binary, cobra CLI framework.
- **Metadata-driven**: the entire service command tree is auto-registered at startup from OpenAPI 3.x specs embedded into the binary via `internal/registry`. To add or change an endpoint, update a spec — not code.
- **Thin client**: all business logic lives in backend services (matters, dmworkim). CLI is transport + validation + formatting.
- **Multi-backend**: different domains live at different base URLs, resolved per-operation from the spec's `x-octo-base-url`.
- **Factory DI**: `internal/cmdutil.Factory` is the DI container; no package-level globals. Tests inject stubs through `ConfigFunc` / `CredentialFunc` / `ClientFunc` / `RegistryFunc`.
- **JSON envelope I/O**: `{ok, identity, data, _pagination, _rate_limit}` on stdout for success; `{ok:false, error:{type,code,message,hint,detail}}` on stderr for failure. Exit codes: auth=3, validation/config=2, rest=1.

## Identity Model

- The CLI is **bot-only** — no user login. `OCTO_BOT_TOKEN` carries an `app_*` (App Bot) or `bf_*` (User Bot) token.
- Each Bot has an **owner**; operations are attributed to the Bot identity. For LLM-backed paths (`matter extract`) the bot acts on behalf of its owner — pass `owner_uid` as `creator_uid`.
- `OCTO_SPACE_ID` (or `--space`) supplies space context for platform-scoped bots. Space-scoped bots resolve their space server-side.

## Command Structure (7 domains, 51 operations)

Service commands are auto-registered. The hand-written leaves are `schema`, `version`, `api` (generic passthrough), `config`, and the cobra-generated `completion`.

```
octo matter    create | list | get | update | delete
               transition | close | reopen | archive | extract
               assignee add|remove
               channel  link|unlink
               timeline add|list|delete
octo message   send | edit | sync | read-receipt
octo group     list | get | members | md-get | md-update
               create | update | member-add | member-remove       (User Bot only)
octo thread    create | list | get | delete | members
               join | leave | md-get | md-update                  (User Bot only)
octo file      upload | download | credentials | presigned
octo bot       register | set-commands | user-info | space-members | typing | heartbeat
octo event     list | ack

octo schema [--list [domain] | <operation-id>]
octo api <METHOD> <PATH> [--params ...] [--data ...] [--service ...]
octo config show
octo completion bash|zsh|fish|powershell
octo version
```

Bot-type capability and per-command flags are in `docs/octo-cli-design.md`. Agent-facing usage lives under `skills/` (`octo-shared`, `octo-matter`, `octo-messaging`, `octo-files`) — keep those in sync when command shapes change.

## Environment

| Var                 | Purpose                                                  |
|---------------------|----------------------------------------------------------|
| `OCTO_BOT_TOKEN`    | Bot token (`app_*` or `bf_*`). Required.                 |
| `OCTO_API_URL`      | Fallback base URL.                                       |
| `OCTO_MATTERS_URL`  | Matters service.                                         |
| `OCTO_DMWORKIM_URL` | dmworkim (message/group/thread/file/bot/event).          |
| `OCTO_SPACE_ID`     | Space context for platform-scoped bots.                  |
| `OCTO_FORMAT`       | Default output format (`json` | `table` | `csv` | `ndjson`). |

Universal flags: `--format`, `--jq`/`-q`, `--dry-run`, `--verbose`, `--timeout`, `--no-retry`, `--space`. Paginated ops additionally support `--page-all` / `--page-limit`.

## Code Style

- `gofmt`, `go vet`, standard-library `testing` with table-driven tests.
- Errors wrap with `fmt.Errorf("context: %w", err)`; CLI errors use the `*output.ExitError` taxonomy so envelopes stay structured.
- The `internal/output` package is a leaf — it must not import other `internal/*` packages.
- No package-level globals; resolve everything through the Factory.
- External deps limited to cobra, yaml, jq-go, and the standard library.
- All text in English.

## Build & Test

```bash
go build -o octo ./cmd/octo
go test ./... -count=1
go vet ./...

# Shell completion (cobra built-in)
octo completion bash   > /etc/bash_completion.d/octo
octo completion zsh    > "${fpath[1]}/_octo"
octo completion fish   > ~/.config/fish/completions/octo.fish
```

Version metadata is injected via `-ldflags` at release time (see `cmd/build.go`).
