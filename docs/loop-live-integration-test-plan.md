# octo-cli Loop live integration test plan

Status: designed, environment execution pending

Target branch: `feat/loop-integration-test`

This plan tests the real `octo-cli loop ...` command surface against a deployed
Fleet service. It is intentionally separate from unit tests and `--dry-run`
tests.

## 1. Objective

Prove that the compiled CLI can use a real credential to:

1. route all Loop commands to the deployed Fleet endpoint;
2. authenticate and receive the expected public API envelope;
3. create, read, update and clean up Loop resources;
4. preserve the public task/expert/expert-team vocabulary;
5. enforce principal type, Space isolation and permission boundaries;
6. handle pagination, validation, not-found, conflict, rate-limit and retry
   responses consistently.

Passing local Go tests or matching 61 OpenAPI operation IDs is necessary but
does not satisfy this plan.

## 2. Safety and environment requirements

Run only in a dedicated test Space. Never run the write suite in production.

Required environment variables:

```bash
export OCTO_CLI_BIN="$(pwd)/bin/octo-cli"
export OCTO_API_BASE_URL="<test-octo-base-url>"
export OCTO_BOT_TOKEN="<non-production-test-credential>"
export OCTO_CONFIG_DIR="$(mktemp -d)"

# Pre-provisioned fixture IDs. See the dependency table below.
export LOOP_TEST_RUNTIME_ID="<runtime-uuid>"
export LOOP_TEST_TEMPLATE_ID="<template-id>"       # optional
export LOOP_TEST_SKILL_ID="<skill-uuid>"           # optional
export LOOP_TEST_LABEL_ID="<label-uuid>"           # optional
```

For isolation tests, provide a second identity from another Space through a
separate protected environment, not a command argument:

```bash
export LOOP_TEST_OTHER_SPACE_TOKEN="<non-production-other-space-credential>"
```

Rules:

- Do not pass credentials on argv.
- Do not write credentials to Git, reports, fixtures or command logs.
- Do not use `set -x`.
- Use a fresh `OCTO_CONFIG_DIR` for every run.
- Prefix every resource name with a unique run ID.
- Record only masked headers and sanitized response bodies.

## 3. Fixture dependency graph

```text
test Space + identity
        |
        +---- Task basic lifecycle (no runtime required)
        |
        +---- pre-existing runtime_id
                  |
                  +---- Expert A
                  |       |
                  |       +---- Expert Team
                  |       |       |
                  |       |       +---- Team-assigned Task
                  |       |
                  |       +---- Expert-assigned Task
                  |               |
                  |               +---- Execution (requires online daemon)
                  |
                  +---- Expert from template (optional template fixture)
```

| Fixture | Source | Required for | Cleanup |
|---|---|---|---|
| Test identity and Space | Test environment | All tests | Revoke after campaign if temporary |
| Runtime | Web/daemon provisioning or test seed | Expert create | Environment-owned |
| Expert template | `expert-template list` | Template create | Read-only |
| Skill | Test seed/catalog | Expert skill mutation | Removed by expert cleanup/archive policy |
| Label | Test seed | Task label mutation | Task deletion cascades association |
| Online daemon | Test deployment | Execution state and messages | Stop after campaign |
| Second-Space identity | Test environment | Cross-Space denial | Revoke after campaign if temporary |

The public Loop API does not create runtimes, skills or labels. Their IDs must
be provisioned before the dependent test level starts.

## 4. Runner design

Implement the runner as `scripts/integration/loop-live.sh` using Bash and
`jq`. The runner must:

- use `set -euo pipefail`, but capture expected non-zero cases explicitly;
- generate `RUN_ID="cli-it-$(date +%Y%m%d%H%M%S)-$RANDOM"`;
- capture every JSON response before extracting identifiers;
- assert `.ok == true` for success cases;
- assert exit code and `.error.code` for failure cases;
- register cleanup actions immediately after each successful create;
- run cleanup in reverse dependency order through `trap cleanup EXIT`;
- classify each case as `PASS`, `FAIL`, `BLOCKED` or `SKIP`;
- continue independent levels when one optional fixture is unavailable;
- never print the unmasked environment.
- avoid shell variables named `PATH` or `path`; zsh treats lowercase `path` as
  a special array tied to command lookup.

