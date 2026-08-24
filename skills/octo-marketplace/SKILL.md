---
name: octo-marketplace
version: 0.5.0
description: Search, install, publish, and update Marketplace plugins — Skills, MCP connectors, and Experts/Squads (专家/专家团) — through the unified plugin API. Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-marketplace — unified plugin catalog

The marketplace is one backend with a single catalog surface: every asset is a
**plugin** with a `plugin_type`. All commands use
`$OCTO_API_BASE_URL/market/api/v1/plugins*`, authenticate with the active bot
profile, and send its Space context.

The four plugin types (the old per-type surface maps onto them):

| plugin_type | was | read via |
|---|---|---|
| `skill` | Skill ZIP | [`skills.md`](skills.md) |
| `connector` | MCP listing | [`mcp.md`](mcp.md) |
| `expert` | Expert (专家) | [`expert.md`](expert.md) |
| `expert_team` | Squad (专家团) | [`expert.md`](expert.md) |

One command family drives every type — pass `--plugin-type`:

```bash
octo-cli marketplace plugin list --scene-code default --plugin-type skill --q "<kw>"
octo-cli marketplace plugin get --plugin-id <id> --include-relations
```

`scene-code` is `default`. `plugin list` and `plugin-category list` require both
`--scene-code` and `--plugin-type`. Use the immutable `plugin_id`, never a name.

Load only the reference matching the task; each covers the type-specific detail
(connector `mcp.json` + probe, skill file tree + upload/import, expert/team
`AGENTS.md` + install).

## Shared output rule

For a non-paginated command, normalize the payload before reading fields because
CLI versions may unwrap the backend envelope or retain it:

```bash
payload=$(octo-cli marketplace plugin get --plugin-id <id> | jq -c '.data.data // .data')
```

Do not apply that to a `{data:[...], pagination:{...}}` response: `plugin list`
is flattened by the CLI output layer, exposing the item array directly at CLI
`.data` and pagination at `._pagination`.

## Shared safety rule

- Show the target plugin and intended mutation before install, publish, create,
  replacement, or delete; continue only after explicit user confirmation.
- Never print presigned URLs, tokens, headers containing secrets, or raw secret
  values. User-supplied connector secrets are written as `${VAR}` placeholders.
- Do not change plugins outside the user-selected one.
- On failure, preserve the previously installed runtime configuration.
