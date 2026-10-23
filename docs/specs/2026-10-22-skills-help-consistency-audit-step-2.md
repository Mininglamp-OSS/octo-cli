# Octo CLI skills and help consistency audit — step-2

Date: 2026-10-22

Status: implemented and locally accepted on 2026-10-23. All 40 findings have a
selected handling approach and verification record below.

## Purpose and maintenance

This document records the additional CLI, skill, help and backend-contract
findings from the second audit pass, including the refreshed Drive source.
Each finding retains a stable ID, observed behavior, source/runtime evidence,
consumer impact and verification boundaries. The implementation register at the
end records the selected handling and verification under each stable ID. The
dated finding sections retain the audit-time observations and decision context.

The earlier audit and repair dispositions are maintained separately in
[step-1](2026-10-22-skills-help-consistency-audit-step-1.md). References to
CLI-001 through CLI-016, SKILL-001 through SKILL-022, LOOP-001 through LOOP-003,
VERIFY entries and DELTA entries refer to that record.

This implementation covers CLI request contracts, help and embedded skills.
Agent Mail is outside this pass.

## Supplemental source/backend audit — 2026-10-22

### Scope, provenance and interpretation

This pass compares the current CLI source, its embedded skills/references and
rendered help with local backend implementations. It includes the refreshed
Drive source. It does not repeat the earlier source repair acceptance, reopen
resolved findings without new evidence, or infer production deployment state.

| Repository or executable | Supplemental audit snapshot |
| --- | --- |
| CLI source base commit | `ed553a0` — `fix: align skills and help with request contracts (#208)`; executed worktree source is the authority for runtime observations |
| Installed CLI | `0.18.0`, commit `d564a63bbd8ec2dd4305a40a64fe0179ca9c667f`, build date `2026-10-13T08:55:20Z`; not replaced |
| octo-server | `25deab1`, local main checkout |
| octo-docs-backend | `ae11631`, local main checkout |
| octo-docs-html | `4bc19a2`, local main checkout |
| octo-marketplace | `b53ff70`, local main checkout |
| octo-fleet | `6785154`, local `fix/issue-board-pagination-main` checkout |
| octo-drive | `2db4df5`, refreshed local main checkout |

These are source snapshots, not assertions that the worktrees were clean or that
a remote branch/deployed environment still has the same contents. Companion
source references below use `repository:path:line` without machine-local paths.

The supplemental pass rendered all 442 source command/group help nodes and all
452 installed nodes without help failures. Source-tree introspection found
5,278 command/flag registrations, including inherited flags repeated per node;
every visible registered flag appeared in the corresponding help flag section.
This establishes presentation coverage, not backend completeness or execution
semantics. After excluding Mail, 84 flag descriptions across 39 commands were
empty; CLI-018 lists every affected command/flag pair.

The initial supplemental runtime probes covered 24 isolated scenarios. The
refreshed Drive pass additionally traversed 61 Drive help nodes, exercised eight
dry-run parameter cases, and ran two successful CLI multipart scenarios against
a local HTTP receiver. Invented credentials were used. No production business
write, real mailbox operation, or live object-store acceptance was performed.
Temporary receivers, binaries, configuration and source probes were removed.

A completeness follow-up compared the retained observations with this register
and exercised an additional isolated multipart retry scenario. DRIVE-007 records
that runtime finding; its selected handling appears in the implementation register.

New entries use these classifications:

- **Confirmed mismatch**: contradictory documented/executable/source contracts,
  or an observed failure.
- **Contract coverage gap**: an existing backend input or workflow is not
  described by the CLI schema/help/skill. This does not mean it must become a
  dedicated flag.
- **Capability documentation gap**: an existing valid workflow remains usable,
  but relevant discovery, pagination or prerequisites are absent.
- **Version delta**: installed/source differences, not automatically defects.

The findings below describe the audit-time state. Their decision boundaries are
retained as historical context; the implementation register at the end records
the current handling, affected surfaces and observed verification for all 40 IDs.

### Current skill coverage

| Skill | Supplemental finding IDs |
| --- | --- |
| octo-shared | SKILL-023 through SKILL-028; CLI-017 and CLI-018 affect cross-domain discovery |
| octo-messaging | MESSAGE-001 through MESSAGE-006 |
| octo-files | FILE-001 and FILE-002 |
| octo-drive | DRIVE-002 through DRIVE-007 |
| octo-docs and references | DOCS-001, DOCS-002 and SKILL-025 |
| octo-html | HTML-001 and HTML-002 |
| octo-marketplace and references | MARKET-001 through MARKET-007 |
| octo-loop | LOOP-004 through LOOP-010 |

The withheld matter/summary skill flags and normal-discovery behavior remain
consistent. Existing valid reader-grant examples remain covered by VERIFY-002;
an optional role not shown in one example is not reclassified as a broken call.

### CLI-017 — Operation descriptions are discarded before help and schema discovery

- **Classification / impact:** confirmed mismatch in information delivery;
  cross-domain contract explanations are inaccessible through the generated
  operation detail.
- **Observed contract:** many embedded operations put usage constraints,
  alternative body shapes, rollout requirements or recovery guidance in
  OpenAPI `description`.
- **Actual behavior:** `OperationInfo` has summary/risk but no operation-level
  description; `buildDetail` does not retain that field. `buildLongDesc` prints
  the summary, operationId, method/path, risk and selected generated hints,
  not the operation description. Schema inspection uses the same expanded
  detail, so it is not a complete substitute for the omitted operation prose.
- **Evidence:** CLI source and rendered help.
  `internal/registry/loader.go:223–230,543–552`;
  `cmd/service/service.go:235–262`; examples include `html.source`,
  `docs.ppt.export`, `plugin.import` and `drive.upload.parts_presign`.
- **Consumer effect:** a user can discover a flag but not the operation-level
  conditions governing it. Correcting only the spec's description does not
  make that guidance visible to help/schema consumers.
- **Decision boundary:** which operation information to expose remains
  unselected; this finding does not require dumping entire JSON Schemas into
  ordinary help.

### CLI-018 — Remaining non-Mail flags have empty help descriptions

- **Classification / impact:** capability documentation gap; discoverability
  and correct value selection.
- **Observed result:** source-tree introspection found the following 84 empty
  descriptions across 39 commands. Names/types remain registered. The targeted
  entries already corrected under CLI-012 retain their resolved disposition;
  this is the broader remaining inventory, not a reopening of those fixes.
- **Evidence:** source-built Cobra tree and full rendered-help traversal.
  Registration is implemented in `cmd/service/flags.go`; the corresponding
  domain definitions are under `internal/registry/specs/`.

