# Octo CLI skills and help consistency audit

Date: 2026-10-22

Status: source repairs and positional task-search behavior verified. Dated
dispositions below record the current state.

## Purpose and maintenance

This document records the discrepancies found while checking embedded skills,
command/subcommand help, flag registration, request construction, and selected
backend contracts. Original observations and version scopes remain historical
evidence. Dated dispositions record the subsequently confirmed adjustments,
implementation references and verification; they do not claim a release or deployment.

Every finding has a stable ID. The initial findings were **Open** or
**Verification dependency**. The user authorized the directly repairable items
and separately confirmed local dry-run refusal, retained service handling,
timeout fallback documentation, synchronized Loop query contracts and explicit
HTML paging with unchanged defaults. Only demonstrated discrepancies were repaired.

When an issue is addressed, update its existing entry with the decision date,
selected behavior, affected repositories, implementation reference, verification
commands/results, and final state. Keep the original observation and version
scope as historical evidence. If investigation disproves a hypothesis, record
that result under the same ID. Add new IDs rather than renumbering existing ones.
Do not mark an issue resolved merely because a test passes or a spec was edited;
record the observable behavior that closes the discrepancy.

## Baselines and evidence boundaries

| Baseline | Recorded value |
| --- | --- |
| Installed release | `0.18.0` |
| Installed commit | `d564a63bbd8ec2dd4305a40a64fe0179ca9c667f` |
| Installed build date | `2026-10-13T08:55:20Z` |
| Source checkout | `3d39abd` — `chore(release): prepare cli v0.19.0 (#207)` |
| Installed command tree | 452 nodes including root and groups; 342 leaf nodes |
| Source command tree | 442 nodes including root and groups; 335 leaf nodes |
| Enabled embedded skills | 9 skills, 17 Markdown documents including references |
| Withheld skills | `octo-matter`, `octo-summary` |

Evidence labels used below:

- **Runtime**: observed by executing the installed binary, a temporary build of
  the source checkout, or a local HTTP receiver. The entry names the baseline.
- **CLI source**: directly supported by source/spec/skill text. This alone does
  not prove the installed binary has the same implementation.
- **Backend source**: cross-checked against local companion-service code. Local
  source is not evidence that a particular deployment runs that revision.
- **Verification dependency**: a documented claim or capability that could not
  be completed with the available backend source, credentials, or deployment.

The initial audit made no production business requests. Request-construction probes used
invented credentials, isolated credential directories, and a loopback API base
URL. HTTP ordering/header probes used a local receiver. Fake profiles, temporary
binaries, and exported skill files were removed after that verification. The
initial audit changed no application implementation; the later source repair
pass is recorded separately below.

Original observation locations refer to the source baseline above; dated
implementation references point to the repaired worktrees. Paths without a
repository prefix are relative to this repository. Companion-service references
use repository names rather than machine-specific checkout paths. Line numbers
are navigation aids; symbols and issue IDs remain authoritative after edits.

### Recorded coverage

| Check | Result | What it establishes |
| --- | --- | --- |
| Help traversal | 452 installed nodes, no help failures | Reachable command/group help renders |
| Advertised long flags | 4,942 non-help flag probes accepted after using the correct sample types | Advertised flag names parse; not that execution consumes them |
| Advertised shorthands | 462 probes accepted | Advertised short names parse |
| Unknown flags | Rejected at all 452 nodes | Unknown names are not silently accepted |
| Enabled schema discovery | 318 operations | Enabled registry introspection coverage |
| Help-to-operation mapping | 315 operations | Three handwritten share leaves are not identified by the same operationId help marker |
| Expected spec flags | 317 parameter flags and 354 promotable body flags; none missing | The CLI registers what its own loaded schemas declare |
| Generated request construction | 297 successful offline dry runs | These fixtures reached request construction without a business request |
| Mail request construction | 18 operations stopped at local authorization prerequisite | No Mail end-to-end claim; see VERIFY-005 |
| Existing skill/flag tests | Passed | Existence/collision coverage, subject to the limits below |

The existing check was run with:

```sh
go test ./cmd ./cmd/service ./skills -count=1 -v \
  -run 'TestSkills_|TestFlags_|TestRoot_Reserved|TestOctoLoopSkillEmbedded'
```

`TestSkills_EveryDocumentedFlagExists` reported 489 flag uses across 426
resolvable invocations in 19 Markdown documents, with 37 unresolved lines. Its
scope explicitly excludes argument arity, body validity, value semantics, and
shell pipeline correctness. It also includes withheld skill documents, which
explains the difference from the 17 enabled documents. Passing this test does not
close the issues below or establish full backend compatibility.

Dry-run fixture failures caused by an incomplete generated sample were corrected
before the final 297/18 coverage totals. They are not product findings.

## Source repair acceptance — 2026-10-22

The source pass and its review follow-up resolve CLI-001 through CLI-016,
SKILL-001 through SKILL-022, LOOP-001 through LOOP-003, and the subsequently
confirmed VERIFY-001 discrepancy.
VERIFY-002 through VERIFY-006 have individual non-defect or scope dispositions.
DELTA entries remain version history, not additional repair requests.

Implementation references describe this source change set. This repository contains
the CLI/skill/help changes; `octo-fleet:openapi/public-v1.yaml` contains the two
matching query declarations. The supplied Docs, HTML and Marketplace backend
repositories were read as authorities and were not modified. The installed
0.18.0 binary was not replaced by this source repair pass.

This records the earlier source pass only; it does not verify the later CLI-only
positional query contract documented under LOOP-001.

**Automated verification:** `go test ./... -count=1`, `go vet ./...`, and
`go build -o <temporary-binary> ./cmd/octo-cli` passed. Behavioral regressions
were observed failing before their corresponding fixes, including the follow-up
jq-compilation and completion cases. Wording-only tests were removed rather than
updated to pin new prose. Pure request forwarding was verified with runtime
receivers, not retained as permanent forwarding-only tests.

**Actual executable smoke:** isolated configuration, invented tokens and local
HTTP receivers established the following:

- Invalid format, malformed jq and an undefined jq function exit 2 with
  `config / CLI_ERROR` and zero business requests. Valid compiled jq returns
  the expected response value. Bash/zsh completion still generates syntactically
  valid shell text with unrelated invalid output controls.
- Skill installation dry-run creates no target. Auth login/update/logout dry-runs
  refuse before mutation; stored profile files remain byte-identical. Invalid
  output options also cannot remove a profile.
- Board comments send the supplied `Idempotency-Key`; dry-run describes the same
  header without another request. Document share permission refusal makes no
  share POST; plain document links and blob permissions still work.
- Loop search can select a query containing reserved URL characters. Batch
  children can select two parent UUIDs; omitted parents retain the empty result.
  Fleet YAML parses, and both parameter definitions match the embedded CLI spec.
- HTML list with no query returns the first 20 of a 21-document fixture.
  Explicit page 2 returns the final document and preserves `_pagination`.
- The embedded group recipe fetches both `group_no` values separately; the owner
  UID recipe decodes a raw UID; the attachment recipe uploads multipart data and
  produces a valid type-8 message dry-run; the event recipe polls cursors
  `0`, then `101`, and retains final cursor `202`. JSON captures also work with
  an ambient CSV output default.
- All 442 source command/group help nodes render. Changed metavar, requiredness,
  cursor/enabled descriptions, schema usage, scene defaults and service/timeout
  guidance were checked on the rendered surface. Nine enabled skills and all
  17 embedded documents are readable; an isolated installation writes 17 files.
- An invalid timeout still warns and sends the request using the 30-second
  default, as confirmed by the user. Generic `api --service loop` sends
  `Accept: application/vnd.octo+json`.

These checks prove the local source contracts and exercised paths. They do not
establish deployment revisions, production permissions, storage exports, actual
message delivery, Mail execution or backend idempotent-deduplication guarantees.

## Skill coverage map

| Skill | Findings or verification dependencies |
| --- | --- |
| `octo-shared` | SKILL-001 through SKILL-005; DELTA-007 |
| `octo-files` | SKILL-006 through SKILL-009 |
| `octo-drive` | SKILL-010 through SKILL-012; CLI-005, CLI-013; DELTA-002 and DELTA-003 |
| `octo-messaging` | SKILL-013 and SKILL-014 |
| `octo-docs` and references | SKILL-015 through SKILL-022; CLI-003, CLI-008 through CLI-012 |
| `octo-html` | VERIFY-001 and VERIFY-002; CLI-004 |
| `octo-loop` | LOOP-001 through LOOP-003; CLI-004; DELTA-004 through DELTA-007 |
| `octo-marketplace` and references | VERIFY-003 and VERIFY-004; no demonstrated CLI example/body mismatch in this audit |
| `octo-mail` | CLI-004 and VERIFY-005; no demonstrated skill example/body mismatch in this audit |

