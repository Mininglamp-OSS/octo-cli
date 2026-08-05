---
name: octo-loop
description: Manage Fleet tasks, executions, experts, expert templates, and expert teams through the stable Loop API exposed by octo-cli.
---

# Octo Loop

Use the `loop` namespace for Fleet control-plane operations. Product names are
`task`, `expert`, `expert-team`, and `execution`; do not use the legacy
issue/agent/squad names.

## Authentication

Set `OCTO_BOT_TOKEN` to a credential Fleet accepts directly, such as an Octo
Session credential, `bf_`, `uk_`, or `octo_loop_`. The CLI does not infer a
human, device, or execution principal from a token prefix. Fleet verifies the
credential and constructs the principal.

All Loop commands use the same gateway as every other domain. The embedded
paths include the Fleet module namespace, so requests resolve under
`$OCTO_API_BASE_URL/fleet/api/v1/*`.

## Discover and execute operations

```bash
octo-cli loop task list
octo-cli loop task get <task-id>
octo-cli loop expert list
octo-cli loop expert-team list
octo-cli loop execution message list <execution-id>
```

Inspect the current contract before constructing a write:

```bash
octo-cli schema task.create
octo-cli schema expert.create
octo-cli schema expert_team.create
```

Use `--data` for complex request bodies. Repository operations, daemon device
enrollment, task claim/heartbeat, and local runtime paths are intentionally not
part of this CLI surface.