| Command | Flags with empty descriptions |
| --- | --- |
| `bot register` | `--agent-platform`, `--agent-version`, `--plugin-version` |
| `bot space-members` | `--limit` |
| `event list` | `--limit` |
| `group create` | `--creator`, `--name`, `--space-id` |
| `group update` | `--name`, `--notice` |
| `loop attachment upload` | `--comment-id`, `--task-id` |
| `loop autopilot create` | `--description`, `--task-title-template` |
| `loop autopilot list` | `--status` |
| `loop autopilot trigger-config create` | `--cron-expression`, `--label`, `--timezone` |
| `loop autopilot trigger-config update` | `--cron-expression`, `--label`, `--timezone` |
| `loop autopilot update` | `--assignee-id`, `--description`, `--task-title-template`, `--title` |
| `loop expert create` | `--description` |
| `loop expert create-from-template` | `--runtime-id` |
| `loop expert update` | `--description`, `--name` |
| `loop expert-team create` | `--description` |
| `loop expert-team evaluate` | `--reason` |
| `loop expert-team update` | `--description`, `--leader-expert-id`, `--name` |
| `loop runtime activity list` | `--days`, `--tz` |
| `loop runtime list` | `--owner-id` |
| `loop runtime usage get` | `--days`, `--tz` |
| `loop runtime usage-by-expert get` | `--days`, `--tz` |
| `loop runtime usage-by-hour get` | `--days`, `--tz` |
| `loop task comment create` | `--attachment-ids`, `--parent-comment-id`, `--suppress-expert-ids` |
| `loop task comment preview` | `--attachment-ids`, `--parent-comment-id`, `--suppress-expert-ids` |
| `loop task create` | `--assignee-id`, `--description`, `--parent-task-id`, `--priority` |
| `loop task quick-create` | `--attachment-ids`, `--expert-id`, `--expert-team-id`, `--parent-task-id` |
| `loop task subscribe` | `--user-id` |
| `loop task unsubscribe` | `--user-id` |
| `loop task update` | `--assignee-id`, `--description`, `--parent-task-id`, `--priority`, `--title` |
| `marketplace mcp probe` | `--args`, `--command`, `--url` |
| `marketplace mcp-icon-upload create` | `--content-type` |
| `marketplace plugin delist` | `--reason` |
| `marketplace plugin import` | `--category-id`, `--changelog`, `--description`, `--icon`, `--tags` |
| `marketplace plugin review-request create` | `--changelog` |
| `message edit` | `--message-seq` |
| `message read-receipt` | `--channel-type` |
| `message sync` | `--end-message-seq`, `--limit`, `--pull-mode`, `--start-message-seq` |
| `thread create` | `--source-message-id` |
| `thread list` | `--page-index`, `--page-size` |

- **Boundary:** an empty description alone does not prove an unconsumed flag or
  wrong backend default. For example, omitted `event list` limit is not sent;
  the backend applies 20 and caps it at 100. Do not mistake the flag's zero
  storage value for a demonstrated backend-default mismatch.

### SKILL-023 — Shared service-selector guidance contradicts real special handling

- **Classification / impact:** confirmed mismatch; callers can omit handling
  needed for the Loop Public API.
- **Observation:** `skills/octo-shared/SKILL.md:98` says API `--service` is
  "for documentation only". Unified gateway routing is correct, but that
  conclusion about the flag is not.
- **Actual behavior / evidence:** `internal/client/client.go:891–895` sets
  `Accept: application/vnd.octo+json` for service `loop`;
  `internal/client/client.go:1088–1090` selects Public API error parsing.
  A real source CLI call to a local receiver with `--service loop` included
  that Accept header; the same passthrough without it did not.
- **Affected surfaces:** shared skill, generic API help and client semantics.
  CLI-007 already corrected the command help; this is the remaining shared
  skill statement, not a request to change the retained public flag.

### SKILL-024 — Shared identity guidance omits Drive routing and overstates one local-gate exception

- **Classification / impact:** confirmed documentation mismatch; wrong
  credential selection and incorrect expectations about local refusal.
- **Observation:** `skills/octo-shared/SKILL.md:62–70` describes App Bot search
  as the one local token-kind exception and recommends bot tokens for domains
  other than search. Drive accepts user API keys and routes them as real people;
  it also rejects unsupported credential kinds locally.
- **Evidence:** `internal/registry/specs/drive.json` service-level
  `x-octo-allowed-token-kinds` and `x-octo-mount-by-token-kind`;
  `cmd/service/identity.go:35–55`; `skills/octo-drive/SKILL.md:32–40`.
  The multipart CLI receiver observed `/v1/user/drive/*` for a user key.
- **Boundary:** this does not authorize using every credential on every domain.
  Per-domain backend authorization and daemon task-mode isolation remain
  separate contracts.

### SKILL-025 — Shared dry-run guidance does not describe operation-specific exceptions

- **Classification / impact:** confirmed documentation mismatch; incorrect
  assumptions about whether an invocation is accepted or reads remote state.
- **Observation:** `skills/octo-shared/SKILL.md:145–151,215–221` describes
  universal flags and request-only dry runs without the local mutation and
  portable-board exceptions.
- **Runtime evidence:** `auth update --dry-run` exits 2 before mutation.
  `auth login/logout` and `skills --install` also explicitly refuse dry-run in
  their command contracts. These are the intended source repairs in CLI-002,
  not failures to restore mutation during dry-run.
- **Portable export evidence:** `docs scene export --image-format excalidraw
  -o board.excalidraw --dry-run` issued one live scene GET to the local receiver
  and created no output file. `cmd/docs_excalidraw_export.go:64–99` reads the
  scene before the dry-run return; its command help states the exception.
  `skills/octo-docs/board.md`, "Portable .excalidraw export", does not.
- **Boundary:** flag inheritance, supported operation semantics, remote reads
  and local/remote writes are different facts. No behavior change is selected.

### SKILL-026 — Shared output wording treats all successful stdout as one JSON object

- **Classification / impact:** confirmed overstatement; automation can choose
  an incompatible parser.
- **Observation:** `skills/octo-shared/SKILL.md:102` says every successful
  invocation prints one JSON object, while the same document exposes
  table/CSV/NDJSON and jq projections at lines 149–150.
- **Actual contract:** the standard success envelope is the default JSON
  surface; the selected renderer/projection can change stdout shape. Help and
  shell completion also render text.
- **Evidence boundary:** document/source contradiction; no new formatter bug
  was demonstrated. This is independent of the already-fixed output preflight
  findings.

### SKILL-027 — Shared skill discovery omits active domain skills

- **Classification / impact:** capability documentation gap; agents loading
  the shared entry point do not discover all relevant domain guides.
- **Observation:** `skills/octo-shared/SKILL.md:247–256` lists messaging, files,
  docs, HTML and withheld domains, but not Drive, Marketplace or Loop.
- **Evidence:** actual `skills` discovery includes those active guides and
  their files exist under `skills/`. The current follow-up excludes Mail.
- **Boundary:** the guides are loadable; this is an index gap, not a broken
  embedding or permission issue.

### SKILL-028 — Shared setup guidance omits existing profile maintenance and file-token input

- **Classification / impact:** capability documentation gap; an operator
  following the shared guide does not discover supported noninteractive
  credential input or profile endpoint maintenance.
- **Observation:** the authentication examples describe a hidden prompt and
  `--with-token`, and the gateway section describes an environment override.
  They do not describe `auth login --token-file`, the profile-specific
  `auth login --api-base-url`, or the existing `auth update --api-base-url`
  operation.
- **Evidence:** `skills/octo-shared/SKILL.md:23–98`;
  `cmd/auth.go:53–61,112,124–128,202–205`. Token-file and stdin-token input are
  mutually exclusive. Updating non-secret profile settings is distinct from
  re-entering or rewriting the credential.
- **Related discovery:** the shared discovery section does not show
  `skills [name]`, `skills --install` or `auth logout`; these operations already
  exist, and local mutations have their own dry-run restrictions.
- **Boundary:** the shown prompt/stdin examples remain valid. This does not
  authorize reading stored credentials, changing an endpoint, overwriting
  exported skills or logging out a profile during ordinary agent work.

### MESSAGE-001 — CLI validation prevents the backend Registry card-edit mode

- **Classification / impact:** confirmed request-contract mismatch; a
  backend-supported edit mode cannot pass the generated CLI gate.