The withheld `octo-matter` and `octo-summary` specs and skill frontmatter agree
on their disabled state. They are absent from normal command/skill discovery.
References to withheld commands in an enabled skill remain separately actionable
under SKILL-001.

## CLI execution and help findings

### CLI-001 — Output option errors are detected after a write request is sent

- **Disposition (2026-10-22): Resolved in source.** Root preflight checks the
  effective format and parses/compiles jq before execution. Factory caches the
  compiled program by expression. See `cmd/root.go`,
  `internal/cmdutil/factory.go`, `internal/output/jq.go` and
  `cmd/output_preflight_test.go`; zero-request and profile-preservation smoke passed.
- **Version/evidence:** Runtime on both 0.18.0 and source baseline.
- **Observation:** `--format bogus` and the malformed jq expression `.[` parse
  as flags. Their validity is checked only when formatting a success result.
  For each option, a local receiver observed one POST before the CLI exited 2.
- **Recorded errors:** `unknown format "bogus" (expected json|table|csv|ndjson)`;
  `parse jq expression: unexpected EOF`. Both were `config / CLI_ERROR`.
- **Sources:** `internal/cmdutil/factory.go`, `EmitSuccess`, especially the calls
  to `output.ApplyJQ` and `output.Format` around lines 618–631;
  `internal/output/jq.go:19`; `internal/output/format.go:26–37`.
- **Reproduction shape:** a valid `message send --data ...` against a local HTTP
  receiver, with either invalid output option and without `--dry-run`.
- **Impact:** a nonzero exit does not mean the write was prevented. An agent
  treating this as a failed write can submit the operation again.
- **Decision boundary:** validation timing and error classification require a
  recorded resolution; this entry does not select a new output/retry contract.

### CLI-002 — Global dry-run is accepted by local mutation commands that still write

- **Disposition (2026-10-22): Resolved in source.** The user selected refusal
  for local mutations. `cmd/auth.go` and `cmd/skills.go` reject dry-run before
  token reads or writes; their help explains this. Regression and actual
  isolated-profile/file checks passed; discovery/read-only commands remain available.
- **Version/evidence:** Runtime on both baselines for skill installation and
  profile logout; CLI source for the related login/update paths.
- **Observation:** `skills --install <isolated-directory> --dry-run` exited 0 and
  wrote 17 Markdown files. After creating an invented profile in an isolated
  `OCTO_CONFIG_DIR`, `auth logout --profile audit-profile --dry-run` exited 0 and
  changed the profile count from 1 to 0.
- **Sources:** `cmd/root.go:47–56` registers the inherited flag;
  `cmd/skills.go`, `runSkillsInstall`; `cmd/auth.go`, `newAuthLoginCmd`,
  `newAuthUpdateCmd`, and `newAuthLogoutCmd`, which do not branch on `DryRun`.
- **Impact:** callers cannot infer a no-local-mutation guarantee from the flag
  being visible and accepted. Logout can remove a usable credential profile.
- **Verification limit:** login/update dry-run mutations were identified from
  source, not separately exercised as dry-run scenarios in this audit.
- **Decision boundary:** the eventual behavior for local-only mutations is not
  selected here; documentation, refusal, and simulation are different contracts.

### CLI-003 — Board comment branch discards an accepted idempotency key

- **Disposition (2026-10-22): Resolved in source.**
  `cmd/docs_board_comment.go` forwards valid keys in real and dry-run requests.
  It reuses `service.ValidateIdempotencyKey` with generated operations and
  rejects explicitly empty or whitespace-only keys before scene reads or writes.
  Point/element and real/dry-run regressions passed with zero requests for blank
  keys; valid and omitted keys remained usable in actual local CLI smoke.
  Backend deduplication semantics are unchanged and were not asserted.
- **Version/evidence:** Runtime on both baselines, including a local real HTTP
  request rather than only dry-run output.
- **Observation:** `docs comments add audit-doc --body audit --point 1,2
  --idempotency-key audit-key --dry-run` reports `headers: {}`. Without dry-run,
  the local receiver observed POST `/v1/bot/docs/audit-doc/comments` with no
  `Idempotency-Key` header.
- **Sources:** `cmd/docs_board_comment.go:45–58` selects the handwritten branch;
  `runDocsBoardCommentAdd` constructs the dry-run and real requests at lines
  100–111 without forwarding the header. The same constructor handles element
  anchors, although only the point branch was executed in the HTTP probe.
- **Impact:** a flag that parses and remains visible in help does not reach the
  request. Callers cannot rely on it being conveyed for uncertain retries.
- **Boundary:** this proves dropped header forwarding, not that the deployed
  backend guarantees deduplication for every board-comment operation. PPT-specific
  required-key wording must not be generalized to all document types.

### CLI-004 — Requiredness is not consistently exposed in help

- **Disposition (2026-10-22): Resolved in source.**
  `cmd/service/flags.go` labels existing required query/header metadata;
  `cmd/mail_jmap.go` labels since-state. Docs metadata distinguishes optional
  member-set Space from required member-delete Space. Rendered help and existing
  parser regressions passed; conditional contracts were not made unconditional.
- **Version/evidence:** Runtime/schema comparison on 0.18.0; representative
  failures also reproduced against the source baseline.
- **Observation:** 145 flags marked required in query/header schema metadata lack
  a required marker in their rendered help. The full installed-baseline list is
  in Appendix A. Of these, 126 are in Loop: 124 workspace flags and two search
  query flags. Requiredness can have conditional exceptions, so the inventory
  records metadata rather than asserting every flag is unconditional.
- **Examples:** `bot user-info --dry-run`, `drive browse --dry-run`, and
  `loop task list --dry-run` exit 2 with `required flag(s) ... not set` for `uid`,
  `space-id`, and `workspace-id`, respectively. Their help does not label those
  flags required.
- **Sources:** `cmd/service/flags.go`, `registerQueryFlags` and
  `registerHeaderFlags`, call `MarkFlagRequired` without adding a description
  suffix. `registerBodyFlags` does add a required suffix.
- **Additional cases outside the 145-row extraction:**
  - `mail message changes --since-state` is required by
    `cmd/mail_jmap.go:90–92`, but its registration at line 115 says only
    `previous JMAP Email state`.
  - `docs members remove --principal-space-id` has contextual requiredness that
    needs to remain distinguishable from an unconditional required flag.
- **Impact:** agents can construct incomplete commands from otherwise accurate
  help; query/header/body and handwritten commands communicate different levels
  of requiredness.

### CLI-005 — Backticks in descriptions replace the rendered parameter type

- **Disposition (2026-10-22): Resolved in source.** The three descriptions in
  `internal/registry/specs/drive.json` no longer contain misleading metavar
  backticks. Actual help displays string/string/int with unchanged flag types.
- **Version/evidence:** Runtime help on both baselines; source explains the
  rendering. All three long flags accept their actual registered value types.
- **Observation:** pflag treats the first backtick-delimited text in a usage
  description as the metavar. Three generated flags therefore display command
  names or endpoint fragments where a value type is expected:

  | Command / flag | Displayed metavar | Actual schema type |
  | --- | --- | --- |
  | `drive doc mount --doc-id` | `drive doc candidates` | string |
  | `drive im-transfer create --im-group-no` | `octo-cli message search files` | string |
  | `drive im-transfer create --im-channel-type` | `blobs/im-transferred/batch` | integer |

- **Sources:** `internal/registry/specs/drive.json:829,1120–1121`;
  `cmd/service/flags.go`, `registerBodyFlags`, passes these descriptions to pflag.
- **Recorded check:** `--im-channel-type=1 --help` succeeds. The type is not the
  endpoint fragment printed by help.
- **Impact:** help scrapers and agents can infer an invalid type or operand from
  the display despite correct underlying registration.

### CLI-006 — Generic API help uses the wrong method for event polling

- **Disposition (2026-10-22): Resolved in source.** `cmd/api.go` now demonstrates
  POST event polling with an event_id body. Rendered help agrees with the
  already-inspected event spec and server route.
- **Version/evidence:** Installed/source help; CLI spec and backend source.
- **Observation:** the example is `api GET /v1/bot/events --service dmworkim`.
  Event polling is POST, with its cursor in the body.
- **Sources:** `cmd/api.go`, `newAPICmd` examples;
  `internal/registry/specs/event.json`; companion `octo-server`,
  `octo-server:modules/bot_api/bot_api.go:413`, registers `POST("/events", ...)`.
  `skills/octo-messaging/SKILL.md:190–192` already describes POST correctly.
- **Impact:** an example copied from help targets a method that the inspected
  backend does not register for this operation.
- **Boundary:** no live backend request was needed or made to demonstrate the
  disagreement between the example, embedded spec, and backend route.

### CLI-007 — API service-selector help describes a routing model the client no longer uses

- **Disposition (2026-10-22): Resolved in source.** The user selected retention
  of the public flag. `cmd/api.go` explains unified-origin/PATH routing and
  Loop protocol/error handling, with a Loop example. Help and actual Loop
  Accept-header smoke passed; request behavior was retained.
