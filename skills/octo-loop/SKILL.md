---
name: octo-loop
version: 0.1.0
description: "Use when operating the Octo Loop control plane through the octo-cli `loop` commands: reading or writing Fleet tasks, comments, metadata, projects, and labels; dispatching work to experts or expert-teams and watching or cancelling their executions; creating, triaging, updating status, or reading progress on a task; listing tasks, experts, or expert-teams in a workspace; and safely handling mention and status-change side effects. Terminology: task (was issue), expert (was agent), expert-team (was squad), dispatch (assign a task to an expert/expert-team and trigger its run). Trigger phrases include: create or report a task/bug, dispatch or schedule work, assign to an expert, run or rerun a task, update or read progress, comment on or reply to a task, list or search tasks in a Loop workspace. Do not use for chit-chat unrelated to Loop, for pure local file work, or when a task or issue clearly refers to GitHub/Jira rather than Loop. Load after octo-shared."
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# Octo Loop

Use the `loop` namespace for Fleet control-plane operations. Product names are
`task`, `expert`, `expert-team`, and `execution`; do not use the legacy
issue/agent/squad names.

Terminology (legacy → current): **agent → expert**, **squad → expert-team**,
**issue → task**, **assign a task to an expert/expert-team and trigger its run → dispatch**.

## When to use / not use

Use this skill when the request targets a specific Loop workspace or task:
reading or writing tasks, comments, projects, or metadata; working with experts
or expert-teams (dispatch, schedule, trigger a run); replying to a comment,
creating or triaging a task, updating status, or reading progress.

Do not use it for chat unrelated to Loop, for pure local file work, or when
"issue" clearly means GitHub/Jira rather than Loop.

## Authentication

Set `OCTO_TOKEN` (or its fully compatible legacy alias `OCTO_BOT_TOKEN`) to a
credential Fleet accepts directly, such as an Octo Session credential, `bf_`,
`uk_`, or `octo_loop_`. The CLI does not infer a
human, device, or execution principal from a token prefix. Fleet verifies the
credential and constructs the principal.

If a command fails with an auth error, check `octo-cli auth status` and let the
user run `octo-cli auth login` — do not fabricate credentials. Never leak or
store tokens, config keys, or API base URLs, and do not bypass workspace
permissions with direct HTTP calls.

All Loop commands use the same gateway as every other domain. The embedded
paths include the Fleet module namespace, so requests resolve under
`$OCTO_API_BASE_URL/fleet/api/v1/*`.

## Workspace UUID

Workspace-scoped Loop commands require the `--workspace-id <workspace-uuid>`
flag, sent as the `X-Workspace-ID` header. The value must be the UUID returned
by `workspace list`, not a workspace slug or name. There is no environment
fallback — pass it explicitly. Discover available workspaces first:

```bash
octo-cli loop workspace list
octo-cli loop workspace member list --workspace-id <workspace-uuid>
```

## Discover and execute operations

Output is JSON by default (`--format json`); parse JSON rather than scraping
tables.

```bash
octo-cli loop task list --workspace-id <workspace-uuid>
octo-cli loop task get <task-id> --workspace-id <workspace-uuid>
octo-cli loop expert list --workspace-id <workspace-uuid>
octo-cli loop expert-team list --workspace-id <workspace-uuid>
octo-cli loop execution message list <execution-id> --workspace-id <workspace-uuid>
```

Inspect the current contract before constructing a write:

```bash
octo-cli schema task.create
octo-cli schema task.comment.create
octo-cli schema expert.create
octo-cli schema expert_team.create
```

Repository operations, daemon device enrollment, task claim/heartbeat, and local
runtime paths are intentionally not part of this CLI surface.

## Read workflow

Read before you write.

```bash
octo-cli loop task get <task-id> --workspace-id <workspace-uuid>
octo-cli loop task comment list <task-id> --workspace-id <workspace-uuid>
octo-cli loop task metadata list <task-id> --workspace-id <workspace-uuid>
octo-cli loop task children-by-parent <task-id> --workspace-id <workspace-uuid>
```

Comment history is paginated with `--page` / `--page-size`.

Explore other namespaces (`project`, `expert`, `expert-team`,
`expert-template`, `execution`, `runtime`, `skill`, `skill-file`, `autopilot`,
`attachment`, `label`, top-level `comment`) with `--help` — their shapes vary.

## Write workflow

Treat writes as actions with side effects: creating comments, creating tasks,
changing status, assigning/dispatching, rerunning executions, mentioning an
expert or expert-team, and autopilot changes. If the user has not clearly asked
for a write, confirm before executing.

