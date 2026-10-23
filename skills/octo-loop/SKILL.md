---
name: octo-loop
version: 0.1.0
description: "Use when operating the Octo Loop control plane through the octo-cli `loop` commands: reading or writing Fleet tasks, comments, metadata, and labels; dispatching work to experts or expert-teams and watching or cancelling their executions; creating, triaging, updating status, or reading progress on a task; listing tasks, experts, or expert-teams in a workspace; and safely handling mention and status-change side effects. Terminology: task (was issue), expert (was agent), expert-team (was squad), dispatch (assign a task to an expert/expert-team and trigger its run). Trigger phrases include: create or report a task/bug, dispatch or schedule work, assign to an expert, run or rerun a task, update or read progress, comment on or reply to a task, list or search tasks in a Loop workspace. Do not use for chit-chat unrelated to Loop, for pure local file work, or when a task or issue clearly refers to GitHub/Jira rather than Loop. Load after octo-shared."
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
reading or writing tasks, comments, or metadata; working with experts
or expert-teams (dispatch, schedule, trigger a run); replying to a comment,
creating or triaging a task, updating status, or reading progress.

Do not use it for chat unrelated to Loop, for pure local file work, or when
"issue" clearly means GitHub/Jira rather than Loop.

## Authentication

Set `OCTO_TOKEN` (compatible alias `OCTO_BOT_TOKEN`) to a bearer credential Fleet accepts directly, such as
`bf_`, `uk_`, or `octo_loop_`. The CLI does not infer a
human, device, or execution principal from a token prefix. Fleet verifies the
credential and constructs the principal.

Outside daemon task mode, if a command fails with an auth error, check
`octo-cli auth status` and let the user run `octo-cli auth login`. In
`OCTO_CREDENTIAL_MODE=task`, those profile diagnostics are unavailable: report
the auth failure so the daemon can refresh or replace the injected credential.
For task workflow and collaboration in a daemon execution, select task mode
for each invocation: `OCTO_CREDENTIAL_MODE=task "$OCTO_CLI_PATH" loop ...`.
Task mode reads only `OCTO_TASK_TOKEN`; missing task credentials never fall
back to ordinary token aliases or host profiles. Independent business calls
keep ordinary authentication. Do not export mode for the session or switch
identity to retry an authentication failure. The standalone examples below
use ordinary mode; follow the daemon's scoped examples during its workflow.
Do not fabricate credentials. Never leak or
store tokens, config keys, or API base URLs, and do not bypass workspace
permissions with direct HTTP calls.

All Loop commands use the same gateway as every other domain. The embedded
paths include the Fleet module namespace, so requests resolve under
`$OCTO_API_BASE_URL/fleet/api/v1/*`.

## Workspace selector

Workspace-scoped Loop commands require the `--workspace-id <uuid>` flag (sent as
the `X-Workspace-ID` header). Use the Workspace UUID, not its Web slug. There is no environment fallback for it — pass it
explicitly. Discover available workspaces first:

```bash
octo-cli loop workspace list
octo-cli loop workspace member list <workspace-id> --workspace-id <workspace-id>
```

## Discover and execute operations

Output is JSON by default (`--format json`); parse JSON rather than scraping
tables.

```bash
octo-cli loop task list --workspace-id <ws>
octo-cli loop task search "oauth timeout" --workspace-id <ws>
octo-cli loop task get <task-id> --workspace-id <ws>
octo-cli loop expert list --workspace-id <ws>
octo-cli loop expert-team list --workspace-id <ws>
octo-cli loop execution message list <execution-id> --workspace-id <ws>
```

Task search takes one required query argument; quote queries containing spaces.
`--workspace-id`, `--page`, and `--page-size` remain flags. For a query that
starts with `-`, put flags before the `--` end-of-options marker:

```bash
octo-cli loop task search --workspace-id <ws> -- "-draft"
```

### Task selection and grouping

Task list and group accept common filters. Comma-separated query unions are
single scalar flag values, so quote them as one shell argument; a nonempty
`--statuses` takes precedence over `--status`, and a nonempty `--priorities`
takes precedence over `--priority`.