- **Version/evidence:** Help text on both baselines; behavior explanation from
  source baseline, not a complete installed-client transport experiment.
- **Observation:** `api --service` says `service key override (matters |
  dmworkim). Default: OCTO_API_BASE_URL`. `Config.ServiceURL(_ string)` returns
  the unified `APIBaseURL` irrespective of that selector. Separately, service
  `loop` affects protocol/error handling in the client but is absent from the
  help's stated choices.
- **Sources:** `cmd/api.go:122`; `internal/config/config.go:150–152`;
  `internal/client/client.go`, the service-specific Loop handling.
- **Impact:** callers can infer that the flag switches backend origins, while
  the gateway path actually selects the module. The flag is not accurately
  described as either a routing selector or a complete list of protocol modes.
- **Decision boundary:** retaining, redefining, or removing this public flag is
  a later contract decision, not settled by this audit.

### CLI-008 — Handwritten document import/export accepts dot-segment document IDs

- **Disposition (2026-10-22): Resolved in source.** Import/export reuse
  `opaquePathSegment` in `cmd/docs_import.go` and `cmd/docs_export.go`.
  Regressions reject empty/dot IDs before HTTP while accepting escaped slash
  and Unicode IDs; actual invalid-ID dry-run checks also passed.
- **Version/evidence:** Runtime export dry-run on both baselines; import path
  construction inspected in source baseline.
- **Observation:** `docs export .. --export-format md -o <isolated-path>.md
  --dry-run` succeeds and reports `/v1/bot/docs/../export/file`.
  `url.PathEscape` does not change `.` or `..` into safe opaque identifiers.
- **Sources:** `cmd/docs_export.go:77`; `cmd/docs_import.go`, the dry-run and
  real import path construction around lines 135 and 175. Compare
  `cmd/docs_scene_helpers.go`, `opaquePathSegment`, and the generated service
  path-value checks.
- **Impact:** document ID boundaries differ between generated and handwritten
  commands. A normalizing intermediary can interpret such a path differently.
- **Boundary:** URL normalization or retargeting was not exercised against a
  gateway; no unintended backend access or data change is claimed.

### CLI-009 — Some handwritten input errors are classified as configuration errors

- **Disposition (2026-10-22): Resolved in source.**
  `cmd/docs_board_comment.go` and `cmd/docs_excalidraw_export.go` use structured
  validation for the identified input branches. Classification/zero-request
  regressions and executable checks passed; transport/scene errors remain distinct.
- **Version/evidence:** CLI source baseline; no separate exhaustive runtime
  classification matrix was run for these branches.
- **Observation:** invalid board point/body combinations and portable
  Excalidraw output requirements can return ordinary Go errors. The top-level
  envelope conversion categorizes these as `config / CLI_ERROR`, unlike the
  structured validation errors used by generated commands.
- **Sources:** `cmd/docs_board_comment.go:50–90`;
  `cmd/docs_excalidraw_export.go:56–65`; `internal/cmdutil/factory.go`, generic
  error-to-envelope handling.
- **Impact:** automation grouping errors by `error.type` receives a
  configuration diagnosis for invalid command input.

### CLI-010 — Schema usage implies a domain is a standalone positional alternative

- **Disposition (2026-10-22): Resolved in source.** `cmd/schema.go` now renders
  `schema [--list [domain] | operation-id]`. Actual help was checked; the
  introspection behavior, including withheld specs, remains unchanged.
- **Version/evidence:** CLI source baseline and installed help.
- **Observation:** usage is `schema [operation-id | domain]`. A domain argument
  only selects a service when `--list` is supplied; otherwise it is looked up
  as an operation ID and fails. The long examples additionally focus on the
  withheld matter domain, although its schema introspection remains supported.
- **Sources:** `cmd/schema.go:24–33,43–54,95–103`.
- **Impact:** the short usage omits the condition under which a domain is valid.
  Withheld-domain schema introspection itself remains an intentional capability.

### CLI-011 — Document-export missing-format errors do not match its required-option wording

- **Disposition (2026-10-22): Resolved in source.** `validateDocsExport` in
  `cmd/docs_export.go` diagnoses a missing format before membership checking.
  The regression distinguishes missing and unsupported inputs without HTTP.
- **Version/evidence:** CLI source baseline.
- **Observation:** help labels `--export-format` and `--output` required.
  `validateDocsExport` checks format membership first, so omitting the format
  produces `unsupported --export-format`, rather than a missing-option diagnosis.
  These handwritten flags do not use Cobra's required annotations.
- **Sources:** `cmd/docs_export.go:49–65`.
- **Impact:** a missing value and a supplied unsupported value are reported
  identically. This is diagnostic inconsistency, not evidence that required
  validation can be bypassed.

### CLI-012 — Some rendered flag descriptions are redundant or blank

- **Disposition (2026-10-22): Resolved in source.** Scene help in
  `cmd/docs_excalidraw_export.go`, reply cursor metadata in
  `internal/registry/specs/docs.json`, and enabled metadata in
  `internal/registry/specs/loop.json` are corrected. Actual help was checked.
  The removed source project subtree remains outside this source repair.
- **Version/evidence:** Installed help and source text; runtime consumers of
  every listed optional flag were not separately exercised.
- **Observations:**
  - `docs scene export --image-format` prints `default png` in usage text as well
    as pflag's default annotation. Source: `cmd/docs_excalidraw_export.go:39`.
  - `docs comments replies --cursor` has no explanatory description.
  - `loop project search --include-closed` and
    `loop autopilot trigger-config update --enabled` have no explanatory
    description in 0.18.0. Project search is absent in the source baseline.
- **Impact:** accepted options do not consistently explain their input or
  meaning. No additional behavior is implied by the missing prose.

### CLI-013 — Document-share permission is accepted without application or the peer flags' refusal

- **Disposition (2026-10-22): Resolved in source.** `cmd/drive_share.go`
  includes permission in the existing explicit blob-only refusal.
  Regressions cover explicit default/non-default values; actual commands prove
  document refusal, plain document-link success and blob view permission application.
- **Version/evidence:** CLI source baseline; not separately runtime-reproduced
  against 0.18.0.
- **Observation:** `drive share create --permission` says `blob shares only`.
  On the document-node branch this value is not applied. The shared refusal
  list covers password/password-file/expiry, but not `permission`.
- **Sources:** `cmd/drive_share.go:93`, `blobOnlyShareFlags` around line 333,
  and the document-share branch.
- **Impact:** an explicitly supplied permission can be accepted without affecting
  the created document link, unlike the neighboring blob-only options.
- **Boundary:** the help already limits the flag to blob shares; this finding
  records inconsistent handling of an explicitly supplied inapplicable option,
  not a claim that document links should gain blob-style permissions.

### CLI-014 — Invalid timeout values fall back to a default rather than failing

- **Disposition (2026-10-22): Resolved by documentation.** The user confirmed
  retaining warning/default fallback. `cmd/root.go` help documents the 30-second
  default; an actual local request with an invalid timeout succeeded with the
  expected warning. Timeout parsing behavior was not changed.
- **Version/evidence:** CLI source baseline; not separately runtime-reproduced
  against 0.18.0.
- **Observation:** a failed `time.ParseDuration` writes a warning and retains the
  default timeout. Root help describes `--timeout` as a per-request timeout but
  does not describe this fallback behavior.
- **Sources:** `internal/client/client.go:742–752`.
- **Impact:** a caller supplying an invalid deadline can still make a request
  using a different deadline. This is an explicit existing implementation choice;
  whether documentation or behavior should change remains a later decision.

### CLI-015 — Output preflight blocks help-only service parent commands

- **Disposition (2026-10-22): Resolved in source.** `cmd/root.go` exempts a
  currently annotated service parent/group with no arguments from success-output
  preflight. This exemption is not inherited by executable leaves; ordinary
  output validation and the existing help/completion paths remain intact.
- **Version/evidence:** runtime and source review of the uncommitted output
  preflight repair, before this follow-up correction.
- **Observation:** `thread --jq '.['` and `OCTO_FORMAT=unsupported thread`
  exited 2 instead of rendering help; the same failure affected a nested
  `docs comments` help entry point. These paths call Help rather than Factory
  success formatting, so they do not consume these output options.
- **Impact:** an unrelated output setting prevented offline API discovery.
- **Verification:** four new regression cases failed at output preflight before
  the correction. Actual top-level/nested help commands then exited 0 for
  malformed jq, invalid environment format and invalid explicit format.
  Existing invalid-output business-leaf tests still reject before HTTP.

### CLI-016 — Raw jq diagnostics expose caller-provided identifiers

