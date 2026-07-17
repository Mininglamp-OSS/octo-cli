# CLAUDE.md — octo-cli project instructions

## What is octo-cli

`octo-cli` is the command-line interface for the Octo ecosystem, built for **AI Agent Bots** to call via `exec` from agent runtimes (OpenClaw, Claude Code, etc.). Output is a JSON envelope; agent-runtime commands take no interactive I/O. The sole exception is `octo-cli auth login`, an operator-only setup step that reads the token from a hidden terminal prompt (or stdin).

## Architecture

- Go single binary, cobra CLI framework.
- **Metadata-driven**: the entire service command tree is auto-registered at startup from OpenAPI 3.x specs embedded into the binary via `internal/registry`. To add or change an endpoint, update a spec — not code.
- **Thin client**: all business logic lives in backend services (matters, dmworkim). CLI is transport + validation + formatting.
- **Multi-backend**: different domains live at different base URLs, all routed through a unified API base URL (`OCTO_API_BASE_URL`).
- **Factory DI**: `internal/cmdutil.Factory` is the DI container; no package-level globals. Tests inject stubs through `ConfigFunc` / `CredentialFunc` / `ClientFunc` / `RegistryFunc`. `Factory.ErrorEmitted` tracks whether an error envelope was already written to stderr, preventing double-emit between RunE and the top-level main error handler.
- **JSON envelope I/O**: `{ok, identity, data, _pagination, _rate_limit}` on stdout for success; `{ok:false, error:{type,code,message,hint,detail}}` on stderr for failure. Exit codes: auth=3, validation/config=2, rest=1.

## Identity Model

- The CLI is **bot-only** — no user login. A bot token is an `app_*` (App Bot) or `bf_*` (User Bot) token.
- **Credential resolution** (see `internal/credential`, `internal/authstore`): a token comes from a stored encrypted profile or, as a fallback, `OCTO_BOT_TOKEN`. Stored profiles live in `~/.octo-cli` (override `OCTO_CONFIG_DIR`): metadata in plaintext `config.json`, tokens in AES-256-GCM `credentials.enc`. Manage them with `octo-cli auth`.
- **Selecting a credential at runtime**: `--bot-id <robot_id>` (env `OCTO_BOT_ID`) is the agent's primary selector — robot ids are self-known; `--profile <name>` selects by friendly name. With exactly one profile, selection is implicit; with **two or more, a selector is required** (ambiguity is a hard error, never a silent guess). Precedence: selector > sole/implicit profile > `OCTO_BOT_TOKEN`. The success envelope's `identity` echoes the active `{profile, robot_id, bot_kind, source}` so misuse is visible.
- **Isolation boundary = OS user**: the encryption key is machine-derived, so the store resists off-machine leakage (commit/backup/sync) but not a same-user process. Isolate mutually-distrusting bots with separate OS users or `OCTO_CONFIG_DIR` values.
- Each Bot has an **owner**; operations are attributed to the Bot identity. For LLM-backed paths (`matter extract`) the bot acts on behalf of its owner — pass `owner_uid` as `creator_uid`.
- `OCTO_SPACE_ID` (or `--space`) supplies space context for platform-scoped bots. Space-scoped bots resolve their space server-side.

## Command Structure (9 domains, 98 operations)

Service commands are auto-registered. The hand-written leaves are `schema`, `version`, `api` (generic passthrough), `config`, `auth`, and the cobra-generated `completion`.

> **`matter` is temporarily withheld** (backend API not yet stable). The spec
> stays embedded — `octo-cli schema matter.*` still introspects it — but the command
> subtree and the `octo-matter` skill are hidden via the `x-octo-disabled` spec
> flag (`internal/registry/specs/matter.json`) and the skill's `disabled: true`
> frontmatter. Flip both off to re-enable. The tree below shows the full surface.