- **Backend contract:** `octo-server:modules/bot_api/send.go:874–939` accepts
  `template_ref`, `state`, `data`, `card_seq` and `transient`. Registry intent
  requires its fields and is mutually exclusive with `content_edit` by field
  presence. Adding an empty or nonempty `content_edit` is not a workaround.
- **CLI contract:** `internal/registry/specs/message.json:55–73` always requires
  `content_edit` and does not describe the alternative Registry request.
- **Runtime evidence:** a synthetic Registry-intent `message edit --data ...
  --dry-run` exited 2 with
  `request body is missing required field(s): content_edit`, before HTTP.
  The identifiers/template reference were invented; this proves the local
  unconditional gate, not acceptance by a deployed template registry.
- **Affected surfaces:** message operation schema/validation, edit help and
  messaging skill. `skills/octo-messaging/SKILL.md:41–45` describes raw editing.

### MESSAGE-002 — Messaging guidance does not describe current Type-17 card contracts

- **Classification / impact:** capability documentation gap; agents cannot
  discover the existing card message/edit prerequisites from the messaging
  guide alone.
- **Observation:** `skills/octo-messaging/SKILL.md:65–89` lists chat payload
  types through 16 but does not describe InteractiveCard type 17, raw versus
  Registry intent, or their OBO restriction.
- **Backend evidence:** `octo-server:modules/bot_api/send.go:98–158` handles
  Type-17 intent, deployment/policy gates and the raw/Registry distinction.
  `octo-server:modules/bot_api/bot_api.go:453–454` registers revision clearing
  and card-profile discovery.
- **Boundary:** the CLI can carry complex send payloads through `--data`.
  Missing prose does not mean ordinary card sends are locally blocked;
  MESSAGE-001 identifies the separate demonstrated edit blocker. Exposing
  additional backend endpoints is not selected by this finding.

### MESSAGE-003 — Event long-poll input is missing from the CLI schema and skill

- **Classification / impact:** contract coverage gap; current event guidance
  cannot discover the backend's optional hold.
- **Backend evidence:** `octo-server:modules/bot_api/events.go:19–35,67–85`
  accepts `wait` in seconds, preserves immediate polling when absent/zero,
  clamps the hold server-side, and waits only when no visible backlog exists.
- **CLI/skill observation:** `internal/registry/specs/event.json:24–27` contains
  only `event_id` and `limit`; `skills/octo-messaging/SKILL.md:182–212` describes
  periodic short polling.
- **Runtime evidence:** `event list --wait 20 --dry-run` exits 2 as an unknown
  flag; `event list --data '{"wait":20}' --dry-run` exits 0 and includes the
  field in the POST body.
- **Boundary:** this capability is already expressible through JSON. Any future
  long-poll guidance must account for the caller's request timeout, rather than
  claiming that adding a flag alone establishes reliable long polling.

### MESSAGE-004 — App Bot group-permission summaries contradict the documented markdown exception

- **Classification / impact:** confirmed documentation inconsistency;
  capability summaries can cause an otherwise-supported operation to be skipped.
- **Observation:** the messaging capability table calls App Bot group access
  read-only; the same skill lists `group md-update` among commands for both bot
  kinds and describes its separate `bot_admin` gate.
  The shared capability table also says App Bot group writes are unavailable.
- **Evidence:** `skills/octo-messaging/SKILL.md:15–20,142–163`;
  `skills/octo-shared/SKILL.md:64–68`; current `group md-update` help.
- **Boundary:** this entry records contradictory guidance. It does not prove
  that a particular App Bot is a group administrator or authorize widening any
  backend permission.

### MESSAGE-005 — History and thread-pagination guidance omits existing controls

- **Classification / impact:** capability documentation gap; history windows
  and collections beyond the first page are difficult to retrieve correctly.
- **Observed omissions:** message sync's `--start-message-seq`,
  `--end-message-seq` and `--pull-mode`; search's manual `--cursor` and
  `--page-size` and per-family `--channel-type` controls; thread creation's
  `--source-message-id`; thread list's
  `--page-index` and `--page-size`.
- **Evidence:** generated help and `internal/registry/specs/message.json`,
  `internal/registry/specs/thread.json`; compare
  `skills/octo-messaging/SKILL.md:47–48,138–140,169–177`.
- **Impact boundary:** current minimal examples remain valid. Thread list
  has page-number inputs, not `--page-all`; it must not inherit cursor-loop
  assumptions merely because the skill labels it paginated.

### MESSAGE-006 — Group mutation guidance omits notice input and operation-specific prerequisites

- **Classification / impact:** capability documentation gap; group
  administration is described less precisely than the registered commands.
- **Observed omissions:** `group create` exposes optional `--space-id`;
  `group update` exposes `--notice` as well as `--name`. The skill's update
  example changes only the name through JSON.
- **Prerequisites:** help labels `group update`, `group member-remove` and
  `thread md-update` with `bot_admin` requirements. Their examples do not
  explain those operation-specific prerequisites. These are separate from
  MESSAGE-004's contradictory App Bot capability summary.
- **Evidence:** `skills/octo-messaging/SKILL.md:142–177`;
  `internal/registry/specs/group.json:167–224` and the group/thread operation
  summaries in their embedded specs; source-built command help.
- **Boundary:** the current name-update body and member examples remain valid
  under their required permissions. This records source/help prerequisites,
  not a new live permission test or authorization expansion.

### FILE-001 — File-upload metadata still advertises a fixed 100 MB ceiling

- **Classification / impact:** confirmed documentation/source mismatch;
  callers cannot infer the effective upload policy from that constant.
- **Observation:** `internal/registry/specs/file.json:16` says `Max 100MB`.
  `octo-server:modules/bot_api/file.go:70–85,265–287` instead reads the current
  `file.MaxUploadSize()` policy and applies extension policy to multipart and
  presigned uploads.
- **Consumer effect:** assuming a fixed ceiling or treating direct upload as
  a policy bypass can cause avoidable server rejection. The file skill's
  direct-storage workflow does not remove backend size/type enforcement.
- **Evidence boundary:** backend source; no production policy value was read
  and no oversized or prohibited file was uploaded.

### FILE-002 — File and bot housekeeping guidance omits existing optional query controls

- **Classification / impact:** capability documentation gap, not invalid
  examples.
- **Observed omissions:** `file download --filename`;
  `bot space-members --limit` and `--space-id`.
  The `bot register` agent/plugin metadata also has promoted flags, although
  its documented JSON body already carries those same fields correctly.
- **Evidence:** actual command help, `internal/registry/specs/file.json`,
  `internal/registry/specs/bot.json`, and
  `skills/octo-files/SKILL.md:20–24,65–71`.
- **Boundary:** the metadata JSON example does not need rewriting merely
  because equivalent promoted flags exist. Query-scope and result-count
  discovery are the substantive omissions.

### DOCS-001 — Document-list filters supported by the bot-mounted backend are absent from CLI metadata

- **Classification / impact:** contract coverage gap; users cannot express
  supported collection filters with the generated list flags.
- **Backend evidence:** `octo-docs-backend:src/api/routes/docs.ts:429–470`
  accepts `owner=me`, filename substring `q` and repeated `type` values.
  `octo-docs-backend:src/api/app.ts:355–362` mounts that shared router for bots.
- **CLI observation:** `docs list` exposes folder, page, pageSize and sort
  inputs; the three filters are absent from its embedded schema/help and
  `skills/octo-docs/SKILL.md:92–93`.
