---
name: octo-marketplace
version: 0.4.0
description: Search, install, publish, and update Marketplace Skills, MCP server listings, and Experts/Squads (专家/专家团). Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-marketplace — Skills and MCP servers

All commands use `$OCTO_API_BASE_URL/market/api/v1/*`, authenticate with the
active bot profile, and send its Space context. Marketplace is one backend with
several catalog domains, exposed as `marketplace skill`, `marketplace mcp`, and
`marketplace expert` / `marketplace squad`.

Load only the reference matching the task:

| Task | Read |
|---|---|
| Search, install, publish, or update a Skill ZIP | [`skills.md`](skills.md) |
| Search, install, create, probe, or update an MCP listing | [`mcp.md`](mcp.md) |
| Search, create, update, or publish an Expert (专家) or Squad (专家团) | [`expert.md`](expert.md) |

## Shared output rule

For a non-paginated command, normalize the backend payload before reading its
fields because CLI versions may either unwrap the backend envelope or retain it:

```bash
payload=$(octo-cli marketplace ... | jq -c '.data.data // .data')
```

Do not apply that expression to any command whose backend response is
`{data:[...], pagination:{...}}` — the CLI output layer flattens that shape,
exposing the result array directly as CLI `.data` and pagination as
`._pagination`. This covers the paginated `skill list` / `mcp list` search
commands **and** the offset-paginated `expert list` / `squad list` (and their
`mine` variants), regardless of whether the command supports `--page-all`.

## Shared safety rule

- Show the target object and intended mutation before install, publish, create,
  replacement, or update; continue only after explicit user confirmation.
- Never print presigned URLs, tokens, headers containing secrets, or raw secret
  values.
- Do not change records outside the user-selected Skill, MCP, Expert, or Squad.
- On failure, preserve the previously installed runtime configuration.
