# Loop integration test plan

Status: test execution pending

The executable, real-Fleet integration design is documented separately in
`loop-live-integration-test-plan.md`. This file remains the broader release
test matrix.

Test branch: `feat/loop-integration-test`

The branch contains only the Fleet public API command integration from
`feature/loop-business-migration`. The previously proposed reusable public Go
client extraction was cancelled and is not part of this branch.

## Scope

The CLI exposes the Fleet public control plane only:

- tasks
- executions
- experts
- expert templates
- expert teams

Repository operations, daemon device enrollment, task claim/heartbeat, and
local runtime management are intentionally outside the generic CLI surface.

## TODO: Space-scoped device enrollment

The Web/Fleet/daemon enrollment flow must be changed as one coordinated
feature:

- Remove `workspace_id` from device bootstrap and enrollment requests and
  responses.
- Derive `octo_space_id` from the verified Human `LoopPrincipal`; never accept
  it as an untrusted request field.
- Bind enrollment, device identity, Device Credential, and Device
  `LoopPrincipal` to Space and device, not Workspace.
- Identify a daemon within a Space by `(octo_space_id, daemon_id)`.
- Remove `--workspace-id` from the Web-generated daemon installation command.
- Register machine-level runtimes with `workspace_id = NULL` and the Space from
  the Device Credential.
- Resolve and authorize Workspace only when accessing a Workspace-owned task,
  runtime profile, or other resource.
- Migrate the database, OpenAPI contract, Fleet policy, daemon enrollment, Web
  flow, and tests together. Do not remove `workspace_id` from only one layer.

This TODO does not add enrollment commands to `octo-cli`.

## Completed local checks

- `gofmt` verification
- `go vet ./...`
- `go test -race -count=1 ./...`
- local binary build
- embedded Loop schema discovery
- Loop command-tree discovery
- isolated dry-run routing for task, execution, expert, expert-template, and
  expert-team
- isolated task-create routing; required-body validation defect recorded in
  the test report

Local checks use a temporary `OCTO_CONFIG_DIR` and placeholder credentials.
They do not read or modify a developer's saved CLI profiles.

## Test environment setup

Use a dedicated test identity and test Space. Do not put credentials in this
document, shell history, command arguments, logs, or test fixtures.

```bash
export OCTO_CONFIG_DIR="$(mktemp -d)"
export OCTO_API_BASE_URL="<test-octo-api>"
export OCTO_BOT_TOKEN="<test-credential>"
```

When the credential already carries Space in its verified principal, do not
send `X-Space-Id`. Loop public operations declare
`x-octo-space-header: false`.

## Test matrix

### 1. Discovery and routing

- `octo-cli schema --list loop` returns all 61 public operations.
- `octo-cli loop --help` contains the five expected resource groups.
- Loop requests use `OCTO_API_BASE_URL/fleet/api/v1/*`.
- Non-Loop requests continue to use their module-qualified gateway paths.
- Authorization is sent as Bearer; tokens are masked in dry-run and verbose
  output.

### 2. Authentication and isolation

Run the read-only matrix with each supported test credential:

- Octo Session, if supported by the CLI execution environment
- `bf_`
- `uk_`
- Human or bot `octo_loop_` appropriate for the selected operation

Verify:

- valid identity succeeds;
- expired, revoked, or wrong-principal credentials fail deterministically;
- no Loop request depends on `X-Space-Id`;
- a principal cannot read a different Space;
- multiple saved profiles require explicit `--profile` or `--bot-id`.

### 3. Read-only smoke tests

- task list, get, search, grouped view, timeline, comments, executions, usage
- execution messages
- expert list/get, skills, environment, executions
- expert-template list/get
- expert-team list/get, members, member statuses

Check JSON envelope shape, pagination, empty lists, not-found behavior, and
permission errors.

### 4. Task lifecycle

In an isolated test Space:

1. Create a task.
2. Read and update it.
3. Add/list a comment and preview triggers.
4. Add/list/remove a label.
5. Set/list/delete metadata.
6. Subscribe/list subscribers/unsubscribe.
7. Add/remove a reaction.
8. Create or discover an execution, then list/cancel/rerun as allowed.
9. Delete the test task.

Also cover quick-create, parent/child task queries, attachments, pull requests,
and active executions when fixtures are available.

### 5. Expert lifecycle

1. Create an expert.
2. Read and update it.
3. Add/list/replace skills.
4. Read/update environment.
5. Archive and restore it.
6. Create an expert from a template.
7. List and cancel executions where permitted.

### 6. Expert-team lifecycle

1. Create and read an expert team.
2. Update it.
3. Add/list/update/remove a member.
4. Read member statuses.
5. Delete the team.

## Exit criteria

- All local checks remain green.
- All 61 embedded operations match Fleet `openapi/public-v1.yaml`.
- Read-only smoke tests pass for every supported identity.
- The three write lifecycles pass and clean up their fixtures.
- Cross-Space and wrong-principal tests are rejected.
- CLI output never exposes a complete credential.
- Existing non-Loop command tests remain green.

Until the authenticated environment matrix is complete, report the Loop
surface as “locally verified, environment integration pending”, not “fully
available”.