- **Disposition (2026-10-22): Resolved in source.**
  `internal/output/jq.go` retains eager parsing/compilation and returns static
  config/CLI_ERROR diagnostics with safe hints for both failure stages.
  Neither diagnostic retains caller identifiers, gojq details or wrapped causes.
  Valid programs and data-dependent runtime error handling are unchanged.
- **Version/evidence:** direct primitive regressions and source CLI stderr
  inspection.
- **Observation:** a fake sensitive marker supplied as an undefined jq function,
  variable or module appeared in the wrapped gojq compiler diagnostic. If a
  credential is accidentally supplied in one of those positions, the diagnostic
  copies it into stderr and any downstream error log/context.
- **Review observation (2026-10-22):** malformed syntax with a sensitive-like
  unexpected token also appeared in the raw gojq parser diagnostic.
- **Impact:** raw parse or compile diagnostics can copy caller-provided sensitive
  input into stderr and downstream logs, unlike sanitized runtime errors.
- **Verification:** direct CompileJQ tests failed for the parser token and all
  three compiler identifier forms before their corresponding corrections;
  valid expressions remained executable. Actual CLI version and write-command
  preflight emitted config/CLI_ERROR without the synthetic marker in stdout,
  stderr or error fields. The local receiver observed zero requests, while a
  valid jq filter returned the expected result.

## Skill-document findings

### SKILL-001 — Enabled shared skill invokes withheld or nonexistent matter commands

- **Disposition (2026-10-22): Resolved in source.**
  `skills/octo-shared/SKILL.md` uses active group-list selector examples;
  `internal/output/errors.go` points to supported matter schema introspection.
  Source/embedded skill review and command-discovery checks passed.
- **Version/evidence:** Installed skill and source text; Runtime on 0.18.0.
- **Observation:** the selector examples in `skills/octo-shared/SKILL.md:42–43`
  call `matter list`, while that subtree is withheld. A separate hint example at
  line 181 says `matters list`, which is not the command name even when the
  singular domain is enabled. The same plural spelling exists in the error map.
- **Recorded check:** `matter list --dry-run` exits 2, `VALIDATION_ERROR`, with
  `that is not a known command`.
- **Sources:** shared skill; `internal/output/errors.go:132`;
  `cmd/root.go`, `withholdDisabledServices`.
- **Impact:** common authentication guidance and recovery guidance lead agents
  to commands absent from the current public tree.

### SKILL-002 — Shared group pipeline selects the wrong field and treats JSON as shell records

- **Disposition (2026-10-22): Resolved in source.** The shared recipe captures
  a JSON array of group_no values and decodes it with jq into a parent-shell
  loop. Running the embedded recipe against two local groups fetched each
  correct group independently.
- **Version/evidence:** Installed/source skill; Runtime with a local group-list
  response fixture on 0.18.0.
- **Observation:** `group list --jq '.data[].id' | xargs -I{} ...` assumes `id`
  and newline-separated raw results. The response schema uses `group_no`.
  Multiple jq results are emitted as a JSON array by this CLI.
- **Recorded fixture:** two groups with `group_no` values `audit-g1`/`audit-g2`.
  The documented expression prints `[null,null]` as formatted JSON; xargs sees
  `[`, `null,`, `null`, `]`. Merely changing the field to `group_no` still gives
  a JSON array, with bracket/comma tokens passed to xargs.
- **Sources:** `skills/octo-shared/SKILL.md:198–202`;
  `internal/registry/specs/group.json`, `group.list` response;
  `internal/cmdutil/factory.go`, multiple-result jq output handling.
- **Impact:** the recipe cannot reliably call `group get` for the returned IDs.

### SKILL-003 — Shared error taxonomy misclassifies auth-service unavailability

- **Disposition (2026-10-22): Resolved by documentation.** The shared error
  table now matches `internal/output/errors.go`: network/exit 1.
  Mapping/source review and the existing error tests passed.
- **Version/evidence:** Installed/source skill; CLI source mapping.
- **Observation:** the table places `AUTH_UNAVAILABLE` under `auth_error`, exit
  3. `backendErrorMapping` maps it to `network`, exit 1, with a retry-later hint.
- **Sources:** `skills/octo-shared/SKILL.md:168–175`;
  `internal/output/errors.go:130`.
- **Impact:** skill-driven error handling can request reauthentication for a
  service-reachability failure. No unavailable auth service was invoked during
  the audit.

### SKILL-004 — Shared data-input wording overstates where the flag exists

- **Disposition (2026-10-22): Resolved by documentation.** The shared skill
  limits data-input guidance to JSON-body operations and points to per-command
  discovery for handwritten forms. Help/embedded-skill review passed.
- **Version/evidence:** Installed/source skill and installed help inventory.
- **Observation:** the skill says `--data` exists on every non-multipart command.
  Registration actually depends on a request body, with separate handwritten
  shapes. Examples without the flag include `group get`, `group members`,
  `group md-get`, `event ack`, `drive upload file`, and `drive download file`.
- **Sources:** `skills/octo-shared/SKILL.md:196`;
  `cmd/service/flags.go`, `registerBodyFlags`; handwritten drive registrations.
- **Impact:** agents can infer a universal escape hatch that those commands do
  not accept. The real raw API escape hatch has a different contract.

### SKILL-005 — Shared pagination wording conflates cursor and page-number operations

- **Disposition (2026-10-22): Resolved by documentation.** The shared skill
  distinguishes registered cursor convenience flags from page-number inputs.
  The guidance was checked against the actual command help surface.
- **Version/evidence:** Installed/source skill; Runtime on 0.18.0.
- **Observation:** the universal-flags section states that paginated operations
  expose `--page-all`/`--page-limit`; a later paragraph narrows this to cursor
  pagination. Loop task lists use page/page-size and do not accept `--page-all`.
- **Recorded check:** `loop task list --workspace-id <uuid> --page-all
  --dry-run` exits 2 because the flag is not recognized.
- **Sources:** `skills/octo-shared/SKILL.md:159–164,210`;
  `cmd/service/service.go`, pagination flag registration.
- **Installed commands exposing page-all:** `docs search`, `docs comments
  replies`, `message search`, `message search all`, `message search files`, and
  `message search media`.
- **Impact:** a general instruction in the shared skill contradicts the
  operation-specific discovery needed for most Loop/Marketplace lists.

### SKILL-006 — File attachment workflow constructs an invalid message body

- **Disposition (2026-10-22): Resolved in source.** The files recipe uses
  channel_id/channel_type and a type-8 payload with url/name/size, supported by
  `octo-server:modules/bot_api/obo_fanout_content_type_test.go`.
  Its actual multipart-upload/local-response/message-dry-run workflow passed.
- **Version/evidence:** Installed/source skill; Runtime on 0.18.0.
- **Observation:** the end-to-end example constructs
  `{chat_id, text, attachments}`. The message-send contract requires
  `channel_id`, `channel_type`, and `payload`.
- **Recorded check:** `message send --data
  '{"chat_id":"c","text":"x","attachments":[]}' --dry-run` exits 2 with
  `request body is missing required field(s): channel_id, channel_type, payload`.
- **Sources:** `skills/octo-files/SKILL.md:94–105`;
  `internal/registry/specs/message.json`, `message.send` request body.
- **Impact:** the primary upload-then-send recipe fails before HTTP. Correct
  attachment content-type/body construction still needs to be captured when
  selecting the eventual adjustment.

### SKILL-007 — File-upload query flags are described as multipart form fields

- **Disposition (2026-10-22): Resolved by documentation.** The files skill
  describes query placement. The executed upload recipe sent `?type=chat`
  alongside the multipart file body; the original path probe remains baseline evidence.
- **Version/evidence:** Installed/source skill; Runtime dry-run on 0.18.0.
- **Observation:** the skill says `--type` and `--path` become form text fields.
  The spec declares query parameters. A recorded upload dry-run produced
  `/v1/bot/file/upload?path=x&type=chat`; the multipart body carries the file.
- **Sources:** `skills/octo-files/SKILL.md:29`;
  `internal/registry/specs/file.json`; `cmd/service/multipart.go`.
- **Impact:** callers attempting to reproduce the request outside the CLI will
  put those inputs in the wrong part of the request.

### SKILL-008 — File-download redirection example names JSON as binary output

- **Disposition (2026-10-22): Resolved by documentation.** The files quick
  example explicitly describes JSON URL information and no longer names that
  output as binary content. Review preserves the existing non-streaming contract.
- **Version/evidence:** Installed/source skill and the described download
  contract; the audit did not download a live storage object.
- **Observation:** the initial example redirects `file download ... --format
  json` into `saved.bin`. The same skill subsequently explains that this command
  returns a JSON envelope containing a redirect/presigned URL, not the bytes.
- **Sources:** `skills/octo-files/SKILL.md:22,33–45`;
  generated file-download help intentionally does not expose binary `--output`.
- **Impact:** a copy-pasted recipe can create a file whose name suggests binary
  content but whose contents are a JSON envelope.

### SKILL-009 — Captured owner UID retains JSON string quoting

