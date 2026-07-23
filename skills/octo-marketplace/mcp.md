# octo-marketplace — MCP workflows

Read `SKILL.md` first for authentication, payload normalization, and confirmation
rules. MCP listings have no downloadable archive or release version.

## Search and inspect

```bash
octo-cli marketplace mcp-category list --mode all
octo-cli marketplace mcp list --keyword "<keywords>" --sort relevance --page 1 --page-size 20
octo-cli marketplace mcp mine list --page 1 --page-size 20
octo-cli marketplace mcp get <mcp-id>
```

Search is paginated: read results from CLI `.data[]`.
The category command is non-paginated; normalize it and use a returned `key` as
the MCP `category` value or list filter. MCP tags are free-form strings; there
is no tag dictionary endpoint.

Search filters include repeatable/comma-separated `transport`, `visibility`,
`source`, `created-by-type`, and `tag`. `mcp-category list --mode mine` returns
counts for owned records; `--created-by-type` narrows provenance.

`key: "all"` is the list-filter sentinel and `key: ""` means uncategorized;
do not store either as a new listing's category. Category keys are derived from
visible MCP records rather than managed as a separate administrator dictionary.

## Install into an Agent Runtime

Installation means adding the detail response's `quick_start` connection to the
runtime's MCP configuration.

Before writing configuration, show:

- MCP name and transport;
- target runtime and config file;
- command or URL;
- required environment-variable and header names, without secret values.

After confirmation, preserve existing entries unless replacement was approved.
Use `quick_start.slug` as the `mcpServers` key. Map stdio to
`command`/`args`/`env`, and HTTP/SSE to `url`/`headers`. Treat keys listed in
`quick_start.env_user_supplied` and `quick_start.headers_user_supplied` as
consumer-provided secrets: ask for them locally, never invent or echo them, and
persist them only through the runtime's approved secret mechanism. Reload the
runtime and verify the server connects.

## Validate a connection

For `streamable-http` and `sse`, probe without persisting:

```bash
octo-cli marketplace mcp probe --data @connection.json
```

Build `connection.json` with only the probe fields: `transport`, `url`,
`command`, `args`, `env`, and `headers`. The probe endpoint rejects catalog
fields and `env_user_supplied` / `headers_user_supplied` as unknown fields.
Use empty values for consumer-supplied secrets; do not send real secrets just
to validate a public connection.

Normalize the response and continue only when `is_ok=true`.

Do not call the Marketplace probe endpoint for `stdio`: the server deliberately
does not spawn user commands. The Agent Runtime must start the proposed command
locally, complete MCP `initialize` and `tools/list`, then stop the test process.

## Create

Create only after the applicable remote or local validation succeeds and the
user reviews tools and visibility:

```bash
octo-cli marketplace mcp create --data @mcp.json
```

The body is flat: catalog metadata plus `transport`, and either
`command`/`args`/`env` for stdio or `url`/`headers` for HTTP/SSE. Put every key
that each consumer must fill locally in `env_user_supplied` or
`headers_user_supplied`; submit an empty value for those keys instead of a real
secret. The owner can round-trip submitted values, while non-owner detail reads
blank them. `category` must be a key returned by `mcp-category list`; `tags` is
a free-form string array.

## Update

Updates are partial and do not create versions. Validate changed connection
fields first, then update only after confirmation:

```bash
# HTTP/SSE validation when connection fields changed
octo-cli marketplace mcp probe --data @connection.json

octo-cli marketplace mcp update <mcp-id> --data @changes.json
```

Category and tag changes use the same `category` key and free-form `tags`
string array as create.

For stdio connection changes, use the local handshake instead of backend probe.
When changing `env` or `headers`, update the matching `*_user_supplied` list in
the same request. Re-read with `marketplace mcp get <mcp-id>` and verify those
lists round-trip and `quick_start` contains no `version` field. Do not print
owner-visible secret values.

Use `marketplace mcp delete <mcp-id>` only after showing the selected owned
record and obtaining explicit confirmation. MCP icon uploads use the presigned
target from `marketplace mcp-icon-upload create`; after uploading, pass the
returned persistent icon URL through `mcp create` or `mcp update`.