- **Runtime evidence:** `docs list --q audit --dry-run` exits 2 as an unknown
  flag. `docs list` has no JSON request body through which to supply a query.
- **Permission boundary:** the adjacent backend `discoverable` behavior is
  explicitly human-only (`req.botToken === undefined`). It is not part of the
  bot-filter omission and is not authorized for exposure by this inventory.

### DOCS-002 — Common comment guidance does not expose single-comment and reply-page reads

- **Classification / impact:** capability documentation gap; a generic
  document/sheet/board workflow lacks a direct path to known comment anchors
  and subsequent reply pages.
- **Observation:** `skills/octo-docs/common.md:25–51` covers list/add/edit/delete
  but not `docs comments get` or `docs comments replies`.
  The PPT reference already describes these commands, so they are not missing
  from the entire embedded skill package.
- **Evidence:** generated command help and `internal/registry/specs/docs.json`
  operations `docs.comments.get` and `docs.comments.replies`.
  Replies expose a cursor plus `--page-all`/`--page-limit`.
- **Boundary:** no conclusion is drawn here about every deployed list
  response's embedded-reply truncation. Existing anchor mutation examples are
  not invalidated.

### HTML-001 — HTML comment help advertises reader permission while the backend requires commenter permission

- **Classification / impact:** confirmed permission-contract mismatch;
  callers can mistake a hidden authorization refusal for missing content.
- **Observation:** `internal/registry/specs/html.json:906–909` says comment
  creation needs reader capability/share code. Rendered
  `html comment add --help` repeats the reader claim.
- **Backend evidence:** `octo-docs-html:internal/transport/httpx/handlers_comments.go:242–245`
  calls `requireDocCommentSlug`; `internal/transport/httpx/capability.go:350–352`
  requires `CapComment`. Read-only share-code readers do not obtain comment
  permission and receive the hidden 404 on this write.
- **Related skill inconsistency:** `skills/octo-html/SKILL.md:67–69` summarizes
  all writes as author/write and all reads as reader. Permission tiers vary by
  operation; neither the reader-only help claim nor the universal write
  summary accurately describes comment creation.
- **Boundary:** source comparison, not a live authorization test. The finding
  does not authorize relaxing backend permissions.

### HTML-002 — HTML comment version and reply inputs are absent from the add schema

- **Classification / impact:** contract coverage gap; comment placement and
  thread behavior are not discoverable through the declared add body.
- **Backend evidence:** `octo-docs-html:internal/transport/httpx/handlers_comments.go:228–267`
  accepts `version` and `parent_id`. Its `mutationVersion.UnmarshalJSON`
  accepts the supported numeric/string selectors, including `"latest"`.
- **CLI observation:** `internal/registry/specs/html.json:915–933` declares
  slug/text/anchor only. `skills/octo-html/SKILL.md:263–265` does not describe
  those add inputs.
- **Runtime evidence:** `html comment add --version latest ... --dry-run`
  exits 2; the same selector in `--data` exits 0 and is retained in the body.
- **Boundary:** this is not a missing transport capability. The separately
  exposed agent `html reply` has its own contract; its `--applied-in` version
  association is another optional skill-coverage detail, not a requirement
  to replace all comment replies with that endpoint.

### MARKET-001 — Marketplace install rejects backend-supported custom environment input locally

- **Classification / impact:** confirmed request-contract mismatch; the
  backend-supported installation environment cannot pass this generated leaf.
- **Backend evidence:** `octo-marketplace:internal/api/handler/plugin/install.go:18–22,61–66`
  accepts `custom_env` as a string map and forwards it into installation.
- **CLI observation:** `internal/registry/specs/marketplace.json:844–872`
  closes the body with `additionalProperties:false` and declares only
  `plugin_id`, `workspace_id` and `runtime_id`.
- **Runtime evidence:** supplying all three identifiers plus
  `"custom_env":{"EXAMPLE":"audit"}` through `--data --dry-run` exits 2:
  `request body contains unknown field(s): custom_env`.
- **Affected surfaces / boundary:** install schema/help, expert installation
  guidance and any secret-bearing input/output policy are relevant to a future
  decision. No real installation or environment injection was performed.

### MARKET-002 — Publication guidance assumes manual review despite the Space automatic-approval policy

- **Classification / impact:** confirmed workflow-contract mismatch;
  callers can report a pending state or expect an approval pause after content
  is already published.
- **Observation:** `skills/octo-marketplace/SKILL.md:58–61` and the skill
  publishing reference describe Space publication as pending until an
  owner/admin approves. The embedded `plugin.publish` description makes the
  same unconditional statement.
- **Backend evidence:** `octo-marketplace:internal/api/handler/plugin/review_policy.go:16–18`
  documents automatic approval enabled when there is no stored override;
  `internal/api/handler/plugin/listing.go:41–42` describes both publication
  outcomes. `internal/service/plugin/review.go:133–172` reads the Space policy
  and can approve a submission immediately.
- **Related discovery gap:** the backend registers review-policy GET/PATCH at
  `internal/api/handler/plugin/handler.go:108–109`; the CLI registry has no
  corresponding operation.
- **Boundary:** documenting conditional outcomes and adding policy management
  are different scope decisions. No policy was read or changed in a deployed
  Space, and no plugin was published.

### MARKET-003 — Skill update recipe publishes a fixed initial version after importing a different version

- **Classification / impact:** confirmed example inconsistency; an update
  workflow carries conflicting version intent.
- **Observation:** `skills/octo-marketplace/skills.md:97–104` has separate
  create/update import examples, with `<next-version>` for update. The shared
  subsequent publish step at lines 114–118 uses `1.0.0` and `Initial release`
  for both.
- **Evidence:** skill text and the publish flag's optional version contract.
- **Boundary:** the existing evidence proves inconsistent instructions; it
  does not establish the exact rejection/result of every deployed private or
  Space publication branch. Handling of version selection remains unselected.

### MARKET-004 — Expert and connector upgrade examples do not specify the review submission contract

- **Classification / impact:** capability documentation gap in a write
  workflow; readers can confuse an upsert document with a review request.
- **Observation:** `skills/octo-marketplace/expert.md`, "Create / update", and
  `skills/octo-marketplace/mcp.md`, "Create / update", direct listed upgrades to
  `plugin review-request create --data @...` but do not provide the minimal
  review envelope or explicitly name required `plugin_id` and `version`.
- **Evidence:** generated review-request help and
  `internal/registry/specs/marketplace.json`, `plugin.review_request.create`.
  The shared guide explains content alternatives and relations; the skill
  reference's separate upgrade JSON already includes identity and version.
- **Boundary:** an unspecified file body is not proof that a shown command is
  invalid. This finding concerns the missing body contract, including the
  distinction between manifest/package content and the target review envelope.

### MARKET-005 — Review and version-history guidance does not explain complete pagination

- **Classification / impact:** capability documentation gap; a one-page
  review queue or version history can be mistaken for the entire collection.
- **Observation:** the Marketplace guide explains pagination for `plugin list`
  but its review-request examples and version-history reference do not carry
  that guidance to `plugin review-request list` and `plugin version list`.
- **Evidence:** both generated commands expose `--page` and `--page-size`;
  their help describes defaults and bounds. Neither has `--page-all`.
  See `skills/octo-marketplace/SKILL.md:66–80,131–140` and
  `skills/octo-marketplace/skills.md`, "Release a new version".