- **Disposition (2026-10-22): Resolved in source.** The files recipe captures
  JSON explicitly and decodes owner_uid with `jq -r`. The embedded recipe
  returned raw uid-123 against a local registration fixture even with CSV as
  the ambient default; no real Bot was registered.
- **Version/evidence:** Installed/source skill; CLI source and locally observed
  jq output rules. A real bot registration was not performed.
- **Observation:** `owner=$(octo-cli bot register --jq '.data.owner_uid')`
  captures JSON-encoded output. For a string UID, the variable includes the JSON
  quotation marks; the skill describes it as the value to cache/use later.
- **Sources:** `skills/octo-files/SKILL.md:80–88`;
  `internal/cmdutil/factory.go`, `EmitSuccess`/jq formatting.
- **Impact:** subsequent string composition can retain or double-encode quotes.
  Capturing the output is not itself an error; the missing raw-decoding step is
  the contract ambiguity to resolve.

### SKILL-010 — Drive skill does not reflect the source-baseline credential precedence

- **Disposition (2026-10-22): Resolved by documentation.** The Drive skill
  now matches the current Factory precedence, distinguishing explicit selectors
  from implicit profiles. Credential logic is unchanged; source review and
  the credential/factory regression suites passed.
- **Version/evidence:** Runtime with isolated fake profiles on both baselines;
  installed/source skill text.
- **Observation:** after storing an invented profile and supplying an invented
  `OCTO_BOT_TOKEN`, the same dry-run resolved:
  - 0.18.0: `identity.source = profile:stored-audit`;
  - source baseline: `identity.source = env:OCTO_BOT_TOKEN`.
  The Drive skill still says a stored profile takes precedence over both token
  variables. That statement agrees with 0.18.0's implicit selection but not with
  the source baseline. The source shared skill already describes the new rule.
- **Sources:** `skills/octo-drive/SKILL.md:24–30`;
  `skills/octo-shared/SKILL.md`, environment-credential section;
  `internal/cmdutil/factory.go`, credential resolution.
- **Impact:** under the source baseline, an agent relying on Drive's prose can
  act with an environment identity while assuming its stored profile won.
- **Version boundary:** this is a source-baseline documentation issue and an
  upgrade-contract change, not a demonstrated 0.18.0 precedence bug. Explicit
  selectors must be distinguished from implicit stored-profile selection.

### SKILL-011 — Drive's legacy token-variable example excludes an accepted token kind

- **Disposition (2026-10-22): Resolved by documentation.** The Drive alias
  example includes uk_. Existing alias acceptance and credential routing remain
  unchanged; the full credential regression suites passed.
- **Version/evidence:** Installed/source skill; Runtime on 0.18.0.
- **Observation:** `OCTO_BOT_TOKEN` is shown as accepting only `bf_`/`app_`, while
  the canonical slot includes `uk_`. Separate probes with invented `uk_` tokens
  in either variable succeeded as user-key dry runs and routed to
  `/v1/user/drive/spaces`.
- **Sources:** `skills/octo-drive/SKILL.md:19–21`; credential alias resolution.
- **Impact:** the skill suggests the two environment aliases accept different
  credential kinds when the observed Drive path supports a user key in either.

### SKILL-012 — Drive download prose names the wrong local error for document links

- **Disposition (2026-10-22): Resolved by documentation.** The Drive skill
  names the document branch's local validation/NOT_DOWNLOADABLE result and
  distinguishes unverified blob-server behavior. Implementation review and
  the existing Drive regression suite passed.
- **Version/evidence:** Installed/source skill; CLI source baseline.
- **Observation:** the share section says downloading either a document link
  or view-only blob share answers `permission_denied`. The document-link branch
  returns local `validation / NOT_DOWNLOADABLE` before making a request. Another
  section of the same skill already names that local error correctly.
- **Sources:** `skills/octo-drive/SKILL.md:231`;
  `cmd/drive_share.go:740–743`.
- **Impact:** agents can misdiagnose an online document as a missing-space-role
  problem and attempt an unrelated permission adjustment.
- **Boundary:** view-only blob server behavior was not established by this local
  document-link source check.

### SKILL-013 — Group-members command is labeled paginated without pagination inputs

- **Disposition (2026-10-22): Resolved by documentation.** The messaging
  skill no longer labels group-members as paginated. Actual help/spec review
  confirms the documented CLI surface; backend completeness was not inferred.
- **Version/evidence:** Installed/source skill and installed help/spec.
- **Observation:** `group members <group_no>` is annotated `# paginated`, but
  the command has no local pagination flags and the spec declares none.
- **Sources:** `skills/octo-messaging/SKILL.md:149`;
  `internal/registry/specs/group.json`, group-members operation.
- **Impact:** consumers cannot implement the documented pagination through this
  command surface. In contrast, `thread list` does expose page-index/page-size.
- **Boundary:** this does not prove whether the group backend returns a complete
  list or supports additional unmodeled inputs.

### SKILL-014 — Standard event poll loop updates the cursor inside a pipeline subshell

- **Disposition (2026-10-22): Resolved in source.** The messaging recipe uses
  Bash process substitution and explicit JSON capture. Executing that embedded
  loop against two local batches sent poll cursors 0/101 and retained cursor 202.
- **Version/evidence:** Installed/source shell example; shell-semantics analysis,
  not a live event-consumption run.
- **Observation:** `echo "$batch" | jq ... | while read ...` contains
  `cursor=$id`. Under common Bash execution semantics that loop runs in a
  subshell; the outer polling loop retains its original cursor.
- **Sources:** `skills/octo-messaging/SKILL.md:194–208`.
- **Impact:** the next poll is not reliably advanced to the processed event ID.
  Ack behavior can limit duplicate delivery, but it does not make the cursor
  assignment take effect in the parent shell.
- **Boundary:** shell behavior differs by shell/options. No backend-delivery or
  duplicate-processing result was measured here.

### SKILL-015 — Document edit reference describes a bare-array body

- **Disposition (2026-10-22): Resolved by documentation.** The doc reference
  specifies an object containing the required ops array. It agrees with the
  existing full example, schema and local object-validation contract.
- **Version/evidence:** Reference text identical in installed/source skills;
  Runtime on 0.18.0.
- **Observation:** `doc.md` says the ops batch goes through `--data` as a JSON
  array. The request body is an object with required `ops`; a later full example
  in the same reference uses that object correctly.
- **Recorded check:** `docs content edit audit-doc --base-version audit --data
  '[]' --dry-run` exits 2 with `--data is not a JSON object`.
- **Sources:** `skills/octo-docs/doc.md:17–19`;
  `internal/registry/specs/docs.json`, content-edit request body.
- **Impact:** the short instruction fails local validation even though the
  correct form appears elsewhere in the same reference.

### SKILL-016 — Forward-grant role prose disagrees with the exposed enum

- **Disposition (2026-10-22): Resolved by documentation.** The supplied
  `octo-docs-backend:src/api/routes/forwardGrant.ts` and
  `octo-docs-backend:src/permission/role.ts` explicitly accept commenter.
  `skills/octo-docs/common.md` and the summary/description in
  `internal/registry/specs/docs.json` now match the unchanged three-role enum.
- **Version/evidence:** Installed/source reference and spec; Runtime dry-run on
  0.18.0. Backend permission enforcement was not exercised.
- **Observation:** the reference says `Only reader|writer are grantable`.
  The CLI schema enum also contains `commenter`, and a dry-run with that role
  succeeds and places `"role":"commenter"` in the request body. The spec's own
  summary/description also use the narrower prose.
- **Sources:** `skills/octo-docs/common.md:124`;
  `internal/registry/specs/docs.json`, `docs.forward-grant` role enum and prose.
- **Impact:** users see inconsistent supported vocabularies in reference prose,
  schema, and flag help. Which vocabulary the target backend accepts remains a
  separate backend-validation question.

### SKILL-017 — Import recipes omit the existing-content replacement warning

- **Disposition (2026-10-22): Resolved by documentation.** The top-level,
  doc and sheet references place replacement warnings beside import guidance
  and distinguish board merge/replace. Reviewed against the existing import
  help/source; no real document content was replaced.
- **Version/evidence:** Installed/source skill and handwritten command help/source.
- **Observation:** the top-level import examples do not state that document and
  sheet imports replace existing content. Command help explicitly says:
  `Document and sheet imports atomically replace existing content.` Board
  Excalidraw import has a separate merge/replace contract.
- **Sources:** `skills/octo-docs/SKILL.md`, import examples around lines 101–103;
  `skills/octo-docs/doc.md`; `skills/octo-docs/sheet.md`;
  `cmd/docs_import.go`, command description.
- **Impact:** an agent can select an import example as a content-addition
  workflow without seeing the replacement semantics at that entry point.
- **Boundary:** the audit did not import into or replace a real document. The
  finding is a missing material warning, not a new replacement-policy decision.