Suggested result layout:

```text
test-results/loop-live/<run-id>/
  summary.json
  junit.xml
  report.html
  commands.log                 # sanitized; no token or raw auth header
  responses/<case-id>.json     # sanitized API envelopes
```

Basic assertion helpers:

```bash
run_cli() {
  case_id="$1"
  shift
  output_file="$RESULT_DIR/responses/$case_id.json"
  set +e
  "$OCTO_CLI_BIN" "$@" >"$output_file" 2>&1
  rc=$?
  set -e
  return "$rc"
}

assert_ok() {
  jq -e '.ok == true' "$1" >/dev/null
}

assert_error() {
  file="$1" expected_code="$2"
  jq -e --arg code "$expected_code" \
    '.ok == false and .error.code == $code' "$file" >/dev/null
}
```

## 5. Execution levels

### Level 0: preflight and read-only discovery

These cases must run before any mutation.

| ID | Command | Assertion |
|---|---|---|
| LIVE-000 | `octo-cli version` | Expected test version |
| LIVE-001 | `octo-cli schema --list loop` | 61 operations |
| LIVE-002 | `octo-cli loop task list --page 1 --page-size 2` | Success envelope and pagination |
| LIVE-003 | `octo-cli loop expert list --page 1 --page-size 2` | Success envelope |
| LIVE-004 | `octo-cli loop expert-template list --page 1 --page-size 2` | Success envelope |
| LIVE-005 | `octo-cli loop expert-team list --page 1 --page-size 2` | Success envelope |
| LIVE-006 | Repeat one list with `--no-retry` | Same business response |

Gate: stop all write levels if authentication, Space scope or base URL is
incorrect.

### Level 1: Task core lifecycle

This level does not require a runtime or daemon.

```bash
create_response=$(
  "$OCTO_CLI_BIN" loop task create --data "$(jq -nc \
    --arg title "$RUN_ID task" \
    --arg description "octo-cli live integration fixture" \
    '{title:$title,description:$description,priority:"normal"}')"
)
TASK_ID=$(jq -er '.data.task_id' <<<"$create_response")
```

Cases:

| ID | Action | Required assertion |
|---|---|---|
| LIVE-100 | Create task | HTTP success; `.data.task_id` non-empty |
| LIVE-101 | Get task | Returned ID/title match fixture |
| LIVE-102 | Update title and description | Follow-up get returns new values |
| LIVE-103 | Call search endpoint | Valid collection; targeted query is blocked by the current contract |
| LIVE-104 | List timeline | Valid collection envelope |
| LIVE-105 | Create comment | Comment returned or visible in list |
| LIVE-106 | List comments | Fixture comment present |
| LIVE-107 | Subscribe | Success; subscriber list contains actor |
| LIVE-108 | Unsubscribe | Actor no longer present, or API confirms removal |
| LIVE-109 | Add reaction | Success |
| LIVE-110 | Remove reaction | Success and idempotency semantics recorded |
| LIVE-111 | Set metadata | List returns exact JSON value |
| LIVE-112 | Delete metadata | Entry absent afterwards |
| LIVE-113 | Add/list/remove label | Run only when `LOOP_TEST_LABEL_ID` exists |
| LIVE-114 | Get usage/attachments/pull requests | Valid empty or populated envelopes |
| LIVE-115 | Delete task | Success |
| LIVE-116 | Get deleted task | exit non-zero and `NOT_FOUND` |

Task deletion is the primary cleanup action and should remove task-owned test
comments, metadata, reactions and label associations.

### Level 2: Expert lifecycle

Requires `LOOP_TEST_RUNTIME_ID`.

```bash
expert_response=$(
  "$OCTO_CLI_BIN" loop expert create --data "$(jq -nc \
    --arg name "$RUN_ID expert" \
    --arg runtime "$LOOP_TEST_RUNTIME_ID" \
    '{name:$name,runtime_id:$runtime,description:"CLI integration fixture"}')"
)
EXPERT_ID=$(jq -er '.data.expert.expert_id // .data.expert_id' \
  <<<"$expert_response")
```