```bash
octo-cli loop task list --workspace-id <ws> \
  --statuses "in_progress,blocked" --assignee-types "expert,expert_team" \
  --assignee-filters "expert:<expert-uuid>,expert_team:<team-uuid>" \
  --label-ids "<label-uuid>" --sort due_date --direction asc
octo-cli loop task group --workspace-id <ws> --group-by assignee \
  --group-assignee-type expert --group-assignee-id <expert-uuid>
```

Identity selection supports `assignee_id(s)`, `assignee_octo_uid(s)` and
`assignee_filters`, with the corresponding `creator_id(s)`,
`creator_octo_uid(s)` and `creator_filters`. Actor ID filters take Fleet actor
UUIDs; Octo UID filters take exact member UIDs. Typed filters use
`<member|expert|expert_team>:<UUID>` entries.
`label_ids` is a comma-separated scalar union. `include_no_assignee` includes
unassigned tasks alongside an assignee filter; `involves_user_id` selects
tasks involving a workspace user through an expert or expert-team assignment.

`metadata` is one JSON string containing a flat object of primitive values
(for example `--metadata '{"source":"automation","reviewed":true}'`).
Supply `date_field` (`created_at` or `updated_at`), `date_start` and `date_end`
together; the start is inclusive and the end is exclusive. Task list alone
also accepts `open_only` and `scheduled`. Sorting accepts `position`, `title`,
`created_at`, `start_date`, `due_date` or `priority`, with `direction` `asc` or
`desc` (case-insensitive).

Grouping is by assignee. `group_assignee_type` can be `none`, `member`,
`expert` or `expert_team`; supply `group_assignee_id` for a specific actor and
omit it when the type is `none`. These filters are backend-validated. Do not
use retired project selectors or assume a task belongs to a project.

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
octo-cli loop task get <task-id> --workspace-id <ws>
octo-cli loop task comment list <task-id> --workspace-id <ws>
octo-cli loop task metadata list <task-id> --workspace-id <ws>
octo-cli loop task timeline list <task-id> --workspace-id <ws>
octo-cli loop task active-execution list <task-id> --workspace-id <ws>
octo-cli loop task execution list <task-id> --workspace-id <ws>
octo-cli loop task usage <task-id> --workspace-id <ws>
octo-cli loop task pull-request list <task-id> --workspace-id <ws>
octo-cli loop task label list <task-id> --workspace-id <ws>
octo-cli loop task child list <task-id> --workspace-id <ws>
octo-cli loop task child-progress --workspace-id <ws>
octo-cli loop expert execution list <expert-id> --workspace-id <ws>
# Batch child selection uses comma-separated parent UUIDs. Omitting this flag
# preserves the API's existing empty-collection response.
octo-cli loop task children-by-parent --workspace-id <ws> --parent-task-ids "<uuid-1>,<uuid-2>"
```

Comment history, timelines, execution lists, pull requests, labels, and children
are paginated with `--page` / `--page-size`. The child-progress operation's
current CLI schema does not describe a parent selector; check its help rather
than assuming one can be supplied.

## Attachment bytes

To save actual attachment bytes, pass `--output` (or `-o`) to either binary
content command:

```bash
octo-cli loop attachment content get <attachment-id> --workspace-id <ws> --output ./attachment.bin
octo-cli loop attachment download <attachment-id> --workspace-id <ws> --output ./attachment.bin
```

With `--output`, JSON stdout reports response metadata and the output path.
Without it, the CLI describes the binary response but saves no file. The flag
does not bypass workspace or attachment permissions.

Explore other namespaces (`expert`, `expert-team`,
`expert-template`, `execution`, `runtime`, `skill`, `skill-file`, `autopilot`,
`attachment`, `label`, top-level `comment`) with `--help` — their shapes vary.

## Write workflow

Treat writes as actions with side effects: creating comments, creating tasks,
changing status, assigning/dispatching, rerunning executions, mentioning an
expert or expert-team, and autopilot changes. If the user has not clearly asked
for a write, confirm before executing.

### Comment and description bodies go through a file

There is **no `--content-file` flag**. Pass comment text through `--data`
using `@file` (or `@-` for stdin), then clean up the temporary files:

```bash
body_dir="$(mktemp -d)"
trap 'rm -rf "$body_dir"' EXIT
# ...write the comment body to "$body_dir/reply.md", preserving real newlines...
printf '{"content": %s}' "$(jq -Rs . < "$body_dir/reply.md")" > "$body_dir/body.json"