- **Boundary:** fetching one page remains a valid operation. The problem is
  claiming or relying on completeness during review, history or reconciliation.

### MARKET-006 — Icon-upload guidance does not describe required presign inputs

- **Classification / impact:** capability documentation gap in the upload
  workflow.
- **Observation:** the MCP reference names `mcp-icon-upload create` and the
  skill import guide mentions an icon URL, without describing the required
  icon-presign inputs and the upload handoff.
- **Evidence:** `marketplace mcp-icon-upload create` and
  `marketplace skill-icon-upload create` require `file_name` and `file_size`,
  exposed as `--file-name` and `--file-size`; MCP icon creation also exposes
  optional content type. See `internal/registry/specs/marketplace.json` and
  `skills/octo-marketplace/mcp.md`, final icon-upload paragraph.
- **Boundary:** naming a helper command is not an invalid executable example.
  This does not select a new high-level upload composite or dependency.

### MARKET-007 — Skill import optional-field guidance does not mention description updates

- **Classification / impact:** low-priority capability documentation gap;
  callers may not discover that import can also supply a description.
- **Observation:** `skills/octo-marketplace/skills.md:107–111` enumerates
  optional category, tags, icon and changelog inputs, but does not mention
  `--description`.
- **Evidence:** `internal/registry/specs/marketplace.json:954–955` declares
  that body property and the generated import command exposes its promoted
  string flag.
- **Boundary:** the optional-field list is not stated to be exhaustive and
  the existing import recipes remain valid. This is a coverage candidate,
  not a broken example or a requirement to enumerate every equivalent flag.

### LOOP-004 — Task list and grouping omit backend-supported filters

- **Classification / impact:** contract coverage gap; common scoped task
  selection cannot be expressed through generated query flags.
- **Backend evidence:** `octo-fleet:internal/handler/issue_filters.go:11–89`
  handles status/priority unions, assignee types, assignee/creator identities
  and typed filters, labels, and unassigned inclusion.
  `internal/handler/issue.go:929–988` also handles sort/direction.
  `internal/publicapi/normalize.go:423–449` preserves/maps public query inputs.
- **CLI observation:** `internal/registry/specs/loop.json:94–108` declares
  task-list page/page-size only; task grouping also lacks these selection
  inputs. The Fleet Public OpenAPI only partially describes the current
  implementation, so copying it without handler comparison would remain
  incomplete.
- **Runtime evidence:** `loop task list --status in_review --dry-run` and the
  `--assignee-octo-uid` form exit 2 as unknown flags, with workspace provided.
- **Boundary:** GET query inputs are not body fields. A `--data` recipe is not
  an equivalent workaround for the generated task-list command. No live
  workspace result or permission was tested.

### LOOP-005 — Task write schemas omit supported identity, date and execution-control fields

- **Classification / impact:** contract coverage gap; schema/help consumers
  cannot discover supported writes or their side effects.
- **Backend evidence:** `octo-fleet:internal/handler/issue.go:2016–2037,2280–2300`
  includes `assignee_octo_uid`, dates and attachments, and update fields such
  as `suppress_run`, `handoff_note` and position.
  These public bodies pass through Fleet's boundary mapping.
- **CLI observation:** embedded `TaskCreate`/`TaskUpdate` at
  `internal/registry/specs/loop.json:6883–6960` describe a smaller set.
- **Runtime evidence:** create with `assignee_type:"member"`,
  `assignee_octo_uid` and `start_date` survived dry-run in `--data`.
  Update with `status`, `suppress_run:true` and `handoff_note` also survived;
  the standalone `--suppress-run` flag was rejected.
- **Boundary:** these bodies are already transportable. Identity field
  exclusivity, authorized Octo-project bindings and run-trigger semantics
  remain backend contracts; no identity alias or suppression policy is
  selected here. Legacy project fields intentionally removed under DELTA-005
  are not automatically candidates for restoration.

### LOOP-006 — The collaboration guide omits existing trigger inspection and execution/progress controls

- **Classification / impact:** capability documentation gap; users warned
  about mention-triggered execution are not directed to the existing tools
  relevant to that risk.
- **Observation:** `skills/octo-loop/SKILL.md:193–231` discusses cancellation
  and mention side effects but omits `task comment preview`,
  `--suppress-expert-ids` and comment attachment IDs.
- **Additional uncovered inputs/reads:** active-execution listing, execution-ID
  cancellation, expert execution listing/cancellation, task timeline,
  child-progress, pull-request and usage reads, and task-label associations.
  The skill describes progress/labels as part of its intended scope but gives
  little operation-specific guidance.
- **Creation inputs:** task create/update/quick-create also expose
  `--parent-task-id`, and quick-create accepts `--attachment-ids`. The skill's
  minimal quick-create example supplies the prompt and an assignee only;
  parent/attachment inputs are not explained. These already exist in the
  embedded schemas and differ from LOOP-005's backend-only write fields.
- **Evidence:** current generated Loop tree and `internal/registry/specs/loop.json`.
  Comment preview/create expose parent-comment, attachment and suppression
  fields; other commands have distinct argument shapes.
- **Boundary:** no claim is made that preview has no side effects merely from
  its name, or that a suppression field guarantees every kind of run is
  suppressed. Those semantics must be checked before selecting future guidance.
  Existing minimal list/cancel examples remain valid.

### LOOP-007 — Shell explanation confuses quoted file substitution with evaluation of shell source

- **Classification / impact:** confirmed explanation mismatch; operators may
  reason incorrectly about which text is transformed and why a file body is
  preferred.
- **Observation:** `skills/octo-loop/SKILL.md:131–134` says
  `--content "$(cat ...)"` rewrites backticks, dollar substitutions, variables,
  quotes and newlines from the file. Ordinary shells do not recursively
  evaluate the output of that quoted command substitution as shell source.
  Internal newlines are retained; trailing newlines are removed.
- **Related example:** the title recipe at lines 156–158 uses
  `jq -Rs . <<< 'Fix login redirect'`; Bash's here-string appends a newline,
  so the JSON title includes a trailing newline not visible in the example.
- **Evidence boundary:** shell-language behavior and exact skill text, not a
  demonstrated CLI string mutation or backend title-storage test.
- **Boundary:** using JSON files remains a valid workflow. Its precise wording
  and example construction are not selected by this inventory.

### LOOP-008 — Task priority help does not expose the backend vocabulary

- **Classification / impact:** contract coverage gap; the advertised string
  accepts values locally that the backend will reject.
- **Evidence:** `octo-fleet:internal/handler/issue.go:72–85` defines
  `urgent`, `high`, `medium`, `low`, `none` and validates task priorities.
  CLI `TaskCreate`/`TaskUpdate` declare priority as an unconstrained string
  with no explanatory description.
- **Runtime evidence:** a source task-create dry-run with an invented priority
  string exited 0 and included that value in the request body.
- **Boundary:** this does not establish a promise to perform every backend
  validation locally. Whether to document values, encode an enum, or preserve
  server-owned vocabulary is a future contract decision.

### LOOP-009 — Autopilot guidance does not describe required dispatch-mode selection

- **Classification / impact:** capability documentation gap in an automation
  workflow with materially different dispatch outcomes.
- **Observation:** `skills/octo-loop/SKILL.md:118–126` names the autopilot
  namespace and requires authorization for changes, but gives no account of
  the existing dispatch-mode input or associated trigger/delivery controls.
- **Evidence:** `internal/registry/specs/loop.json:7308–7355` defines
  `dispatch_mode` as required for autopilot creation and accepts
  `create_task` or `direct_execution`. The generated tree also exposes
  trigger configuration, delivery replay and signing-secret/token management.