| ID | Action | Required assertion |
|---|---|---|
| LIVE-200 | Create expert | Expert ID returned; runtime matches fixture |
| LIVE-201 | Get expert | Public vocabulary uses `expert_id` |
| LIVE-202 | Update expert | Name/description persisted |
| LIVE-203 | Get/update environment | `custom_env` round trip; use non-secret values only |
| LIVE-204 | List/add/replace skills | Run only with `LOOP_TEST_SKILL_ID` |
| LIVE-205 | List expert executions | Valid collection |
| LIVE-206 | Archive expert | Archived state visible |
| LIVE-207 | Restore expert | Active state visible |
| LIVE-208 | Create from template | Run only with template and runtime fixtures |

There is no public expert delete endpoint. Final cleanup archives created
experts. Therefore this suite must use a dedicated test Space or a backend
fixture cleanup job.

### Level 3: Expert team lifecycle

Requires Expert A. Create Expert B when member mutation is tested.

```bash
team_response=$(
  "$OCTO_CLI_BIN" loop expert-team create --data "$(jq -nc \
    --arg name "$RUN_ID team" --arg leader "$EXPERT_ID" \
    '{name:$name,leader_expert_id:$leader,description:"CLI integration fixture"}')"
)
TEAM_ID=$(jq -er '.data.expert_team_id' <<<"$team_response")
```

| ID | Action | Required assertion |
|---|---|---|
| LIVE-300 | Create team | Team ID and leader Expert ID returned |
| LIVE-301 | Get/update team | Public fields persisted |
| LIVE-302 | List members | Leader visible as `member_type=expert` |
| LIVE-303 | Add Expert B | Member appears with requested role |
| LIVE-304 | Update Expert B role | New role visible |
| LIVE-305 | Read member statuses | Valid public task/expert vocabulary |
| LIVE-306 | Remove Expert B | Member absent afterwards |
| LIVE-307 | Delete team | Success; subsequent get is `NOT_FOUND` |

For member add/update, send an explicit body even though the current CLI schema
display is incomplete:

```json
{"member_type":"expert","member_id":"<expert-b-id>","role":"member"}
```

### Level 4: Assignment and execution

Requires an online daemon/runtime capable of consuming assigned work.

| ID | Action | Required assertion |
|---|---|---|
| LIVE-400 | Create task assigned to Expert | Task stores expert assignment |
| LIVE-401 | Create task assigned to Expert Team | Task stores team assignment |
| LIVE-402 | Poll active executions | Execution appears before timeout |
| LIVE-403 | List task executions | Public execution fields returned |
| LIVE-404 | Read execution messages | Valid ordered collection |
| LIVE-405 | Cancel execution | Terminal/cancelled state observed |
| LIVE-406 | Rerun execution | New execution ID differs from original |
| LIVE-407 | Expert execution list | Execution references correct expert/task |

Use bounded polling, for example 60 seconds with a 2-second interval. A timeout
is `BLOCKED` when daemon health is unavailable, not automatically a CLI defect.

### Level 5: negative authentication and isolation

| ID | Scenario | Expected result |
|---|---|---|
| LIVE-500 | No token | CLI exit 3, `UNAUTHORIZED` |
| LIVE-501 | Invalid token | Auth failure; no retry storm |
| LIVE-502 | Revoked/expired token | Auth failure with stable error envelope |
| LIVE-503 | Read first-Space task using second-Space token | `NOT_FOUND` or `FORBIDDEN`, never data |
| LIVE-504 | Mutate first-Space task using second-Space token | Rejected; original data unchanged |
| LIVE-505 | Device/execution principal invokes Human-only operation | `FORBIDDEN` |
| LIVE-506 | Loop request with local `OCTO_SPACE_ID` set | Server scope still comes from principal; no scope escape |

Do not infer cross-Space correctness from list returning an empty page; create a
known first-Space fixture and address it directly from the second identity.

### Level 6: transport and error behavior

| ID | Scenario | Expected result |
|---|---|---|
| LIVE-600 | Missing required body field | CLI validation after known defect is fixed; otherwise Fleet 4xx recorded |
| LIVE-601 | Unknown resource | Stable `NOT_FOUND` envelope and non-zero exit |
| LIVE-602 | Invalid UUID/path ID | Validation or 4xx; no panic |
| LIVE-603 | Page size boundary | Valid boundary accepted, invalid rejected |
| LIVE-604 | Fleet 429 | Retry-After honored unless `--no-retry` |
| LIVE-605 | Transient 503 | Bounded retry then success/final error |
| LIVE-606 | Timeout | Deterministic timeout envelope and non-zero exit |
| LIVE-607 | Token masking | Complete credential absent from all artifacts |

