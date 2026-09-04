---
name: octo-marketplace
version: 0.7.0
description: Search, install, publish, review, and update Marketplace plugins — Skills, MCP connectors, and Experts/Squads (专家/专家团) — through the unified plugin API. Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-marketplace — unified plugin catalog

The marketplace is one backend with a single catalog surface: every asset is a
**plugin** with a `plugin_type`. All commands authenticate with the active
credential and send its Space context; a `bf_*` User Bot is resolved to its owner
and authoritative Bot Space by the backend. Unified catalog operations use
`/market/api/v1/plugins*`; helper operations such as categories, uploads, probes,
and icon presigns use other routes under `/market/api/v1/*`.

| plugin_type | was | read via |
|---|---|---|
| `skill` | Skill ZIP | [`skills.md`](skills.md) |
| `connector` | MCP listing | [`mcp.md`](mcp.md) |
| `expert` | Expert (专家) | [`expert.md`](expert.md) |
| `expert_team` | Squad (专家团) | [`expert.md`](expert.md) |

```bash
octo-cli marketplace plugin list --scene-code default --plugin-type skill --q "<kw>"
octo-cli marketplace plugin get --plugin-id <id> --include-relations
```

`scene-code` is `default`. Catalog listing requires `--scene-code` and
`--plugin-type`; the owned all-types view is the exception:

```bash
octo-cli marketplace plugin list --scene-code default --mode mine
```

Use immutable `plugin_id` and `review_id` values, never names.

## Save, publish, and review lifecycle

Creating with `plugin upsert` or skill `plugin import` saves a **draft** and a
version snapshot; it does not expose the plugin to the Space catalog. Updates
preserve the current listing state: draft/delisted content remains editable,
published private content remains owner-only, and published Space content
rejects direct edits and must use review. Version labels use
`MAJOR.MINOR.PATCH` (each numeric component is 1–9 digits) and may not move
backward.

After verifying a saved draft, publish it explicitly:

```bash
octo-cli marketplace plugin publish --plugin-id <plugin-id> \
  --version 1.2.0 --changelog "What changed"
```

- A `private` plugin's listing state becomes published immediately, but it stays
  owner-only and is not exposed in the Space catalog.
- A `space` plugin opens a review request and remains a draft until a Space
  owner/admin approves it. Save the returned `review_id`.
- Read `display_status` as the user-facing state: `draft`, `pending_review`,
  `published`, `rejected`, or `delisted`. `listing_state` is the lower-level
  listing axis.

Applicants can inspect or cancel their requests:

```bash
octo-cli marketplace plugin review-request list --mode mine --status pending
octo-cli marketplace plugin review-request get <review-id>
octo-cli marketplace plugin review-request cancel <review-id>
```

Space owners/admins review the frozen submission, not mutable live draft data:

```bash
octo-cli marketplace plugin review-request list --mode space --status pending
octo-cli marketplace plugin review-request get <review-id>
octo-cli marketplace plugin review-request approve <review-id>
octo-cli marketplace plugin review-request reject <review-id> --reason "Explain the required correction"
```

A Space owner/admin can take published Space content down; this leaves the
plugin editable and eligible for a later publish:

```bash
octo-cli marketplace plugin delist --plugin-id <plugin-id> --reason "Policy or maintenance reason"
```

An already-published Space plugin cannot be edited through `upsert` or skill
`import`; the backend rejects that bypass. Submit the full frozen upgrade with
`review-request create`: a listed upgrade must carry either `parse_task_id` for
a freshly parsed skill archive or both `manifest_json` + `plugin_json` for
declared documents. Only an unlisted Space-intent first submission may omit
content and snapshot its live draft. A present `relations` array replaces the
frozen graph; omitting it inherits the live graph.

## Retry and recovery

Marketplace mutations are not transport-retried. A gateway failure may mean the
server committed the operation, so `RESULT_UNKNOWN` must be resolved by reading
state before retrying:

- create: walk `plugin list --scene-code default --mode mine --q "<name>"`
  through every page;
- update/import of an existing id: `plugin get --plugin-id <id>` and compare the
  intended version, hashes, and content; use `plugin version list` where useful;
- publish: `plugin get --plugin-id <id>` and inspect `display_status` /
  `review_id`, then list owned review requests;
- review decisions: re-read the review request;
- delist/delete: re-read the plugin.

`--q` matches `plugin_name` substrings only and a page defaults to 20 rows. Stop
only after an exact match or a short page.

## Pagination and filtering

`plugin list` uses `--page` / `--page-size` (default 20, max 100); there is no
`--page-all`. Filters are `--category-id`, `--q`, repeatable `--tag`, `--mode
mine`, and `--sort` (`newest`, `oldest`, `updated`, `name`, `placement`, `views`,
`installs`, `downloads`, `comprehensive`).

`plugin-category list` requires `--scene-code` and `--plugin-type`.
`plugin-tag list` accepts optional `--scene-code`, `--plugin-type`, `--q`,
`--mode mine`, and `--limit` (default 50, max 100).

Load only the reference matching the task: connector `mcp.json` + probe,
skill attachment tree + upload/import, or expert/team `AGENTS.md` + relations.

## Shared output rule

For a non-paginated command, normalize before reading fields:

```bash
payload=$(octo-cli marketplace plugin get --plugin-id <id> | jq -c '.data.data // .data')
```

Do not apply that to `{data:[...], pagination:{...}}`; the CLI exposes items at
`.data` and pagination at `._pagination`.

## Shared safety rule

- Show the target and intended mutation before create, update, publish, review
  decision, delist, install, or delete; continue only after explicit user
  confirmation.
- Never print presigned URLs, tokens, secret-bearing headers, or raw secret
  values. Connector secrets use `${VAR}` placeholders.
- Do not modify a plugin or review request outside the user-selected target.
- Preserve the prior runtime configuration if installation fails.
