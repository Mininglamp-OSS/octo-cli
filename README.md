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

# List todos
octo todo list

# Create a todo
octo todo create --title "Deploy v2.0" --assignee user-123

# Mark done
octo todo done <todo-id>

# Create a goal
octo todo goal create --title "Q3 Release"
```

## Authentication


## Output

Default: JSON (for bot consumption). Use `--format table` for human-readable output.

## License

Apache-2.0