- **Consumer effect:** task-backed dispatch and direct execution are distinct
  supported choices. A future automation recipe cannot assume that every
  dispatch creates a task or omit a required mode.
- **Boundary:** the skill currently directs readers to command help rather
  than showing an invalid autopilot-create call. This does not select a
  dispatch mode, approve secret rotation, or promise that all automation
  administration needs a full skill tutorial.

### LOOP-010 — Attachment content/download guidance does not explain the file-output path

- **Classification / impact:** capability documentation gap; the guide's
  default-JSON guidance is insufficient for retrieving actual attachment bytes.
- **Observation:** `skills/octo-loop/SKILL.md:69–70,118–120` discusses JSON
  results and lists attachment as a namespace to explore, but does not show how
  binary content is saved.
- **Evidence:** `attachment.content.get` and `attachment.download` declare
  binary response bodies in `internal/registry/specs/loop.json:3697–3807`;
  their generated commands expose `--output` / `-o`.
- **Output contract:** for a successful inline binary response, `--output`
  saves the bytes to the chosen file and JSON stdout reports metadata plus the
  output path. Without the flag, the client describes the response but saves
  no file. See `internal/client/client.go:1042–1069`.
- **Boundary:** this is an existing capability, not a request for a new
  transfer command or permission to download arbitrary task attachments.

### DRIVE-002 — Cross-space move/copy and conflict handling are missing from CLI schemas

- **Classification / impact:** contract coverage gap; existing backend
  operations are only partially discoverable.
- **Affected operations:** `drive file move`, `drive file copy`,
  `drive folder move`.
- **Backend evidence:** `octo-drive:internal/modules/file/api.go:42–56,125–154`
  and `internal/modules/folder/api.go:50–59,154–165` accept and consume
  `target_space_id` and `on_conflict`.
  Omitted target space retains same-space behavior; explicit `rename` confirms
  acceptance of a same-name suffix rather than silently assuming it.
- **CLI observation:** embedded schemas at
  `internal/registry/specs/drive.json:355–445` declare parent ID and, for copy,
  optional name only. Skill workflows show same-space parent changes.
- **Runtime evidence:** standalone `--target-space-id` exits 2. For all three
  commands, dry-run JSON containing parent ID, target space and
  `on_conflict:"rename"` exits 0 and preserves the fields.
- **Boundary:** this is not a transport blocker or permission grant. No real
  cross-space move/copy was performed, and conflict resolution must not be
  assumed approved merely because the backend supports it.

### DRIVE-003 — Drive error guidance omits multipart errors and the completing state

- **Classification / impact:** capability documentation gap; agents lack
  operation-specific recovery context for existing structured errors.
- **Evidence:** `octo-drive:internal/httpx/errors.go:24–37` maps
  `upload_mode_conflict`, `object_size_mismatch`,
  `upload_policy_unavailable` and `too_many_upload_sessions`.
  `internal/output/errors.go:178–184` already preserves/classifies them and
  provides hints; the Drive skill error table does not list these four cases.
- **State explanation:** the skill's `upload_session_not_active` row mentions
  completed, expired or aborted sessions but not the supported `completing`
  status.
- **Boundary:** error mapping exists; this is guidance for existing structured
  errors and states, not a request to add error codes or retry every failure.

### DRIVE-004 — Invite/revoke script examples do not consistently use the documented leading-dash-safe input

- **Classification / impact:** confirmed executable-example boundary issue;
  valid opaque IDs can be parsed as flags.
- **Observation:** `skills/octo-drive/SKILL.md:104–109,232–233` shows positional
  invite accept/revoke and share revoke while explaining that these base64url
  values may start with `-` and recommending named flags in scripts.
- **Runtime evidence:** `drive invite accept -Ab3Audit --dry-run` exits 2 with
  `INVALID_FLAG`; `drive invite accept --invite-token -Ab3Audit --dry-run`
  exits 0 and constructs the masked invite path.
- **Other affected examples:** invite revoke has `--invite-id`; share revoke
  has `--share-id`. The remaining space positional argument on invite revoke
  is distinct from the optional ID flag.
- **Boundary:** quoting the shell value does not stop Cobra option parsing.
  The positional form is valid for other IDs; no token format change is
  selected.

### DRIVE-005 — Drive member example uses Bash's operating-system UID variable

- **Classification / impact:** confirmed example ambiguity with a concrete
  wrong-target input risk.
- **Observation:** `skills/octo-drive/SKILL.md:111–114` says to add a known
  Octo identity but supplies `--uid "$UID"`.
- **Runtime/source evidence:** Bash defines read-only `UID` as the local OS
  user identifier; a harmless shell probe returned a numeric OS UID.
  `drive member add --uid` instead expects the target Octo user/bot identity.
- **Boundary:** the CLI flag is correctly typed as a string. No actual member
  was added and no claim is made about how a server resolves an accidental
  numeric identity.

### DRIVE-006 — Drive workflow guidance omits existing page and target controls

- **Classification / impact:** capability documentation gap; valid minimal
  examples can be mistaken for the complete available workflow.
- **Observed omissions:** document candidates have real `--page`/`--page-size`
  pagination; IM transfer has `--target-parent-id` and `--name-override`;
  high-level upload has `--name` and `--content-type`; copy has a name override;
  invites have an expiry input. Password and password-file inputs are mutually
  exclusive in command annotations, while the skill does not state the
  exclusivity explicitly.
- **Evidence:** source-built Drive help, `internal/registry/specs/drive.json`,
  handwritten upload/share commands and `skills/octo-drive/SKILL.md`.
- **Boundary:** none of these omissions proves the existing minimal example
  invalid. Candidates use one-based `--page`, whereas search uses zero-based
  `--page-index` and browse's page envelope does not narrow the full listing;
  those interfaces must not be conflated in future guidance.
- **Editorial observation:** the Drive skill labels section 3 "The five
  workflows", but it contains six workflow headings, including full-text
  search. This is a low-impact count inconsistency, not a command failure.

### DRIVE-007 — Multipart retries remain active under the advertised no-retry flag

- **Classification / impact:** confirmed help/behavior scope mismatch;
  an operator cannot infer all multipart retry behavior from the inherited
  `--no-retry` description.
- **Advertised contract:** `cmd/root.go:69` describes `--no-retry` as
  "disable retry on transient failures". The shared skill describes it as
  disabling the default retry-on-transient policy, without explaining the
  multipart scheduler/completion exception.
- **Actual behavior:** the generic client observes `Options.NoRetry` in
  `internal/client/client.go:911–914`, but
  `cmd/drive_upload_multipart.go:403–458,589–628` has separate part/completion
  retry loops that do not consult the global selection.
- **Additional runtime verification:** the real source CLI ran
  `drive upload file <isolated-local-file> --space-id audit-space --no-retry`
  against a local receiver. The receiver returned one transient 503 for the
  single part's PUT and one for completion, then succeeded on the next request.
  The observed counts were one prepare, one presign, two PUTs and two complete
  requests; the command exited 0. Only invented credentials and local file
  bytes were used.
- **Affected surfaces:** global flag help, shared/Drive skill guidance and
  the handwritten multipart retry policy.
- **Decision boundary:** whether the global flag should control those loops
  or its documented scope should distinguish them remains unselected.
  No retry implementation or mutation policy was changed by this audit.

### Supplemental installed/source version observations

