# CLAUDE.md — octo-cli project instructions

## What is octo-cli

octo-cli is a command-line interface for the Octo ecosystem, designed primarily for **AI Agent Bots** to interact with Octo services via `exec` calls from agent runtimes (OpenClaw, Claude Code, etc.).

Each Octo built-in service module maps to a CLI subcommand: `octo todo`, `octo <future-module>`, etc.

**Current scope: `octo todo` only.**

## Architecture

- Go single binary, cobra CLI framework
- Thin REST client — all logic lives in backend services (todo-service, etc.)
- Default output: JSON (consumed by bots); `--format table` for human debugging
- Authentication: Bot Token via `OCTO_BOT_TOKEN` env var (robot_id:app_key format)
- Server URL: `OCTO_API_URL` env var

## Identity Model

- **Bot** authenticates with its own token (not user credentials)
- Each Bot has an **owner** (the human who created it)
- Operations are attributed to the Bot identity
- Daemon (octo-daemon) is device-level, separate auth — daemon installs octo-cli but auth is independent

## Command Structure

```
octo todo list [--goal <id>] [--status <s>] [--assignee <uid>] [--limit <n>] [--cursor <c>]
octo todo create --title <t> [--goal <id>] [--assignee <uid>...] [--deadline <date>] [--desc <d>] [--source-channel <id>] [--source-type <n>]
octo todo get <id>
octo todo update <id> [--title <t>] [--desc <d>] [--deadline <date>]
octo todo close <id>
octo todo reopen <id>
octo todo delete <id>
octo todo assign <id> <uid>
octo todo unassign <id> <uid>
octo todo comment <id> <text>
octo todo comments <id>
octo todo comment-delete <id> <comment-id>
octo todo attachment list <id>
octo todo attachment add <id> --url <url> [--name <n>] [--type <mime>]
octo todo attachment delete <id> <attachment-id>

octo todo goal list
octo todo goal create --title <t> [--desc <d>]
octo todo goal get <id>
octo todo goal update <id> [--title <t>] [--desc <d>]
octo todo goal assign <id> <uid>
octo todo goal unassign <id> <uid>
octo todo goal archive <id>

octo version
```

## API Mapping

All commands call todo-service REST API at `$OCTO_API_URL/api/v1/...`


## Code Style

- Go: `gofmt`, `go vet`
- Tests: standard `testing`, table-driven
- Errors: `fmt.Errorf("context: %w", err)`
- No external deps beyond cobra and standard library
- All text in English

## Build & Test

```bash
go build -o octo ./cmd/octo
go test ./... -count=1
go vet ./...
```