### SKILL-018 — Excalidraw import version description is broader than source validation

- **Disposition (2026-10-22): Resolved by documentation.** The board reference
  requires integer version 2. Existing import-validation regressions passed;
  the underlying accepted version was not changed.
- **Version/evidence:** Installed/source reference text; CLI source baseline.
- **Observation:** the reference promises a positive integer `version` is
  validated. `validateExcalidrawEnvelope` requires integer version exactly 2 and
  emits `version must be 2` otherwise.
- **Sources:** `skills/octo-docs/board.md:72–74`;
  `cmd/docs_import.go:210–226`.
- **Impact:** a file satisfying the written description can still be rejected
  locally. The installed 0.18.0 validation branch was not separately exercised
  with a non-2 version, so its runtime equivalence remains unverified.

### SKILL-019 — Whiteboard export reference overstates local format rejection

- **Disposition (2026-10-22): Resolved by documentation.** The board reference
  distinguishes generic local format acceptance from board-backend support.
  It was reviewed against export validation and rendered help; no additional
  backend export format is promised.
- **Version/evidence:** Installed/source reference; CLI source baseline.
- **Observation:** the reference says every export-format other than png/svg is
  rejected locally. The shared `docs export` command accepts md/docx/pdf/xlsx as
  well and cannot establish the actual document type from an opaque ID alone
  during its local format check.
- **Sources:** `skills/octo-docs/board.md:114`;
  `cmd/docs_export.go`, accepted-format table and file/image path selection.
- **Impact:** supported board formats and the general command's local input
  vocabulary are conflated. No claim is made that a board backend can export to
  every format accepted by the generic local command.

### SKILL-020 — Board anchor incompatibilities are only partially described

- **Disposition (2026-10-22): Resolved by documentation.** The board reference
  and `cmd/docs_board_comment.go` help enumerate the existing incompatible flags.
  Actual help and the board conflict/validation regressions passed.
- **Version/evidence:** Installed/source reference and help; CLI source baseline.
- **Observation:** prose names incompatibility with `--data` and mutual
  exclusion of `--point`/`--element-id`. The handwritten branch also rejects
  `--parentId`, `--anchorStart`, `--anchorEnd`, `--anchorText`, `--blockPath`, and
  `--occurrence` when board-anchor flags are used.
- **Sources:** `skills/octo-docs/board.md:54–57`;
  `cmd/docs_board_comment.go:50–51`.
- **Impact:** these inherited/generated flags remain visible on the same command
  but their combination restrictions are not fully communicated.

### SKILL-021 — Common comment overview describes only document and sheet anchors

- **Disposition (2026-10-22): Resolved by documentation.** The common reference
  now includes document, sheet, board and PPT anchor forms and their detailed
  reference links. Embedded documentation review passed; no anchor API was added.
- **Version/evidence:** Reference text identical in installed/source skills.
- **Observation:** the common overview describes comments as anchored to text
  ranges or cells, while board and PPT anchor forms are documented in their
  respective references.
- **Sources:** `skills/octo-docs/common.md:7,19–20`;
  `skills/octo-docs/board.md`; `skills/octo-docs/ppt.md`.
- **Impact:** the shared entry point presents a narrower capability model than
  the type-specific references. This is a low-severity overview completeness
  issue, not an absent implementation.

### SKILL-022 — Scene deletion field description obscures its promoted flag form

- **Disposition (2026-10-22): Resolved by documentation.** Scene metadata in
  `internal/registry/specs/docs.json` explains both the existing flag and data
  forms. Actual help displays the promoted string-array flag and both options.
- **Version/evidence:** Installed help and CLI spec/source baseline.
- **Observation:** `--deletedElementIds` is available as a promoted string-array
  flag, while its property description says `Pass via --data`.
- **Sources:** `internal/registry/specs/docs.json`, scene-edit
  `deletedElementIds`; generated scene-edit help.
- **Impact:** the reference/help reader cannot tell whether `--data` is required
  or simply another supported input form. The field's prose and promoted flag
  need to communicate the same input contract.

## Loop request-contract findings

### LOOP-001 — Task search does not expose the backend-required query

- **Disposition (2026-10-22): Resolved in source.** `task.search` takes one
  required positional query:
  `octo-cli loop task search "oauth timeout" --workspace-id <ws>`.
  It sends the value as HTTP query `q` on `GET /fleet/api/v1/tasks/_search`.
  `--workspace-id`, `--page`, and `--page-size` remain flags; `--q` is not an
  alias. Other search commands and the Fleet backend/OpenAPI are unchanged
  by this CLI-shape update.
- **Version/evidence:** Runtime request construction and unknown-flag refusal on
  both CLI baselines; embedded spec plus Backend source.
- **Observation:** `loop task search` exposes page/page-size/workspace inputs,
  not `q`. A dry-run has no search query; supplying `--q audit` exits 2 because
  the flag is unknown. The generic command parser cannot supply the missing
  query via this generated leaf.
- **Backend evidence:** `octo-fleet:internal/publicapi/handler_routes.go` maps
  `SearchTasks` to `legacy.SearchIssues`;
  `octo-fleet:internal/handler/issue.go:601–604` returns HTTP 400 with
  `q parameter is required` when q is empty.
- **Spec evidence:** `internal/registry/specs/loop.json:176–191` and companion
  `octo-fleet:openapi/public-v1.yaml` omit the query. The upstream public contract
  and the embedded CLI contract share the omission.
- **Skill surface:** `octo-loop` advertises listing/searching tasks in its
  description, although it does not include a bad search invocation in its body.
- **Impact:** the exposed task-search leaf cannot express the search request
  needed by the inspected handler. Successful dry-run construction alone does
  not establish a usable operation.
- **Boundary:** the actual deployed search endpoint was not called. Additional
  backend search controls and their eventual public vocabulary were not designed.
- **Extension contract:** `x-octo-positional: "query"` is opt-in for required
  string query parameters only, never path parameters. These query operands are
  appended after existing path parameters in declared `parameters` order;
  `PositionalName` supplies the CLI label while `Name` remains the HTTP key.
  An absent extension does not change existing behavior.
- **Acceptance:** `go test ./cmd -run '^TestLoopTaskSearch' -count=1` was
  observed failing before the positional-input change and passing afterward.
  Actual source CLI requests preserved spaces, reserved characters, Unicode,
  dot and leading-dash queries under wire key `q`, together with workspace
  and pagination inputs. Missing, empty and extra query arguments, `--q`,
  and missing workspace were refused with exit 2 before HTTP. An empty
  query reported `VALIDATION_ERROR`; `loop skill search --q` remained usable.
  Rendered help and schema expose the required operand and its HTTP mapping.
  The CI-pinned golangci-lint v2.12.2 full scan with Go 1.24.0 reported no issues.
  `TestOctoLoopSkillReadExamplesExecute` also executes the actual embedded
  search, leading-dash and two-parent examples through Bash argv parsing and
  CLI request construction. Targeted example mutations failed for a flag-based
  task query, an omitted end-of-options separator and missing parent selection.
  The skill was restored byte-for-byte after each audit; coverage does not
  depend on fixed prose assertions.

### LOOP-002 — Batch child-task command cannot identify any parent tasks

- **Disposition (2026-10-22): Resolved in source.** Both specs expose optional
  comma-separated parent_task_ids. The skill distinguishes single-parent
  task child list from batch selection. Actual two-parent selection and omitted
  selection smoke passed; the backend's empty-selection behavior is retained.
- **Version/evidence:** Runtime request construction/unknown-flag refusal on
  both baselines; embedded spec and Backend source.
- **Observation:** `loop task children-by-parent --workspace-id <uuid>
  --dry-run` sends no parent query. `--parent-task-ids` is not accepted. The skill
  places this command in the workflow for reading a particular task's children.
- **Backend evidence:** `octo-fleet:internal/publicapi/normalize.go:425` maps
  `parent_task_ids` to `parent_ids`; the public facade maps the operation to
  `ListChildrenByParents`. `octo-fleet:internal/handler/issue.go:1713–1719` returns an empty
  issues collection when that value is absent.
- **Spec evidence:** `internal/registry/specs/loop.json:301–315` and companion
  `octo-fleet:openapi/public-v1.yaml` expose only pagination here.
- **Skill evidence:** `skills/octo-loop/SKILL.md:100`.
- **Impact:** a successful response can look like there are no children even
  though the CLI never named the parent selection. A distinct existing command,
  `loop task child list <task-id>`, can express single-parent lookup; this fact
  does not select the eventual batch-contract adjustment.
- **Boundary:** the empty-result consequence follows the inspected handler;
  it was not reproduced against a deployed service or a real task hierarchy.

### LOOP-003 — Metadata path key is described as a separate metadata ID

- **Disposition (2026-10-22): Resolved by documentation.** The Loop skill
  describes a persistent metadata key encoded in the path, with value in the
  body. Review against the existing CLI/Fleet schema passed.