These observations extend DELTA-001 through DELTA-007 and the dated
LOOP-001/VERIFY-001 dispositions. They are not additional backend defects:

- Installed task search rejects a query positional argument with
  `accepts 0 arg(s), received 1`. The source form accepts one quoted query and
  includes its encoded value in the GET query string.
- Installed API passthrough rejects `--workspace-id`; source dry-run includes
  `X-Workspace-ID`. Installed HTML list rejects explicit page/page-size;
  source dry-run includes canonical page/page_size.
- Source additionally exposes `task children-by-parent --parent-task-ids`,
  Drive prepare capability selection and the three multipart leaves. The
  expert-team evaluation command path and project-surface removals are
  already recorded in DELTA-004/DELTA-005.
- Source/installed skill-package comparison found content differences in the
  shared, messaging, files, Drive, Docs, HTML, Loop and Marketplace packages.
  Marketplace's main SKILL.md matched but its MCP reference differed; Docs
  differed in its main guide and board/common/doc/sheet references.
- Querying an unknown nested path with `--help` can render parent help and
  exit 0. The installed/source tree comparison therefore checked command
  discovery and rendered Usage paths rather than treating exit 0 alone as
  proof that a leaf exists.

The installed executable was not updated. A future distribution decision must
distinguish installed binary version, embedded skills and any previously
exported skill files; a source-only match is not evidence of installed parity.

### Refreshed Drive compatibility evidence

The current Drive backend registers the multipart leaves at
`octo-drive:internal/modules/blob/api.go:50–67` and wires the upload service
through `internal/router/router.go:369–377`. Its request/response fields match
the CLI's capability header, part-number/ETag bodies and server-owned plan.
`internal/modules/upload/service.go:237–245` converts configured part size to
bytes before responding. This source dependency is available for the current
audit snapshot.

Observed CLI protocol scenarios against an isolated local receiver:

| Scenario | Result | Verification boundary |
| --- | --- | --- |
| Bot multipart success, 26 parts | One prepare; presign batches 20 and 6; 26 PUTs; one complete; zero aborts; bytes matched input | Real CLI, local protocol fixture; no live DB/object store |
| User-key multipart success, two parts | User mount used for prepare/presign/complete; bytes matched input | Same isolation; no production identity verification |
| Presigned PUT credential boundary | No Octo Authorization or Space header on fixture PUTs | Local receiver observation |
| Four low-level upload dry-runs | Capability header, upload ID, part numbers and completion ETags retained | Request construction only |

Ordinary multipart request compatibility is therefore not itself an open
finding. This evidence does not imply deployment readiness, backend feature
enablement or real storage acceptance.


## Implementation and local acceptance — 2026-10-23

### Selected handling by finding

The register below completes this step's CLI scope. Permission and publication
prerequisites were aligned with the inspected service contracts; their live
authorization and deployment behavior is outside the local acceptance evidence.

| ID | Selected handling and affected surfaces | Observed verification |
| --- | --- | --- |
| CLI-017 | Retain operation `description` in registry operation details and render it alongside the summary in generated long help. | Before/after CLI and schema probes; operation-specific guidance is visible in actual help. |
| CLI-018 | Populate the 84 empty query, header and promoted-body flag descriptions across the affected embedded specs. | Traversed all 411 non-Mail help nodes; no empty flag descriptions remain. |
| SKILL-023 | Describe unified gateway routing and the real `api --service loop` Accept-header and Public API error-parser behavior in `octo-shared`. | Real CLI local receiver observed the Loop vendor Accept header only when selected; existing parser behavior remains covered by tests. |
| SKILL-024 | Document user-key Drive mounts, per-operation token gates and the App Bot group-markdown membership/`bot_admin` prerequisite. | Source-contract review; real CLI local receiver observed user-key Drive routing. Live group authorization was not exercised. |
| SKILL-025 | State dry-run boundaries for local mutations and portable `.excalidraw` export; supply a complete export command with a document ID. | Actual CLI refused local-mutation dry-runs; portable export dry-run performed the scene GET but created no output file or attachment download. |
| SKILL-026 | Describe JSON as the default envelope format and explain the stdout-shape effects of `--format` and `--jq`. | Guide review against output dispatch and existing output tests; embedded guide export matched source. |
| SKILL-027 | Align the shared domain index with current Drive, Loop and Marketplace command surfaces and withheld domains. | Actual command-tree traversal and embedded guide export review. |
| SKILL-028 | Document token-file login, profile-origin maintenance, credential selector exclusivity, skill installation and logout. | Six actual operator scenarios with an isolated config directory; origin update left encrypted credential bytes and salt unchanged; mutually exclusive input left state unchanged; logout removed the profile. |
| MESSAGE-001 | Use the existing body-variant contract for raw and Registry `message edit`: retain common fields, require each mode's fields and reject mixed-mode fields by presence, including empty or null values. | Failing-before/passing-after request tests; actual CLI exercised both valid modes and refused mixed, null and blank raw edits before HTTP. |
| MESSAGE-002 | Document Type-17 raw/Registry edit workflows, template policy gates and the absence of an OBO edit mode. | Source-contract review, both CLI request-construction paths and regression tests. Registry policy acceptance was not tested live. |
| MESSAGE-003 | Document event wait seconds, immediate behavior, visible-backlog behavior, the server's 30-second clamp and caller timeout margin. | Actual help and dry-run request construction with `wait`/`limit`; inspected service wait logic. No live long-poll timing claim. |
| MESSAGE-004 | State the administrator prerequisites for group operations and the App Bot group-markdown exception. | Source-contract and generated-help review. Live membership/administrator grants were not exercised. |
| MESSAGE-005 | Complete history/search/thread guidance, channel-type selection, source identifiers and operation-specific cursor/page-number workflows. | Command-tree/parameter review and embedded guide export; paging guidance matches the inspected operations. |
| MESSAGE-006 | Describe group create/update/member inputs, limits and administrator prerequisites. | Embedded spec, help and service-contract review. |
| FILE-001 | Replace fixed upload-policy assumptions with the backend-controlled size/extension policy and describe the supported upload paths. | Inspected service policy and transfer implementations; embedded guide review. No live upload-policy rollout claim. |
| FILE-002 | Describe the upload filename and bot space-member query controls. | Generated flag descriptions and command help verified in the complete help traversal. |
| DOCS-001 | Add owner, query and repeated type filters to `docs list`, including the supported type vocabulary. | Actual CLI dry-run preserved `owner=me`, `q` and separate repeated `type` query values. |
| DOCS-002 | Document comment get/replies operations and the pagination supported by reply reads; keep root-comment listing guidance operation-specific. | Operation/spec review and embedded guide export. |
| HTML-001 | State comment capability requirements and the reader-share-code boundary. | Source authorization contract and help/guide review. Live capability checks were not exercised. |
| HTML-002 | Allow string/integer comment versions, expose the parent-comment flag and describe reply `applied_in` as a separate version association. | Actual CLI preserved `latest`, `v3`, JSON integer `3`, `parent_id` and integer `applied_in` in the appropriate requests. |
| MARKET-001 | Accept optional string-map `custom_env`; collect secret values from the merged body through one shared collector used by generated commands and generic API diagnostics, including configured Marketplace mounts and equivalent unreserved literal-path escapes. Preserve outbound paths and opaque placeholder values. | Failing-before/passing-after map, mount-prefix and escaped-literal tests; actual CLI dry-run/verbose/error diagnostics hid synthetic values while the local receiver observed unchanged paths and wire bodies. |
| MARKET-002 | Document Space auto/manual approval and private owner-only installation prerequisites. | Source-contract and guide review. Live installation approvals were not exercised. |
| MARKET-003 | Use one consistent imported/published version through the publication workflow. | Version-field and workflow review against the inspected service contracts. No live publication performed. |
| MARKET-004 | Document review-request envelopes and required identifier/version/content relationships. | Request/response contract and embedded guide review. |
| MARKET-005 | Provide complete explicit paging workflows for relevant Marketplace lists. | Pagination contracts and guide examples reviewed against registered parameters. |
| MARKET-006 | Document icon-upload name/size requirements, returned method/headers and the distinct durable MCP versus Skill icon handoff fields. | Upload response/request contracts and guide review. No live icon storage acceptance. |
| MARKET-007 | Explain import description semantics at the operation and workflow surfaces. | Actual generated long help and embedded guide export review. |
| LOOP-004 | Expose the supported scalar CSV list/group filters, plural precedence, typed identities, labels, metadata, date/sort/page controls and operation-specific switches. | Actual CLI list/group dry-runs preserved filters; source-contract review confirmed scalar CSV rather than repeated-array encoding. |
| LOOP-005 | Complete task identity/date/attachment/parent fields and update suppression/handoff controls; support fractional number flags and explicit JSON null clearing. Numeric query and multipart scalar flags serialize in plain decimal notation. | Actual CLI create/update requests preserved attachments, `position: 4.5`, suppression controls and nullable fields; isolated number-flag regression test observed the fractional HTTP body value. Large/small signed decimal and exponent inputs failed before the text-serialization fix and passed after it; an isolated generated-command receiver smoke observed plain decimals in query and multipart fields with file bytes intact. |
| LOOP-006 | Document execution/cancel/progress, label, preview/suppression and quick-create workflows. | Actual CLI quick-create request construction plus command/parameter and source-contract review. |
| LOOP-007 | Correct shell substitution and newline examples so the shown commands express the intended values. | Shell/example review and embedded guide export. |
| LOOP-008 | Add the backend's five-value priority vocabulary and retain the seven-value task-status contract. | Failing-before/passing-after priority tests for flags and JSON bodies; independently backend-sourced enum contract checks cover the added request vocabularies. |
| LOOP-009 | Require and document autopilot `dispatch_mode`, both supported choices and sensitive controls. | Actual CLI dry-runs exercised both dispatch modes and retained the controls. |
| LOOP-010 | Document binary output and `--output` behavior separately from JSON metadata output. | Real CLI local receiver verified file bytes for binary output and metadata-only request description for dry-run. |
| DRIVE-002 | Add `target_space_id` and `on_conflict` to the three affected move/copy operations; leave conflict outcomes to the backend contract. | Actual CLI dry-runs preserved both fields for all three operations. |
| DRIVE-003 | Document the observed upload error codes and the `completing` state boundary without adding a recovery mechanism. | Error/state source-contract review and guide review; local retry/cleanup scenarios passed. No real storage-state recovery claim. |
| DRIVE-004 | Show named ID flags as the supported escape hatch for IDs beginning with `-`. | Actual CLI dry-run accepted a dash-leading ID through the named flag and retained it in the request. |
| DRIVE-005 | Use `OCTO_UID` in shell examples to avoid assigning the shell's readonly `UID`. | Shell/example review and embedded guide export. |
| DRIVE-006 | Add the six missing Drive workflows and relevant optional controls, including expiry/password exclusivity. | Registered command/parameter and source-contract review; exported embedded guide matched source. |
| DRIVE-007 | Apply `--no-retry` to custom multipart part PUTs, failure-triggered re-signing and completion retries; retain the initial attempt, initial expired-URL signing, default retry policy and cleanup behavior. State these boundaries in global help and the shared guide. | Failing-before/passing-after part/signature/completion tests; actual CLI local receiver observed one attempt with no-retry, the existing transient retry flow by default, cleanup and byte/header boundaries. Review follow-up: rendered `drive upload file --help` exposes the retry boundaries; six actual-binary receiver scenarios confirmed rejected signatures, transient failures, completion failures, default recovery and initial expired-URL signing with cleanup and storage-header isolation. |

