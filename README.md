# octo-cli

Command-line interface for the Octo ecosystem. Designed for AI Agent Bots to interact with Octo services via `exec` from agent runtimes.

## Install

```bash
go install github.com/dmwork-org/octo-cli/cmd/octo@latest
```

Also installed automatically with `octo-daemon`.

## Quick Start

```bash
export OCTO_BOT_TOKEN="your-bot-token"
export OCTO_API_URL="https://todo.example.com"

# Todos
octo todo list
octo todo list --status open --assignee me
octo todo list -q "deploy" --creator user-456
octo todo create --title "Deploy v2.0" --assignee user-123 --remind-at 2026-06-01T09:00:00Z
octo todo get <todo-id>
octo todo update <todo-id> --title "New title" --goal <goal-id>
octo todo close <todo-id>
octo todo reopen <todo-id>
octo todo delete <todo-id>

# Assignees
octo todo assign <todo-id> <user-id>
octo todo unassign <todo-id> <user-id>

# Comments
octo todo comment <todo-id> "Fix the build"
octo todo comments <todo-id>
octo todo comment-delete <todo-id> <comment-id>

# Attachments
octo todo attachment list <todo-id>
octo todo attachment add <todo-id> --url https://example.com/file.pdf --name report.pdf
octo todo attachment delete <todo-id> <attachment-id>

# Goals
octo todo goal list --status active
octo todo goal create --title "Q3 Release" --deadline 2026-09-30T00:00:00Z --assignee user-1
octo todo goal get <goal-id>
octo todo goal update <goal-id> --title "Q4 Release" --deadline 2026-12-31T00:00:00Z
octo todo goal assign <goal-id> <user-id>
octo todo goal unassign <goal-id> <user-id>
octo todo goal archive <goal-id>
```

## Authentication

Set `OCTO_BOT_TOKEN` to your BotFather bot token. The CLI sends `Authorization: Bearer <token>` on every request. Space context is resolved server-side from the token.

## Output

Default: JSON (for bot consumption). Use `--format table` for human-readable output.

## License

Apache-2.0