```
octo-cli matter    create | list | get | update | delete        (withheld)
               transition | close | reopen | archive | extract
               assignee add|remove
               channel  link|unlink
               timeline add|list|delete
octo-cli message   send | edit | sync | read-receipt
octo-cli group     list | get | members | md-get | md-update
               create | update | member-add | member-remove       (User Bot only)
octo-cli thread    create | list | get | members
               join | leave | md-get | md-update                  (User Bot only)
octo-cli file      upload | download | credentials | presigned
octo-cli bot       register | set-commands | user-info | space-members | typing | heartbeat
octo-cli event     list | ack
octo-cli docs      create | list | get | rename | delete | forward-grant
               content  get|edit
               sheet    get|edit
               scene    get|edit
               members  list|set|remove
               share    get|set
               comments list|add|edit|delete
               versions list|create|state|rename|delete|restore
               attachments presign|get|resolve
octo-cli html      list | get | publish | versions | rm     (octo-doc HTML docs; distinct backend from `docs`)
               draft    save|promote
               share | unshare
               grant    add|list|rm
               asset    add|ls|rm
               comment  list|add
               element  get|replace
               reply

octo-cli auth      login | status | logout | list
octo-cli schema [--list [domain] | <operation-id>]
octo-cli api <METHOD> <PATH> [--params ...] [--data ...] [--service ...]
octo-cli config show
octo-cli completion bash|zsh|fish|powershell
octo-cli version
```

`octo-cli auth login` stores a bot token (read from a hidden prompt, `--with-token` stdin, or `--token-file` — never argv) under a profile keyed by `--bot-id`/`--profile`. `status`/`list` show metadata only (tokens always masked); `logout` removes a profile.

Bot-type capability and per-command flags are in `docs/octo-cli-design.md`. Agent-facing usage lives under `skills/` (`octo-shared`, `octo-matter` (withheld — see above), `octo-messaging`, `octo-files`, `octo-docs`, `octo-html`) — keep those in sync when command shapes change.

## Environment

| Var                 | Purpose                                                  |
|---------------------|----------------------------------------------------------|
| `OCTO_BOT_TOKEN`    | Bot token (`app_*` or `bf_*`). Fallback when no stored profile is selected. |
| `OCTO_BOT_ID`       | Robot id selecting a stored profile (env form of `--bot-id`). Selector, not a secret. |
| `OCTO_CONFIG_DIR`   | Override the credential dir (default `~/.octo-cli`).     |
| `OCTO_API_BASE_URL`  | Unified API base URL for all services. Required.          |
| `OCTO_SPACE_ID`     | Space context for platform-scoped bots.                  |
| `OCTO_FORMAT`       | Default output format (`json` | `table` | `csv` | `ndjson`). |

Universal flags: `--format`, `--jq`/`-q`, `--dry-run`, `--verbose`, `--timeout`, `--no-retry`, `--space`, `--bot-id`, `--profile`. Paginated ops additionally support `--page-all` / `--page-limit`.

## Code Style

- `gofmt`, `go vet`, standard-library `testing` with table-driven tests.
- Errors wrap with `fmt.Errorf("context: %w", err)`; CLI errors use the `*output.ExitError` taxonomy so envelopes stay structured.
- The `internal/output` package is a leaf — it must not import other `internal/*` packages.
- No mutable package-level globals; state flows through the Factory. The only package-level `var` declarations allowed are (a) ldflags-injected build metadata (`cmd/build.go`), (b) `//go:embed` file systems (`internal/registry`), and (c) immutable lookup tables (e.g. `backendErrorMapping` in `internal/output/errors.go`, `httpMethods` in `internal/registry/loader.go`). These are const-equivalent — never mutated at runtime — and exist only because Go doesn't permit `const` for their types.
- External deps limited to cobra, gojq, `golang.org/x/term` (hidden token prompt in `octo-cli auth login`), and the standard library.
- All text in English.

## Build & Test

```bash
go build -o octo-cli ./cmd/octo-cli
go test ./... -count=1
go vet ./...

# Shell completion (cobra built-in)
octo-cli completion bash   > /etc/bash_completion.d/octo-cli
octo-cli completion zsh    > "${fpath[1]}/_octo-cli"
octo-cli completion fish   > ~/.config/fish/completions/octo-cli.fish
```

Version metadata is injected via `-ldflags` at release time (see `cmd/build.go`).