429/503 injection should use a controlled test proxy or Fleet fault-injection
mode, not production traffic.

## 6. Canonical real CLI command set

The runner should invoke the public command surface directly. These are real
calls: do not add `--dry-run`.

### Task commands

```bash
"$OCTO_CLI_BIN" loop task create --data "$TASK_CREATE_BODY"
"$OCTO_CLI_BIN" loop task get "$TASK_ID"
"$OCTO_CLI_BIN" loop task update "$TASK_ID" --data "$TASK_UPDATE_BODY"
"$OCTO_CLI_BIN" loop task search --page 1 --page-size 20
"$OCTO_CLI_BIN" loop task timeline list "$TASK_ID"

"$OCTO_CLI_BIN" loop task comment create "$TASK_ID" \
  --content "$RUN_ID comment"
"$OCTO_CLI_BIN" loop task comment list "$TASK_ID"

"$OCTO_CLI_BIN" loop task subscribe "$TASK_ID"
"$OCTO_CLI_BIN" loop task subscriber list "$TASK_ID"
"$OCTO_CLI_BIN" loop task unsubscribe "$TASK_ID"

"$OCTO_CLI_BIN" loop task reaction add "$TASK_ID" --emoji "thumbsup"
"$OCTO_CLI_BIN" loop task reaction remove "$TASK_ID" --emoji "thumbsup"

METADATA_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
"$OCTO_CLI_BIN" loop task metadata set "$TASK_ID" "$METADATA_ID" \
  --data "$(jq -nc --arg run "$RUN_ID" '{value:{integration_run:$run}}')"
"$OCTO_CLI_BIN" loop task metadata list "$TASK_ID"
"$OCTO_CLI_BIN" loop task metadata delete "$TASK_ID" "$METADATA_ID"

"$OCTO_CLI_BIN" loop task label add "$TASK_ID" \
  --label-id "$LOOP_TEST_LABEL_ID"
"$OCTO_CLI_BIN" loop task label list "$TASK_ID"
"$OCTO_CLI_BIN" loop task label remove "$TASK_ID" "$LOOP_TEST_LABEL_ID"

"$OCTO_CLI_BIN" loop task usage "$TASK_ID"
"$OCTO_CLI_BIN" loop task attachment list "$TASK_ID"
"$OCTO_CLI_BIN" loop task pull-request list "$TASK_ID"
"$OCTO_CLI_BIN" loop task delete "$TASK_ID"
```

### Expert commands

```bash
"$OCTO_CLI_BIN" loop expert create --data "$EXPERT_CREATE_BODY"
"$OCTO_CLI_BIN" loop expert get "$EXPERT_ID"
"$OCTO_CLI_BIN" loop expert update "$EXPERT_ID" --data "$EXPERT_UPDATE_BODY"

"$OCTO_CLI_BIN" loop expert environment get "$EXPERT_ID"
"$OCTO_CLI_BIN" loop expert environment update "$EXPERT_ID" \
  --data '{"custom_env":{"OCTO_CLI_INTEGRATION":"true"}}'

"$OCTO_CLI_BIN" loop expert skill add "$EXPERT_ID" \
  --data "$(jq -nc --arg id "$LOOP_TEST_SKILL_ID" '{skill_ids:[$id]}')"
"$OCTO_CLI_BIN" loop expert skill list "$EXPERT_ID"
"$OCTO_CLI_BIN" loop expert skill replace "$EXPERT_ID" \
  --data '{"skill_ids":[]}'

"$OCTO_CLI_BIN" loop expert execution list "$EXPERT_ID"
"$OCTO_CLI_BIN" loop expert archive "$EXPERT_ID"
"$OCTO_CLI_BIN" loop expert restore "$EXPERT_ID"

"$OCTO_CLI_BIN" loop expert create-from-template \
  --data "$EXPERT_FROM_TEMPLATE_BODY"
```

### Expert-team commands