octo-cli loop task comment create <task-id> --workspace-id <ws> --data "@$body_dir/body.json"
```

Quoting a command substitution such as `"$(cat "$body_dir/reply.md")"` passes
its output as one argument. The shell does not recursively evaluate `$()`,
backticks, quotes, variable-looking text, or other shell syntax in that output;
embedded newlines remain. Command substitution removes trailing newlines, so
use the JSON-file route when exact body text matters. JSON escaping with
`jq -Rs .` also avoids constructing a fragile inline JSON string.

Use `mktemp -d`, never a fixed path like `./reply.md` (it can clobber a user
file). To reply within a thread, add `--parent-comment-id <comment-id>`;
`--attachment-ids` and `--suppress-expert-ids` accept comma-separated UUIDs.
Individual flags override same-named fields in `--data`.

String arrays such as `attachment_ids` and `suppress_expert_ids` are promoted
to comma-separated flags. Object inputs such as `runtime_ref`, and arrays of
objects such as webhook `event_filters`, are not promoted and must stay inside
the JSON body passed through `--data`.

Task descriptions are available as the `--description` scalar flag. For
multiline or exact text, use a JSON body from a file; `printf '%s'` avoids the
extra newline added by a Bash here-string:

```bash
printf '{"title": %s, "description": %s}' \
  "$(printf '%s' 'Fix login redirect' | jq -Rs .)" \
  "$(jq -Rs . < "$body_dir/description.md")" > "$body_dir/task.json"
octo-cli loop task create --workspace-id <ws> --data "@$body_dir/task.json"
```

### Metadata

Metadata is persistent task state, not a log. Read it on entry and write only
high-value facts that will be read again (for example `pr_url`,
`pipeline_status`, `blocked_reason`). Inspect the contract first — the set path
is keyed by the persistent metadata key (for example, `pr_url`), not a separate
metadata-record ID; the request body supplies its `value`:

```bash
octo-cli schema task.metadata.set
octo-cli loop task metadata list <task-id> --workspace-id <ws>
```

## Status and assignment side effects

Task create/update accepts these priorities: `urgent`, `high`, `medium`, `low`,
and `none`. Status values are `backlog`, `todo`, `in_progress`, `in_review`,
`done`, `blocked`, and `cancelled`; inspect workspace guidance before choosing
a lifecycle transition.

```bash
octo-cli loop task update <task-id> --workspace-id <ws> --priority high
octo-cli loop task update <task-id> --workspace-id <ws> --status <status>
octo-cli loop task update <task-id> --workspace-id <ws> \
  --assignee-id <uuid> --assignee-type expert   # or expert_team, member
octo-cli loop task update <task-id> --workspace-id <ws> \
  --assignee-octo-uid <member-octo-uid> --assignee-type member
```

`assignee_octo_uid` is for member assignment in an authorized Octo Project. The
backend requires it to be paired with `assignee_type=member` and forbids
combining it with `assignee_id`; project authorization remains backend-enforced.
`assignee_id` normally means a Fleet actor UUID.

Task create/update also accept `--parent-task-id`, `--start-date`,
`--due-date`, and comma-separated `--attachment-ids`. On update, `--position`
sets manual task ordering and accepts fractional values. Explicit `null` in
`--data` clears a parent or date field, for example
`--data '{"start_date":null}'`.

Changing status is not cosmetic — it can start or stop work. In the standard
Fleet workflow `backlog` parks an assigned task, `in_progress` means work is in
progress, and `in_review` means the work is awaiting review. Use `done` only
after the work is accepted or no review is required; `done` and `cancelled` are
terminal. Moving out of `backlog` to an active status can trigger the assigned
expert to run.

Task update also accepts `--suppress-run`, which suppresses the run created by
that write. It does not stop existing executions from being cancelled when an
assignee changes. `--handoff-note` supplies optional context to Fleet's handoff
path. Assignment pairing, authorization, and exact run-trigger semantics remain
backend contracts.

## Dispatch and executions

To create a task and dispatch it to an expert or expert-team in one call, use
`quick-create` with a `prompt` and exactly one assignee id. A parent and existing
attachments are optional:

```bash
octo-cli loop task quick-create --workspace-id <ws> \
  --prompt "Investigate the login redirect bug" --expert-id <uuid> \
  --parent-task-id <parent-task-uuid> --attachment-ids "<attachment-uuid>"
