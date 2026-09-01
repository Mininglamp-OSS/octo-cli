# octo-marketplace — MCP connector workflows

Read `SKILL.md` first for authentication, payload normalization, versioning, the
no-separate-publish rule, and the shared safety rule. MCP listings are
`plugin_type=connector`: no downloadable archive or release version. The
connection lives in the package's root `mcp.json` (standard
`{"mcpServers":…}`); user-supplied secret values appear as `${VAR}`
placeholders, never real secrets.

## Search and inspect

```bash
octo-cli marketplace plugin-category list --scene-code default --plugin-type connector
octo-cli marketplace plugin list --scene-code default --plugin-type connector \
  --q "<keywords>" --sort comprehensive --page 1 --page-size 20
octo-cli marketplace plugin list --scene-code default --plugin-type connector --mode mine
octo-cli marketplace plugin get --plugin-id <plugin-id>
```

`plugin list` is page-paginated (CLI `.data` + `._pagination`). Filter with
repeatable `--tag`; `--category-id` is scalar (not repeatable). `plugin get`
returns the connector descriptor (`plugin_json.connector`) plus the root
`mcp.json`.

## Install into an Agent Runtime

Installation adds the connector's `mcp.json` server entry to the runtime's MCP
configuration. Before writing config, show the connector name and transport, the
target runtime and config file, the command or URL, and the required
environment-variable / header names (never secret values).

After confirmation, preserve existing entries unless replacement was approved.
Copy the `mcpServers` entry from `mcp.json`. Any value written as `${VAR}` is a
consumer-supplied secret: ask for it locally, never invent or echo it, and
persist it only through the runtime's approved secret mechanism. Reload the
runtime and verify the server connects.

## Validate a connection

For `streamable-http` and `sse`, probe without persisting (retained endpoint):

```bash
octo-cli marketplace mcp probe --data @connection.json
```

A newer connector-namespaced alias is also available server-side at
`POST /api/v1/connectors/_probe`; the `marketplace mcp probe` command targets
`/mcps/_probe` which the backend retains.

`connection.json` carries only probe fields: `transport`, `url`, `command`,
`args`, `env`, `headers`. Use empty values for consumer-supplied secrets; do not
send real secrets just to validate. Continue only when the normalized response
has `is_ok=true`.

Do not probe `stdio`: the server does not spawn user commands. The runtime must
start the command locally, complete MCP `initialize` and `tools/list`, then stop.

## Create / update

Create or edit a connector plugin through the unified write path. Every save is
a version snapshot and attaches/self-heals the default market placement, so a
created connector is listable immediately — no follow-up publish step. Show the
target and intended change, then continue only after confirmation:

```bash
octo-cli marketplace plugin upsert --data @plugin.json
```

The `--data` body is `{"plugin":{plugin_name, plugin_type:"connector",
visibility, category_id, tags, icon, version?, manifest_json, plugin_json}}`.
Both `manifest_json` and `plugin_json` are required on every write. The
`plugin_json` must follow the connector contract: a top-level
`connector:{type,source}` descriptor plus a root `mcp.json`, with every
consumer-filled value written as a `${VAR}` placeholder (never a real secret).
Set `plugin.plugin_id` to update; omit to create. Validate changed connection
fields with `mcp probe` first.

Re-read with `plugin get --plugin-id <id>` and verify the `${VAR}` placeholders
round-trip. Delete only after showing the owned record and confirming:

```bash
octo-cli marketplace plugin delete --plugin-id <id>
```

Icon upload uses the presigned target from `marketplace mcp-icon-upload create`;
pass the returned persistent icon URL through `plugin upsert`'s `plugin.icon`.
