# octo-cli

Command-line interface for the Octo ecosystem. Designed for AI Agent Bots to interact with Octo services via `exec` from agent runtimes.

## Install

```bash
go install github.com/dmwork-org/octo-cli/cmd/octo@latest
```

Also installed automatically with `octo-daemon`.

## Quick Start

```bash
export OCTO_BOT_TOKEN="your-robot-id/your-app-key"
export OCTO_SPACE_ID="your-space-id"  # optional
export OCTO_API_URL="https://todo.example.com"

# Todos
octo todo list
octo todo list --status open --assignee me
octo todo create --title "Deploy v2.0" --assignee user-123
octo todo get <todo-id>
octo todo update <todo-id> --title "New title"
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
octo todo goal list
octo todo goal create --title "Q3 Release"
octo todo goal get <goal-id>
octo todo goal update <goal-id> --title "Q4 Release"
octo todo goal assign <goal-id> <user-id>
octo todo goal unassign <goal-id> <user-id>
octo todo goal archive <goal-id>
```

## Authentication

Set `OCTO_BOT_TOKEN` to your bot's `robot_id/app_key` credentials. The CLI sends `Authorization: Bot <token>` on every request.

`OCTO_SPACE_ID` is optional — if set, it's sent as `X-Space-ID` header for space-scoped operations. Bot auth can auto-resolve the space from the server.

## Output

Default: JSON (for bot consumption). Use `--format table` for human-readable output.

## License

Apache-2.0