### Comment and description bodies go through a file

There is **no `--content-file` flag**. Body text is a request-body field, so
pass it via `--data`, which accepts `@file` (or `@-` for stdin). Do not build
the body inline with `--content "$(cat ...)"`: the shell rewrites backticks,
`$()`, variables, quotes, and newlines before the CLI sees them.

Write the body to a private temp file, wrap it as valid JSON (use `jq -Rs .` to
escape the text safely), and clean up:

```bash
body_dir="$(mktemp -d)"
trap 'rm -rf "$body_dir"' EXIT
# ...write the comment body to "$body_dir/reply.md", preserving real newlines...
printf '{"content": %s}' "$(jq -Rs . < "$body_dir/reply.md")" > "$body_dir/body.json"

octo-cli loop task comment create <task-id> --workspace-id <workspace-uuid> --data @body.json
```

Use `mktemp -d`, never a fixed path like `./reply.md` (it can clobber a user
file). To reply within a thread, add `--parent-comment-id <comment-id>`;
individual flags override same-named fields in `--data`.

Task descriptions follow the same pattern — `--description` is a plain string
flag with the same shell hazards, so prefer a JSON body from a file:

```bash
printf '{"title": %s, "description": %s}' \
  "$(jq -Rs . <<< 'Fix login redirect')" \
  "$(jq -Rs . < "$body_dir/description.md")" > "$body_dir/task.json"
octo-cli loop task create --workspace-id <workspace-uuid> --data @task.json
```

### Metadata

Metadata is persistent task state, not a log. Read it on entry and write only
high-value facts that will be read again (for example `pr_url`,
`pipeline_status`, `blocked_reason`). Inspect the contract first — the set path
is keyed by a metadata id:

```bash
octo-cli schema task.metadata.set
octo-cli loop task metadata list <task-id> --workspace-id <workspace-uuid>
```

## Status and assignment side effects

Status and assignment are done through `task update`, not a dedicated command:

```bash
octo-cli loop task update <task-id> --workspace-id <workspace-uuid> --status <status>
octo-cli loop task update <task-id> --workspace-id <workspace-uuid> \
  --assignee-id <uuid> --assignee-type expert   # or expert_team, member
```

Changing status is not cosmetic — it can start or stop work. In the standard
Fleet workflow `backlog` parks an assigned task, `in_progress` means work is in
progress, and `in_review` means the work is awaiting review. Use `done` only
after the work is accepted or no review is required; `done` and `cancelled` are
terminal. Moving out of `backlog` to an active status can trigger the assigned
expert to run. Confirm the exact status values accepted by your workspace via
the schema.

## Dispatch and executions

To create a task and dispatch it to an expert or expert-team in one call, use
`quick-create` with a `prompt` and an assignee id:

```bash
octo-cli loop task quick-create --workspace-id <workspace-uuid> \
  --prompt "Investigate the login redirect bug" --expert-id <uuid>
# or --expert-team-id <uuid>
```

Each dispatch produces an execution. Read run state and stop a runaway run
rather than guessing:

```bash
octo-cli loop task execution list <task-id> --workspace-id <workspace-uuid>
octo-cli loop task execution cancel <task-id> <execution-id> --workspace-id <workspace-uuid>
octo-cli loop task rerun <task-id> --workspace-id <workspace-uuid> --execution-id <execution-id>
```

Cancelling and rerunning are writes with side effects — confirm before running
them unless the user asked.

## Mention side effects

Comment bodies can contain mention links that Fleet interprets as actions, not
decoration. Mentioning an **expert** or **expert-team** triggers a run;
mentioning a person is a notification only. Look up real ids before composing a
mention:

```bash
octo-cli loop expert list --workspace-id <workspace-uuid>
octo-cli loop expert-team list --workspace-id <workspace-uuid>
octo-cli loop workspace member list --workspace-id <workspace-uuid>
```

Do not mention an expert or expert-team just to say thanks, confirm, or wrap up
— re-mentioning the responder can trigger a fresh run and create a loop. Confirm
the exact mention link syntax against the product before relying on it.

## External agent boundary

An external agent does not automatically inherit Loop context. When asked to act
on a task or comment, obtain or derive: the task id, the triggering comment id
and its parent (if replying), the target workspace, whether writes are allowed,
and whether mentions / status changes / reruns / dispatch are allowed. If any of
these is missing and the operation would write state, ask first. For read-only
investigation, gather context with JSON output and state what is still missing.
