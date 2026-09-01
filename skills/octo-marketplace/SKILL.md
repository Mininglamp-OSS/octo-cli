---
name: octo-marketplace
version: 0.6.0
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

## Versioning and visibility — read this before any write

After unification the backend has **no separate publish step**: every `plugin
upsert` (and `plugin import`) IS a version snapshot — each save appends an
auto-increment row to `plugin_versions`. The `plugin.version` field you pass is
the human-readable `current_version` label (defaults to `1.0.0`); the snapshot
label itself is server-assigned. A changelog note may be passed alongside
`version` on **import** (which finalizes through upsert).

A plugin is visible in scene-scoped lists (any `plugin list` with a non-empty
`--scene-code`, including `--mode mine`) only while it has a visible row in
`plugin_placements` for that scene. **Create and import attach the default
placement automatically**; **update self-heals a missing default placement** — so
a normal upsert/import leaves the plugin listable without a follow-up call. Do
not attempt to manipulate placements through the CLI.

If a gateway-timeout hits a write, the outcome is UNKNOWN (the CLI surfaces
`RESULT_UNKNOWN` rather than retrying, because create/upsert/install/delete are
non-idempotent). Before re-running, re-check `plugin list --scene-code default
--plugin-type <type> --mode mine --q "<name>"` to confirm the plugin does or does
not exist — that prevents duplicates.

## Pagination and filtering

`plugin list` is offset-paged: walk `--page` (with `--page-size`) yourself until
a short page. There is no `--page-all`. The server-side filters are only
`--category-id`, `--q`, `--tag` (repeatable), `--mode mine`, and `--sort`
(`newest`/`oldest`/`updated`/`name`/`placement`/`views`/`installs`/`downloads`/
`comprehensive`). Narrow anything else (e.g. by connector transport or
visibility) client-side over the returned rows.

`plugin-category list` accepts only `--scene-code` + `--plugin-type` (no mode,
pagination, or keyword); owned/provenance-scoped category counts from the
retired per-type surface are gone.

`plugin-tag list` accepts optional `--scene-code`, `--plugin-type`, `--q`,
`--mode mine`, and `--limit` (default 50, max 100). When `--scene-code` is
omitted tags are aggregated over all visible plugins regardless of scene.

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

- Show the target plugin and intended mutation before install, create,
  replacement, or delete; continue only after explicit user confirmation.
  (There is no separate "publish" step.)
- Never print presigned URLs, tokens, headers containing secrets, or raw secret
  values. User-supplied connector secrets are written as `${VAR}` placeholders.
- Do not change plugins outside the user-selected one.
- On failure, preserve the previously installed runtime configuration.