- **Version/evidence:** Installed/source skill and schema text.
- **Observation:** the skill says the set path is keyed by a metadata ID. The
  schema describes a metadata key encoded as a path identifier, for example
  `pr_url`, and the body requires `value`.
- **Sources:** `skills/octo-loop/SKILL.md`, Metadata section;
  `internal/registry/specs/loop.json`, metadata path parameter and set body;
  companion public OpenAPI description.
- **Impact:** an agent can search for a separate ID that the documented API does
  not require, rather than using the persistent metadata key.

## Verification dependencies and wording questions

These entries began as verification dependencies rather than confirmed bugs.
Their dated dispositions distinguish the subsequently proven defect from
claims confirmed by source and validation intentionally left outside this repair.

### VERIFY-001 — HTML list pagination cannot be completed from the exposed inputs

- **Disposition (2026-10-22): Resolved in source after confirmation.**
  `octo-docs-html:internal/transport/httpx/handlers_docs.go` proves that list
  defaults to page 1/20 and accepts canonical page/page_size. The user approved
  exposing those two optional flags while leaving omitted queries unchanged.
  HTML spec/skill and a consumer pagination regression were updated; a real CLI
  fixture returned 20 items by default and only item 21 on page 2.
- **Evidence:** Installed/source skill and installed help/spec.
- **Observation:** the skill describes an offset-style data envelope plus
  `_pagination` with total/page/page_size, and explicitly says there is no cursor
  or page-all support. The CLI exposes no page/page-size/offset flags either.
- **Sources:** `skills/octo-html/SKILL.md:149–150`;
  `internal/registry/specs/html.json`, `html.list` parameters.
- **Unanswered question:** does the backend support pagination inputs that the
  CLI omits, or does this operation intentionally return a complete collection?
- **Impact:** the skill does not give an expressible way to retrieve another
  page if a response is truncated.
- **Dependency:** docs-html backend source or authoritative API contract, followed
  by an authorized read-only multi-page scenario. An independently named
  `octo-docs-html` checkout was not present among the inspected sibling roots;
  the backend may have a different repository or nested location.

### VERIFY-002 — HTML named-reader wording may underdescribe grant roles

- **Disposition (2026-10-22): Verified; no change.** The named-reader example
  is valid for reader. The supplied
  `octo-docs-html:internal/transport/httpx/handlers_grants.go` also accepts
  commenter/writer, matching the existing enum. The user requested changes only
  for demonstrated defects, so the valid example was retained.
- **Evidence:** Installed/source skill and exposed role enum.
- **Observation:** the example heading says `named-reader access`; the grant
  command exposes reader/commenter/writer roles. The example itself passes
  reader and is valid.
- **Sources:** `skills/octo-html/SKILL.md:242–245`; `html grant add` help/schema.
- **Unanswered question:** is the heading intended as an example-specific label
  or a description of all supported named grants?
- **Impact:** potentially narrow capability guidance. The wording alone does
  not prove a broken command or a server permission discrepancy.

### VERIFY-003 — Marketplace connector alias is asserted without local backend proof

- **Disposition (2026-10-22): Verified by backend source.**
  `octo-marketplace:internal/api/router/router.go` registers both aliases;
  `octo-marketplace:internal/api/handler/mcp.go` sends both to the same probe
  service. The MCP reference now distinguishes backend paths from the /market
  gateway mount. No probe behavior or route was changed; deployment was not probed.
- **Evidence:** Installed/source reference text; no matching backend validation.
- **Observation:** the MCP reference asserts a server alias at
  `POST /api/v1/connectors/_probe` while the generated command uses the retained
  MCP probe route under the Marketplace gateway namespace.
- **Sources:** `skills/octo-marketplace/mcp.md:48–50`; embedded probe operation.
- **Unanswered questions:** whether the alias is deployed, which gateway prefix
  it requires, and whether it has the same request/response contract.
- **Dependency:** Marketplace backend source or authoritative route documentation.
  The alias is not used by the skill's actual CLI probe example.

### VERIFY-004 — Marketplace tag limits and install receipt names need backend confirmation

- **Disposition (2026-10-22): Verified; no change.**
  `octo-marketplace:internal/repository/plugin/tags.go` applies default 50/max 100.
  `octo-marketplace:internal/service/plugin/install.go` selects AgentID or
  SquadID by plugin type; its handler emits agent_id or squad_id with omitempty.
  The existing skill claims match source. No installation was performed.
- **Evidence:** Installed/source skill references; no authoritative backend proof
  in the inspected CLI material.
- **Claims to verify:**
  - `plugin-tag list --limit` defaults to 50 and caps at 100
    (`skills/octo-marketplace/SKILL.md:140`).
  - expert installation returns exactly one of `agent_id` or `squad_id`
    (`skills/octo-marketplace/expert.md:141–142`).
- **Unanswered question:** whether those limits and receipt names match the
  current backend/public response, including vocabulary normalization.
- **Dependency:** Marketplace backend source/API contract and, for receipt shape,
  an authorized installation scenario or existing trustworthy response fixture.
  No installation was performed by this audit.

### VERIFY-005 — Mail execution is bounded by missing isolated authorization

- **Disposition (2026-10-22): Unchanged; not a demonstrated defect.** The user
  confirmed retaining current Mail behavior. Offline help/schema and the full
  existing regression suite were checked; real mailbox execution remains
  outside this repair and no personal mailbox credentials were used.
- **Evidence:** Runtime on 0.18.0.
- **Observation:** 18 operations stopped with `Agent Mail is not connected for
  the active Bot`; the hint requests completion of Mail authorization. Appendix
  B lists them. This is the expected local prerequisite, not an identified bug.
- **What was checked:** command/flag discovery, skill examples, and relevant
  credential/Space scoping source. No mismatched example was demonstrated.
- **What remains unverified:** authorized Mail request construction/execution,
  version/idempotency behavior, and backend receipt compatibility.
- **Dependency:** explicitly authorized test Bot/Space/mailbox context. Real
  host mailbox credentials were not copied into the isolated audit store.

### VERIFY-006 — Local and schema consistency do not establish deployment compatibility

- **Disposition (2026-10-22): Scope boundary retained.** Supplied backend
  source closed the specific questions above. No release, deployment or
  production business acceptance was performed; source/runtime-local evidence
  must not be promoted into a deployment guarantee.
- **Evidence:** Coverage boundary for the entire audit.
- **Observation:** a generated leaf can perfectly match its embedded spec while
  both omit a backend input, as LOOP-001 and LOOP-002 demonstrate. A successful
  dry-run proves construction, not backend acceptance or permission.
- **Remaining scope:** docs/html role and import/export semantics, Marketplace
  backend-only claims, and Mail execution were not comprehensively exercised
  against deployed services. Local server/Fleet source checks cover only the
  concrete paths identified in their findings.
- **Dependency:** the relevant authoritative service contract/source and an
  agreed test environment for any later live validation. Existing sibling
  server/Fleet repositories were sufficient for their recorded source checks;
  no blanket claim is made that other service code is unavailable elsewhere.

## Source-baseline changes relative to the installed release

These entries are version deltas, not automatically defects. Matching a source
skill to a source command does not make that command available in 0.18.0.

### DELTA-001 — Generic API adds an explicit workspace header flag

The source baseline adds `api --workspace-id`. Runtime checks showed 0.18.0
rejecting the flag, while the source build includes `X-Workspace-ID` in dry-run
headers. The source shared skill adds the matching usage. The API passthrough
still has a different contract from generated Loop operations.

### DELTA-002 — Low-level upload adds a capability header

The source baseline adds `drive upload prepare --upload-capabilities`.
0.18.0 rejects the flag; the source build sends
`X-Drive-Upload-Capabilities: multipart-v1` when requested. This is a header, not
an extra JSON body field. The source Drive skill documents it.

### DELTA-003 — Drive adds multipart transfer operations and orchestration

New source commands are `drive upload parts-presign`, `complete`, and `abort`.
The source Drive skill adds multipart scheduling, completion, error/recovery,
and low-level operation descriptions. The installed skill describes its own
single-part prepare/PUT/confirm flow; that version-matched description is not
itself a stale-doc defect. This audit did not run a multipart transfer or validate
its documented retry/concurrency/recovery behavior against storage.

### DELTA-004 — Expert-team evaluation moves to a different command path

The source adds `loop expert-team evaluate` and removes
`loop task team-evaluation record` plus its now-empty command group. The new
help exposes outcome/reason/workspace inputs. This inventory does not decide
migration messaging or validate the new backend evaluation operation.

### DELTA-005 — Loop project commands and selected project fields are removed

The installed release has a `loop project` subtree. The source baseline removes
that subtree and removes `--project-id` from task quick-create and autopilot
create/update. The source Loop skill removes project references consistently.
The exact removed node list is in Appendix C. Old project examples are valid
only for their recorded installed baseline, not the new source tree.

