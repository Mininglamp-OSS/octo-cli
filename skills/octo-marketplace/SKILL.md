---
name: octo-marketplace
version: 0.3.0
description: Search, install, publish, and update Marketplace Skills and MCP server listings. Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-marketplace — Skills and MCP servers

All commands use `$OCTO_API_BASE_URL/market/api/v1/*`, authenticate with the
active bot profile, and send its Space context. Marketplace is one backend with
two catalog domains, exposed as `marketplace skill` and `marketplace mcp`.

Load only the reference matching the task:

| Task | Read |
|---|---|
| Search, install, publish, or update a Skill ZIP | [`skills.md`](skills.md) |
| Search, install, create, probe, or update an MCP listing | [`mcp.md`](mcp.md) |

## Shared output rule

For a non-paginated command, normalize the backend payload before reading its
fields because CLI versions may either unwrap the backend envelope or retain it:

```bash
payload=$(octo-cli marketplace ... | jq -c '.data.data // .data')
```

Do not apply that expression to paginated search commands: `skill list` and
`mcp list` expose their result array directly as CLI `.data` and pagination as
`._pagination`.

## Shared safety rule

- Show the target object and intended mutation before install, publish, create,
  replacement, or update; continue only after explicit user confirmation.
- Never print presigned URLs, tokens, headers containing secrets, or raw secret
  values.
- Do not change records outside the user-selected Skill or MCP.
- On failure, preserve the previously installed runtime configuration.