```bash
"$OCTO_CLI_BIN" loop expert-team create --data "$TEAM_CREATE_BODY"
"$OCTO_CLI_BIN" loop expert-team get "$TEAM_ID"
"$OCTO_CLI_BIN" loop expert-team update "$TEAM_ID" --data "$TEAM_UPDATE_BODY"
"$OCTO_CLI_BIN" loop expert-team member list "$TEAM_ID"

TEAM_MEMBER_BODY=$(jq -nc --arg id "$EXPERT_B_ID" \
  '{member_type:"expert",member_id:$id,role:"member"}')
"$OCTO_CLI_BIN" loop expert-team member add "$TEAM_ID" \
  --data "$TEAM_MEMBER_BODY"
"$OCTO_CLI_BIN" loop expert-team member update "$TEAM_ID" \
  --data "$TEAM_MEMBER_BODY"
"$OCTO_CLI_BIN" loop expert-team member-status list "$TEAM_ID"
"$OCTO_CLI_BIN" loop expert-team member remove "$TEAM_ID" \
  --data "$(jq -nc --arg id "$EXPERT_B_ID" \
    '{member_type:"expert",member_id:$id}')"
"$OCTO_CLI_BIN" loop expert-team delete "$TEAM_ID"
```

### Execution commands

```bash
"$OCTO_CLI_BIN" loop task active-execution list "$TASK_ID"
"$OCTO_CLI_BIN" loop task execution list "$TASK_ID"
"$OCTO_CLI_BIN" loop execution message list "$EXECUTION_ID"
"$OCTO_CLI_BIN" loop task execution cancel "$TASK_ID" "$EXECUTION_ID"
"$OCTO_CLI_BIN" loop execution cancel "$EXECUTION_ID"
"$OCTO_CLI_BIN" loop task rerun "$TASK_ID" \
  --data "$(jq -nc --arg id "$EXECUTION_ID" '{execution_id:$id}')"
```

Before implementing the runner, execute `--help` for every leaf command and pin
the command syntax in a shell test. This prevents an OpenAPI operation rename
from silently changing the generated Cobra tree.

## 7. Cleanup order

Cleanup runs even when assertions fail:

1. cancel any active test executions;
2. delete test tasks;
3. remove non-leader team members;
4. delete expert teams;
5. archive created experts;
6. stop or release dedicated runtime/daemon fixtures;
7. remove temporary config and unredacted temporary response files.

Every cleanup result appears in the report. Cleanup failure changes the run
status to `FAIL-CLEANUP` and prints the resource IDs for an operator, but never
prints credentials.

## 8. Known blockers before claiming full coverage

1. **CLI required-body validation:** `task.create --data '{}'` currently passes
   local dry-run despite `title` being required. The live suite must record the
   Fleet response until CLI validation is fixed.
2. **Expert-team member schema:** the OpenAPI schema uses `allOf`; the CLI schema
   loader currently shows an empty request body for member add/update. Explicit
   `--data` still transports the body, but schema discovery/validation coverage
   is incomplete.
3. **Runtime fixture:** expert creation cannot run without a valid runtime.
4. **Execution fixture:** execution behavior cannot run without an online
   daemon consuming tasks.
5. **Expert cleanup:** public API can archive but cannot delete experts.
6. **Task search parameters:** `task.search` currently exposes only `page` and
   `page_size`; no keyword or filter parameter is defined, so the live suite
   cannot perform a deterministic run-ID search.

## 9. Pass criteria

The release gate is satisfied only when:

- all mandatory Level 0 and Level 1 cases pass;
- Levels 2 and 3 pass with provisioned runtime fixtures;
- Level 4 passes with a healthy online daemon;
- cross-Space read and write attempts are rejected;
- all created tasks and teams are deleted and experts archived;
- no full credential appears in output or reports;
- no unexpected 5xx, panic, malformed envelope or vocabulary leak occurs;
- the two known CLI schema/validation gaps are fixed or explicitly accepted as
  release exceptions;
- the generated report contains commands, exit codes, sanitized responses,
  timings, cleanup results and fixture IDs.

Final statuses:

- `PASS`: all mandatory and provisioned optional levels pass.
- `CONDITIONAL PASS`: core levels pass; optional fixture-dependent levels are
  `SKIP` with approved reasons.
- `FAIL`: assertion, security, cleanup or unexpected server error.
- `BLOCKED`: Fleet, identity, runtime or daemon prerequisite is unavailable.