### DELTA-006 — Loop workspace guidance becomes UUID-specific

113 shared command-help entries change workspace wording from selector to UUID.
The source Loop skill explicitly distinguishes the UUID from the Web slug.
This records a clarification in the public input guidance; the audit did not
introduce new UUID validation or prove deployment-specific workspace selection.

### DELTA-007 — Authentication guidance and implicit precedence change

The source shared/Loop skills document the canonical `OCTO_TOKEN`, its compatible
alias, and task-scoped `OCTO_CREDENTIAL_MODE=task` usage with `OCTO_TASK_TOKEN`.
The installed binary already accepted the canonical variable and a user key in
either alias in the recorded probes. Documentation omission is not evidence of
missing canonical-variable support.

The directly measured behavioral difference is implicit stored-profile versus
environment-token precedence, recorded under SKILL-010. The source Drive skill
has not caught up with that change. The source docs/html/files main skill
changes otherwise mainly add the canonical credential-variable spelling; their
reference Markdown bodies match the installed copies.

## Existing behaviors to keep distinct from confirmed discrepancies

The following source/usage boundaries were inspected and do not by themselves
justify adding features or changing behavior:

- Optional query flags can show schema defaults while an omitted flag is not
  sent; backend-owned defaults are an existing contract.
- `--page-limit` is a control for `--page-all`. Source pagination logic uses 10
  when its supplied value is not positive. Ordinary requests do not consume it.
  Whether to change that input policy is outside an already-selected fix.
- `--symbol` is explicitly described as a currency symbol for `--currency`;
  `--pattern` explicitly overrides the source default. Other combinations do not
  establish a missing generic symbol feature.
- Global `--space` registration does not mean every service accepts a Space
  header. Specs opt into header behavior and Mail uses it for authorization
  scope; service-specific scope semantics must be preserved.
- String-slice flags use pflag's comma/repetition behavior. That is different
  from the handwritten repeatable board `--element-id` string-array flag, whose
  commas are preserved.
- Schema introspection for a withheld domain remains supported even though its
  normal command tree and enabled skill are not listed.
- Skill descriptions are not intended to enumerate every optional flag. Using
  a valid `--data` body instead of its promoted flags is not a mismatch.

## Follow-up recording and completion rules

The source repair pass prioritized observable write risk and executable recipes,
then help/diagnostic consistency. Newly verified backend omissions were handled
after their scope was confirmed. Future changes should continue to separate
demonstrated defects from version differences and unverified deployment claims.

For a later resolution, record in the relevant existing finding:

1. The approved adjustment and any changed public contract, without silently
   treating a version difference or verification dependency as a confirmed bug.
2. The authoritative source of truth and repositories that must change together.
3. A before/after reproduction or source-only verification boundary.
4. Consumer-visible acceptance evidence: parsing alone is insufficient for
   forwarding, side effects, identity, response shape, or shell recipes.
5. Updated affected skills/help/specs/callers and any remaining deployment limit.

The inventory and its dated dispositions are checked for stable unique IDs,
local/companion references, coverage totals, version attribution and explicit
verification limits. Main reviewed the parallel implementations, including jq
compilation timing, completion behavior and rendered schema-description placement.
This record introduces no release, migration or deployment authorization.

## Appendix A — Required schema flags without a help required marker

Installed 0.18.0 snapshot: 145 command/flag pairs. This is metadata-based;
conditional requiredness and handwritten checks need the separate treatment in
CLI-004. Source-baseline removals are version deltas, not changes to this snapshot.

### bot: `--uid` (1)

- `bot user-info`

### drive: `--space-id` (4)

- `drive blob list`
- `drive browse`
- `drive doc candidates`
- `drive doc list`

### file: `--filename` (2)

- `file credentials`
- `file presigned`

### html: `--slug` (1)

- `html comment list`

### loop: `--q` (2)

- `loop project search`
- `loop skill search`

### loop: `--workspace-id` (124)

- `loop attachment content get`
- `loop attachment delete`
- `loop attachment download`
- `loop attachment get`
- `loop attachment upload`
- `loop autopilot create`
- `loop autopilot delete`
- `loop autopilot delivery get`
- `loop autopilot delivery list`
- `loop autopilot delivery replay`
- `loop autopilot get`
- `loop autopilot list`
- `loop autopilot run get`
- `loop autopilot run list`
- `loop autopilot signing-secret set`
- `loop autopilot trigger`
- `loop autopilot trigger-config create`
- `loop autopilot trigger-config delete`
- `loop autopilot trigger-config update`
- `loop autopilot update`
- `loop autopilot webhook-token rotate`
- `loop comment delete`
- `loop comment reaction add`
- `loop comment reaction remove`
- `loop comment reopen`
- `loop comment resolve`
- `loop comment update`
- `loop execution cancel`
- `loop execution message list`
- `loop expert archive`
- `loop expert create`
- `loop expert create-from-template`
- `loop expert environment get`
- `loop expert environment update`
- `loop expert execution cancel`
- `loop expert execution list`
- `loop expert get`
- `loop expert list`
- `loop expert restore`
- `loop expert skill add`
- `loop expert skill list`
- `loop expert skill replace`
- `loop expert update`
- `loop expert-team create`
- `loop expert-team delete`
- `loop expert-team get`
- `loop expert-team list`
- `loop expert-team member add`
- `loop expert-team member list`
- `loop expert-team member remove`
- `loop expert-team member update`
- `loop expert-team member-status list`
- `loop expert-team update`
- `loop expert-template get`
- `loop expert-template list`
- `loop label create`
- `loop label delete`
- `loop label get`
- `loop label list`
- `loop label update`
- `loop project create`
- `loop project delete`
- `loop project get`
- `loop project list`
- `loop project resource create`
- `loop project resource delete`
- `loop project resource list`
- `loop project resource update`
- `loop project search`
- `loop project update`
- `loop runtime activity list`
- `loop runtime local-skill-import get`
- `loop runtime local-skill-request get`
- `loop runtime model-request get`
- `loop runtime update-request get`
- `loop runtime usage get`
- `loop runtime usage-by-expert get`
- `loop runtime usage-by-hour get`
- `loop skill create`
- `loop skill delete`
- `loop skill get`
- `loop skill import`
- `loop skill list`
- `loop skill search`
- `loop skill update`
- `loop skill-file delete`
- `loop skill-file list`
- `loop skill-file upsert`
- `loop task active-execution list`
- `loop task attachment list`
- `loop task child list`
- `loop task child-progress`
- `loop task children-by-parent`
- `loop task comment create`
- `loop task comment list`
- `loop task comment preview`
- `loop task create`
- `loop task delete`
- `loop task execution cancel`
- `loop task execution list`
- `loop task get`
- `loop task group`
- `loop task label add`
- `loop task label list`
- `loop task label remove`
- `loop task list`
- `loop task metadata delete`
- `loop task metadata list`
- `loop task metadata set`
- `loop task pull-request list`
- `loop task quick-create`
- `loop task reaction add`
- `loop task reaction remove`
- `loop task rerun`
- `loop task search`
- `loop task subscribe`
- `loop task subscriber list`
- `loop task team-evaluation record`
- `loop task timeline list`
- `loop task unsubscribe`
- `loop task update`
- `loop task usage`
- `loop workspace get`
- `loop workspace member list`

### mail: `--idempotency-key` (3)

- `mail draft create-agent`
- `mail message reply-draft`
- `mail message send-intent`

### marketplace: `--mode` (1)

- `marketplace plugin review-request list`

### marketplace: `--plugin-id` (4)

- `marketplace plugin download`
- `marketplace plugin get`
- `marketplace plugin skillmd`
- `marketplace plugin version list`

### marketplace: `--plugin-type` (1)

- `marketplace plugin-category list`

### marketplace: `--scene-code` (2)

- `marketplace plugin list`
- `marketplace plugin-category list`

## Appendix B — Mail operations stopped at authorization

- `mail address list`
- `mail draft create-agent`
- `mail draft delete`
- `mail draft list`
- `mail draft send`
- `mail draft update`
- `mail mailbox list`
- `mail me`
- `mail message attachment download`
- `mail message auto-reply-context`
- `mail message delivery`
- `mail message flag`
- `mail message list`
- `mail message raw`
- `mail message read`
- `mail message reply-draft`
- `mail message send-intent`
- `mail thread get`

## Appendix C — Command-node version delta

### Added in the source baseline

- `drive upload abort`
- `drive upload complete`
- `drive upload parts-presign`
- `loop expert-team evaluate`

### Removed from the source baseline

- `loop project`
- `loop project create`
- `loop project delete`
- `loop project get`
- `loop project list`
- `loop project resource`
- `loop project resource create`
- `loop project resource delete`
- `loop project resource list`
- `loop project resource update`
- `loop project search`
- `loop project update`
- `loop task team-evaluation`
- `loop task team-evaluation record`