### Acceptance evidence

All checks below were observed locally. No dependencies, release version, deployed
service or installed operator binary were changed by this implementation.

| Help metric, excluding Agent Mail | Audit baseline | Accepted implementation |
| --- | --- | --- |
| Reachable command/help nodes | 411 | 411 |
| Empty parameter descriptions | 84 | 0 |
| Commands with empty parameter descriptions | 39 | 0 |

Executed repository checks:

```text
go test -race -shuffle=on -count=1 ./...
go test -race -count=1 ./skills
go vet ./...
golangci-lint run
gofmt -l .
go build -o /tmp/octo-step2-after ./cmd/octo-cli
npm test --prefix npm
node --test scripts/release-workflows.test.mjs scripts/ppt-media-guidance.test.mjs
```

The Go suites passed; lint reported zero issues; formatting listed no files.
The npm suite passed all 9 tests and the Node script suites passed all 32 tests.
The local toolchain was Go 1.27.0, golangci-lint 2.13.2, Node 22.22.3 and npm
10.9.8; the repository's CI toolchain remains unchanged.

Actual binary acceptance additionally covered:

- **27 request/validation scenarios** spanning operation descriptions, Docs/HTML
  parameters, event controls, Loop filters and task bodies, both edit modes,
  Marketplace map secrecy, Drive move/copy and dash-leading IDs.
- **10 isolated protocol scenarios / 25 observed HTTP requests** covering Loop
  Accept negotiation, portable export dry-run reads, binary download, Marketplace
  diagnostic redaction with intact wire values, multipart no-retry/default retry,
  user-key mounts, upload byte integrity and storage credential-header isolation.
- **6 isolated operator scenarios** covering token-file login, origin update,
  config inspection, mutually exclusive inputs, logout and the resulting empty
  profile list. Synthetic credentials were used throughout.
- **16 exported non-Mail Markdown guides/references**, byte-identical to the
  embedded source, including all 13 changed guide files.

Regression assertions for multipart retry bounds, edit-body modes, secret maps,
effective Marketplace mounts, priority vocabularies and fractional number flags
were first observed failing for their predicted reason against an isolated
pre-fix snapshot. Temporary fixtures used local receivers and invented tokens.
The updated permanent tests assert request behavior, value preservation and
diagnostic privacy rather than guide wording or implementation structure.

### Generic API path recognition acceptance — 2026-10-23

Registry recognition compares literal path segments with RFC 3986 unreserved
escapes decoded on either side. It preserves literal case, encoded separators,
other reserved escapes and the original spelling of captured path parameters.
Request paths and bodies are sent unchanged.

The escaped-path privacy regressions failed before the fix by exposing synthetic
`custom_env` values. Actual binary acceptance then passed 20 isolated scenarios:
nine canonical/escaped/configured-prefix forms through both dry-run and verbose
error paths, plus two reserved-encoding boundaries. The local receiver observed
11 requests with unchanged paths and bodies; diagnostics exposed no secret values.

### Verification boundaries

Acceptance establishes local CLI construction, validation, diagnostic privacy,
help discovery, embedded documentation and the exercised protocol behavior.
It does not establish live tenant permissions, registry/template policy approval,
Marketplace publication/approval, production catalog state, object-store
acceptance or deployment readiness. Companion repositories were inspected
read-only. No production writes, backend implementation changes, release or
deployment were performed.