# or --expert-team-id <uuid>
```

Each dispatch produces an execution. Inspect active/all task executions before
stopping or rerunning work:

```bash
octo-cli loop task active-execution list <task-id> --workspace-id <ws>
octo-cli loop task execution list <task-id> --workspace-id <ws>
octo-cli loop task execution cancel <task-id> <execution-id> --workspace-id <ws>
octo-cli loop task rerun <task-id> --workspace-id <ws> --execution-id <execution-id>
octo-cli loop expert execution list <expert-id> --workspace-id <ws>
octo-cli loop expert execution cancel <expert-id> --workspace-id <ws>
```

Expert execution cancellation requests cancellation for active executions
owned by that expert; it is not a substitute for selecting one task execution.
Cancelling and rerunning are writes with side effects — confirm before running
them unless the user asked.

## Autopilot mode and delivery controls

Autopilot creation requires an explicit `--dispatch-mode`: `create_task` creates
a Fleet task for a dispatch, while `direct_execution` dispatches without
creating a task. Do not choose a mode on the user's behalf:

```bash
octo-cli loop autopilot create --workspace-id <ws> --title "Daily review" \
  --assignee-id <expert-uuid> --dispatch-mode create_task
# For an expert-team, add --assignee-type expert_team and use its UUID.
```

Inspect the mode and trigger contracts before writing:

```bash
octo-cli schema autopilot.create
octo-cli schema autopilot.trigger_config.create
octo-cli loop autopilot trigger-config create <autopilot-id> --help
```

Trigger configuration supports `schedule` and `webhook` kinds; schedule
expressions use `cron_expression` and `timezone`. A task title template is only
used in `create_task` mode; `direct_execution` does not create a task. Updates
can change the mode, so review that effect before applying one. Webhook
`event_filters` contain objects and must be passed through `--data`, not
promoted scalar flags.

The existing controls include manual trigger, run and delivery inspection, and
delivery replay:

```bash
octo-cli loop autopilot trigger <autopilot-id> --workspace-id <ws>
octo-cli loop autopilot run list <autopilot-id> --workspace-id <ws>
octo-cli loop autopilot delivery list <autopilot-id> --workspace-id <ws>
octo-cli loop autopilot delivery replay <autopilot-id> <delivery-id> --workspace-id <ws>
octo-cli loop autopilot webhook-token rotate <autopilot-id> <trigger-id> --workspace-id <ws>
octo-cli loop autopilot signing-secret set <autopilot-id> <trigger-id> --help
```

Configuration changes, delivery replay, token rotation, and signing-secret
changes are sensitive writes; confirm before performing them. Use command help
for secret inputs, and never echo webhook secrets or tokens into logs or task
comments.


## Comment triggers and mentions

Comment bodies can contain mention links that Fleet interprets as actions, not
decoration. The comment preview resolves mentions and automation triggers
without creating the comment; inspect it before posting an action-triggering
body:

```bash
octo-cli loop task comment preview <task-id> --workspace-id <ws> --data "@$body_dir/body.json"
```

Comment create/preview accept `--parent-comment-id`, `--attachment-ids`, and
`--suppress-expert-ids`. Suppression asks Fleet not to trigger the selected
experts from that comment; it is not a blanket cancellation of existing or
other executions. Mentioning an **expert** or **expert-team** triggers a run;
mentioning a person is a notification only. Look up real ids before composing a
mention:

```bash
octo-cli loop expert list --workspace-id <ws>
octo-cli loop expert-team list --workspace-id <ws>
octo-cli loop workspace member list <ws> --workspace-id <ws>
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
