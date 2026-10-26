# Octo CLI skills and help consistency audit — step-3

Date: 2026-10-23

Status: authorized scope implemented and locally accepted on 2026-10-23; 26 entries addressed, nine retained by user decision and three capability candidates excluded.

## Purpose and maintenance

This document records the source-built CLI audit of embedded skills, every in-scope command/group help page, registered parameters, request construction and selected companion-backend contracts. The original problem register separates audit observations from the subsequent implementation decisions and verification.

Earlier audits and their dated dispositions remain in [step-1](2026-10-22-skills-help-consistency-audit-step-1.md) and [step-2](2026-10-22-skills-help-consistency-audit-step-2.md). Their recorded repairs are not reopened by this document. An overlap is identified where relevant: a new observation can concern a narrower boundary that the earlier acceptance did not exercise.

Each entry has a stable `AUD3-NNN` ID and records the affected surface, discrepancy, consumer impact, evidence and remaining uncertainty. To process an entry later, append the dated decision, selected handling, affected repositories/files, implementation reference and observed verification to that same entry. Keep the audit-time observation. Do not renumber IDs or replace an observation with a proposed solution.

The initial disposition of every entry was **Recorded; handling not selected**. Later dispositions distinguish locally verified resolution, an explicitly retained contract, or an external verification dependency. A source edit or a passing test alone does not establish resolution; record the behavior that closes the discrepancy.

### Authorized implementation scope

The user authorized implementation and acceptance of this register on 2026-10-23, with these explicit decisions:

- AUD3-024: retain the existing Docs sharing-role vocabulary, `read|edit`.
- AUD3-026–033: leave all HTML behavior, schemas and skills unchanged. The recorded differences and external deletion-policy uncertainty remain historical findings, not verified repairs.
- AUD3-036–038: do not add the three capability candidates.
- Implement the remaining existing-surface contract and guidance corrections. Registration caching is CLI housekeeping guidance, not a prohibition on legitimate runtime re-registration.
- Continue excluding Agent Mail, Matter and Smart Summary. No backend implementation, deployment, release or installed-binary replacement is authorized.

Acceptance uses a fresh build from this checkout, targeted failing-before/passing-after behavior checks, the repository checks and in-scope command/help/schema/embedded-skill verification. Synthetic dry-runs and local HTTP receivers do not establish deployed-service acceptance.

The observation, runtime and uncertainty bullets below retain the original audit snapshot. Each dated disposition describes the later handling and its verification; it does not overwrite the historical reproduction.

## Audit baseline and evidence boundaries

### Executable identity and scope

The runtime evidence below used only the executable built from source for this audit:

```sh
go build -o /tmp/octo-cli-current-audit ./cmd/octo-cli
```

| Baseline | Recorded value |
| --- | --- |
| Executed binary | `/tmp/octo-cli-current-audit` |
| Binary SHA-256 | `1d0fc32c8457159b1fef582080691e3c2b80ddaaff98bcf7e234ebf968575007` |
| Version output | `version=dev`, `commit=none`, `build_date=unknown` |
| Source commit provenance | A clean source commit ID was not captured for this pass; the hash identifies the executed binary, not companion-backend revisions |
| Included skills | `octo-shared`, `octo-messaging`, `octo-files`, `octo-drive`, `octo-docs`, `octo-html`, `octo-loop`, `octo-marketplace` |
| Excluded domains | Agent Mail (`octo-mail`), Matter (`matters` / `octo-matter`), Smart Summary (`smart-summary` / `octo-summary`) |
| Companion sources inspected | `octo-server`, `octo-drive`, `octo-docs-backend`, `octo-docs-html`, `octo-fleet`, `octo-marketplace` |
| Network/mutation boundary | Invented credentials, isolated configuration and a loopback API origin; no live business API requests or deletion probes |

In the recorded probes, ambient `OCTO_*` settings were removed and the audit supplied `OCTO_TOKEN=bf_audit_synthetic`, `OCTO_API_BASE_URL=http://127.0.0.1:1` and `OCTO_CONFIG_DIR=/tmp/octo-cli-current-audit-no-profiles`. This is a synthetic token, not an operational credential. Every business-operation reproduction shown below includes `--dry-run`.

Paths without a repository prefix refer to `octo-cli`. Companion references use `repository:path:line`, not a presumed relative checkout location. Line numbers refer to the inspected snapshot and can move; operation IDs and symbols are the durable navigation anchors. The findings are audit-snapshot observations, not a claim that every later checkout or deployment still has the discrepancy.

### Recorded coverage

| Check | Observed result | Evidence boundary |
| --- | --- | --- |
| Help traversal | 412 in-scope nodes, including groups/root; zero failures | Every reachable in-scope help page was actually invoked |
| Registered flag parsing | 5,017 instances, including inherited repetitions; zero parse failures | Names and representative typed values parse; backend consumption is a separate question |
| Formal help flag sections | No missing visible registered flags or unsupported formal flag definitions | References to other commands inside prose/examples were reviewed separately |
| Schema discovery | 293 in-scope operations; zero lookup failures | Registry introspection, not deployment coverage |
| Schema-to-command mapping | 711 nonreserved parameter/promoted-field mappings checked | `drive.share.blob-create` intentionally takes body `file_id` as positional `<file-id>` |
| Embedded skill provenance | Eight skills, 16 Markdown documents; exact match with the audited source | Includes progressive-disclosure reference documents |
| Parsed skill snippets | 439 snippets; no unknown long or short options | Parsing includes multiline shell snippets and inline commands; it is not execution of all recipes |
| Concrete schema references in skills | 41 lookups; zero failures | Generic placeholder-based discovery advice is separately covered by AUD3-003 |
| Targeted runtime cases | 34 recorded command/help/validation/request-construction cases | Dry-run does not prove a deployed backend accepted the request |

The central result is that **formal help flag registration is consistent, while several descriptions, examples, constraints and backend contracts are not**. An accepted flag or successful dry-run is not evidence that an example satisfies backend semantics.

### Evidence levels

- **Runtime:** output observed from the source-built executable. A dry-run establishes local validation, serialization and the described URL/headers/body only.
- **CLI source:** embedded spec, skill or implementation supports the statement. This can explain a rendered help discrepancy but does not establish backend acceptance.
- **Backend source:** the inspected handler, DTO, validator or query builder supports the statement. This does not establish the revision or policy enforced by a running deployment.
- **Policy/deployment dependency:** a narrower product boundary or external enforcement remains unconfirmed. Such an entry must not be promoted to a confirmed exploit or silently resolved by expanding permissions.

## Skill coverage map

| Skill | Entries | Audit assessment |
| --- | --- | --- |
| `octo-shared` | Cross-domain evidence boundaries; AUD3-018 and AUD3-036–038 intersect generic API/error guidance | No demonstrated invalid shared flag or identity recipe in this pass |
| `octo-drive` | AUD3-001–004 | Existing workflows remain usable; search filtering has a substantive example/contract mismatch |
| `octo-messaging` | AUD3-005–006, AUD3-009–017 | Existing surface remains usable; single-channel search examples omit a backend-required value |
| `octo-files` | AUD3-007–008, AUD3-018–020 | Existing surface remains usable; pagination, metadata identity and size-policy guidance need separate treatment |
| `octo-docs` and references | AUD3-021–025 | Type discovery, membership/sharing contracts and attachment constraints differ from the inspected backend |
| `octo-html` | AUD3-026–033 | Grant semantics, permission descriptions and several response schemas differ; deletion remains a static-review dependency |
| `octo-loop` | AUD3-034, AUD3-036 | Audited current task flags/fields match; additional semantics and a distinct capability candidate are recorded |
| `octo-marketplace` and references | AUD3-035, AUD3-037–038 | Existing installation contract remains supported; dependency visibility and additive APIs need separate decisions |

## Drive findings

### AUD3-001 — Search type filter uses the wrong field name and shape

- **Classification:** confirmed skill/spec/backend mismatch; result-selection risk.
- **Observed contract:** the search recipe sends `{"filters":{"type":"blob","owner_scope":"me"}}`; advanced-filter guidance names scalar `type` with values `folder|doc|blob`.
- **Backend contract:** `search.Filters` exposes `Types []string` as JSON `types`. `buildDriveQuery` adds the type restriction only when `Filters.Types` is nonempty. The corresponding backend shape is `{"filters":{"types":["blob"],"owner_scope":"me"}}`.
- **Runtime:** `drive search --q audit --data '{"filters":{"type":"blob"}}' --dry-run` exits 0 and retains scalar `filters.type`. A second dry-run with `filters.types:["blob"]` also exits 0 and retains that array.
- **Impact:** the documented request can appear successful while failing to narrow results to the requested node type. The CLI accepts an object shape that does not populate the backend's type filter.
- **Sources:** `skills/octo-drive/SKILL.md:181–184`; `internal/registry/specs/drive.json:245–262`; `octo-drive:internal/search/query.go:19–24,75–82`; `octo-drive:internal/search/api.go:51–58`; `octo-drive:internal/search/service.go:432–447`.
- **Boundary:** the filter omission follows from the inspected JSON binding and query builder. No live search result set was fetched.
- **Status / handling (2026-10-23):** Guide and schema aligned locally.
  - **Implementation:** `skills/octo-drive/SKILL.md` uses `filters.types:["blob"]`; `internal/registry/specs/drive.json` models the nested array and filter properties. The ownership vocabulary includes the backend's empty no-filter value.
  - **Verification:** source-built dry-runs preserve the correct object, accept all three node types and empty/me/others ownership values, and reject invalid vocabulary members. The existing vocabulary contract table records remote `origin/main@2db4df5` provenance.
  - **Review follow-up (2026-10-23):** optional `size_min`, `size_max` and `owner_scope` explicitly admit JSON null through OpenAPI 3.1 type unions; ownership also lists null in its enum. Registry projection retains declared nullability. Real-command regressions and fresh CLI smoke confirm null preservation, exact non-null uint64 bounds and invalid-value refusal; non-nullable IDs remain protected.

### AUD3-002 — Search help refers to a nonexistent `--filters` option

- **Classification:** confirmed help-prose mismatch, separate from the JSON field error in AUD3-001.
- **Observation:** the operation description calls the advanced object `--filters` while also saying it is supplied through `--data`. No standalone `--filters` flag is registered.
- **Runtime:** `drive search --filters '{}' --dry-run` exits 2 with `VALIDATION_ERROR` and `a flag in the command line was not recognised`. Structured filters supplied through `--data`, with required `--q`, reach request construction.
- **Impact:** help can lead an agent to invent an invalid invocation even though the formal Flags section and the skill's transport mechanism are correct.
- **Sources:** `internal/registry/specs/drive.json:245–262`; rendered `drive search --help`.
- **Status / handling (2026-10-23):** Help and guide aligned locally.
  - **Implementation:** search metadata describes the nested object supplied through `--data`.
  - **Verification:** actual search help and the formal Flags section match registered options; the corrected structured-filter recipe constructs the intended body.

### AUD3-003 — Generic schema lookup advice does not cover composite commands

- **Classification:** confirmed discovery-guidance mismatch.
- **Observation:** the Drive guide recommends `schema drive.<group>.<verb>` for the wire contract. Composite CLI leaves do not necessarily have an operation ID matching their command path.
- **Runtime:** `schema drive.upload.file`, `schema drive.download.file` and `schema drive.share.create` each exit 2 with `unknown operation "<id>"`. Their actual command help remains available. For example, `drive download file --help` identifies underlying operation `drive.download.url`; upload help lists its multiple underlying operations.
- **Impact:** a valid CLI command leads to a failed schema query and obscures the distinction between a composite workflow and an individual backend request.
- **Sources:** `skills/octo-drive/SKILL.md:313`; rendered help for `drive upload file`, `drive download file` and `drive share create`.
- **Boundary:** this does not require inventing schemas for composite commands; how discovery should explain them remains undecided.
- **Status / handling (2026-10-23):** Discovery guidance aligned locally.
  - **Implementation:** the Drive guide directs callers to command help and its exact underlying operation IDs.
  - **Verification:** composite help and underlying schema lookups succeed. The three composite command paths still correctly fail as unknown operation IDs; no synthetic wire schemas were added.

### AUD3-004 — Conflict-selector guidance does not state that the current backend ignores it

- **Classification:** documentation precision gap and backend-comment inconsistency; not a missing CLI capability.
- **Observation:** move/copy expose `--on-conflict`, and the skill includes `rename` examples. Current CLI help already leaves the outcome to the backend rather than guaranteeing an automatic rename.
- **Backend contract:** `resolveDestName` explicitly accepts and ignores `onConflict`, returning the supplied name unchanged. It documents that duplicate sibling names are allowed. Earlier comments on the move/copy paths still describe numbered renaming.
- **Impact:** a caller may infer collision behavior from the selector or older comments that the current helper does not implement.
- **Sources:** `skills/octo-drive/SKILL.md:160–169`; corresponding move/copy flag descriptions; `octo-drive:internal/modules/file/service.go:234–236,465–468,504–507,713–731`.
- **Boundary:** multipart capability headers and cross-space `target_space_id` fields matched the inspected backend; those existing capabilities are not reopened. The desired future conflict policy has not been selected.
- **Status / handling (2026-10-23):** Current conflict behavior documented.
  - **Implementation:** move/copy field descriptions and the Drive guide explain that the current backend accepts but ignores the selector and permits duplicate sibling names.
  - **Verification:** all three move/copy schemas and command help were exercised; behavior remains supported by the inspected `resolveDestName` source, not a live collision probe.

## Messaging, group and thread findings

### AUD3-005 — Single-channel search examples omit required `channel_type`

- **Classification:** confirmed example/backend mismatch.
- **Observation:** the `search`, `search all`, `search files`, `search media` and `search around` single-channel examples pass `--chat-id` but omit `--channel-type`. Later prose only says to provide a type when needed. The schema/help does not explain its conditional requiredness.
- **Backend contract:** `validateBase` rejects every channel type other than 1, 2 or 5, including missing JSON decoding to 0. Thread channel IDs also have their own format requirement. Cross-channel message/file search is a different route and does not inherit this single-channel requirement.
- **Runtime:** all five documented single-channel shapes reach dry-run with a body lacking `channel_type`; `internal/client/search_route.go` selects an endpoint but does not infer or inject the type. A probe with explicit `--channel-type 2` preserves it.
- **Impact:** valid-looking skill commands assemble requests that fail the inspected backend's validation.
- **Sources:** `skills/octo-messaging/SKILL.md:109–127,135–151`; `internal/registry/specs/message.json` search operations; `internal/client/search_route.go:24–60`; `octo-server:modules/messages_search/validate.go:91–102`.
- **Prior boundary:** step-2 MESSAGE-005 recorded channel-type discovery/paging guidance. This observation concerns the concrete example bodies and conditional requirement, not the existence of the flag.
- **Status / handling (2026-10-23):** Single-channel examples and help aligned locally.
  - **Implementation:** all five examples supply the group channel type, and search metadata describes the conditional requirement and valid channel types.
  - **Verification:** five explicit-type recipes preserve `channel_type:2`; three cross-channel recipes still route globally. The CLI does not infer the type or add a new local conditional gate.

### AUD3-006 — Thread creation does not declare the backend OBO input

- **Classification:** contract coverage gap; exposure as a dedicated flag remains a decision.
- **Observation:** the backend accepts optional body `on_behalf_of` when creating a thread. The CLI schema/help declares `name` and `source_message_id`, not the OBO field.
- **Runtime:** `thread create audit-group --name audit --on-behalf-of audit-user --dry-run` exits 2 for an unrecognized flag. Replacing that flag with `--data '{"on_behalf_of":"audit-user"}'` exits 0 and retains the field.
- **Backend semantics:** the target user needs an active grant and parent-group read scope; the field changes the thread-creation identity. Backend authorization remains authoritative.
- **Impact:** schema/help consumers cannot discover this supported identity-sensitive path; knowing a generic JSON escape hatch is not equivalent to a declared contract.
- **Sources:** `internal/registry/specs/thread.json:13–30`; `octo-server:modules/bot_api/threads.go:65–124`.
- **Status / handling (2026-10-23):** Existing OBO input exposed.
  - **Implementation:** thread-create metadata and the messaging guide declare `--on-behalf-of`, including active-grant and parent-group-read prerequisites.
  - **Verification:** the pre-change binary rejects the flag; the fresh binary constructs `on_behalf_of:"audit-user"`. Backend authorization was source-reviewed, not exercised with a live grant.

### AUD3-007 — Bot registration does not declare `agent_hosting`

- **Classification:** contract coverage gap.
- **Observation:** the User Bot registration handler accepts body `agent_hosting`; it is absent from the CLI schema and help.
- **Runtime:** `bot register --agent-hosting audit --dry-run` exits 2 for an unrecognized flag. `bot register --data '{"agent_hosting":"audit"}' --dry-run` exits 0 and preserves the field.
- **Backend semantics:** the inspected implementation normalizes an ASCII slug, limits it to 64 bytes, uses `none` to retract the hosting marker, and treats an empty value as unchanged. It is an open vocabulary, not a finite enum. Invalid metadata does not make the registration itself fail. App Bot behavior is separately covered by AUD3-019.
- **Impact:** callers cannot discover the current metadata field or its update semantics through schema/help.
- **Sources:** `internal/registry/specs/bot.json:13–38`; `octo-server:modules/bot_api/register.go:44–90,120–177,199–212,399–424`.
- **Status / handling (2026-10-23):** Existing registration metadata exposed.
  - **Implementation:** bot metadata and the files guide declare `--agent-hosting` and its User Bot update semantics without a finite enum or new local slug validation.
  - **Verification:** the pre-change binary rejects the flag; fresh dry-runs preserve a normal slug, an invalid report and an explicit empty value for backend handling.

### AUD3-008 — Space-member listing cannot express the backend page query

- **Classification:** contract coverage gap with collection-completeness impact.
- **Observation:** the backend accepts query `page`; CLI help exposes `keyword`, `limit` and `space-id` but no page selector. Skill guidance describes the limit without explaining later pages.
- **Runtime:** `bot space-members --page 2 --dry-run` exits 2 for an unrecognized flag.
- **Backend semantics:** pages are one-based; omitted page is 1. The inspected handler bounds the page at 1,000,000, defaults limit to 50, resets values below 1 to that default and caps it at 200.
- **Impact:** the generated operation cannot enumerate members past its first page. A JSON body field is not equivalent to this GET query parameter.
- **Sources:** `internal/registry/specs/bot.json:114–148`; `skills/octo-files/SKILL.md` space-members section; `octo-server:modules/bot_api/groups.go:27–31,301–334`.
- **Prior boundary:** step-2 FILE-002 recorded the then-declared optional controls. This is the additional page input, not a claim that the existing limit flag is absent.
- **Status / handling (2026-10-23):** Manual member paging exposed.
  - **Implementation:** bot metadata and the files guide add the one-based `page` query and explain defaults/caps.
  - **Verification:** the pre-change binary rejects `--page`; the fresh binary describes GET query `page=2&limit=20` without a body. Automatic `--page-all` remains unavailable for this operation.

### AUD3-009 — Around-search context window size is absent from the schema/help

- **Classification:** contract coverage gap, not cursor pagination.
- **Observation:** backend body `page_size` controls the context window; the CLI around-search schema does not declare it.
- **Runtime:** a baseline with `--chat-id audit-group --channel-type 2 --anchor-message-id 1` exits 0 in dry-run. Adding `--page-size 10` exits 2 for an unrecognized flag. Supplying `--data '{"page_size":10}'` instead exits 0 and preserves the field.
- **Backend semantics:** omitted/zero size defaults to 20; nonzero values must be 1–100. Results expose `has_more_before/after` but no resumable cursor.
- **Impact:** callers cannot discover how to control the context window. The guide's no-cursor-pagination statement is not itself false and should not be conflated with a fixed-size window.
- **Sources:** `internal/registry/specs/message.json:313–335`; `octo-server:modules/messages_search/validate.go:81–99,139–167`; `octo-server:modules/messages_search/search_around.go:14–22,117–145`.
- **Status / handling (2026-10-23):** Context-window size exposed.
  - **Implementation:** around-search metadata and the guide declare `--page-size`, zero/omitted default 20 and nonzero range 1–100, distinct from cursor pagination.
  - **Verification:** the pre-change binary rejects the flag; the fresh binary constructs `page_size:10` with the existing explicit channel/anchor inputs.

### AUD3-010 — Grouped search does not declare the response-correlation sequence

- **Classification:** contract coverage gap; low functional urgency.
- **Observation:** backend body `sequence:int64` is returned to the caller; CLI schema/help omits it.
- **Runtime:** `message search groups --keyword audit --sequence 1 --dry-run` exits 2. `--data '{"sequence":1}'` with the same keyword exits 0 and preserves it.
- **Impact:** clients cannot discover the supported mechanism for recognizing a stale response.
- **Sources:** `internal/registry/specs/message.json:347–363`; `octo-server:modules/messages_search/search_global_groups.go:18–27,98–118`.
- **Boundary:** sequence is not a page selector, does not enable pagination and is not checked for monotonicity.
- **Status / handling (2026-10-23):** Response-correlation input exposed.
  - **Implementation:** grouped-search request and response schemas declare `sequence:int64`; the guide describes correlation rather than pagination.
  - **Verification:** the pre-change binary rejects `--sequence`; the fresh binary preserves `sequence:1` and exposes it through schema/help.
  - **Review follow-up (2026-10-23):** the response schema places `sequence` under backend `data.properties`, and the guide identifies `.data.data.sequence` in CLI JSON output. Schema-driven extraction initially failed at the response root; after the metadata correction it resolves the echoed value from a local HTTP fixture matching the backend DTO. Runtime response wrapping is unchanged.

### AUD3-011 — Event listing is described as the newest page rather than the next ascending batch

- **Classification:** confirmed ordering-description mismatch.
- **Observation:** the no-argument `event list` example is labeled `newest page`.
- **Backend contract:** the queue read uses an exclusive lower bound after `event_id`, a maximum of `+inf` and ascending event IDs. Cursor 0 starts at the earliest queued event, not a reverse/latest page.
- **Impact:** agents can misunderstand backlog order or incorrectly describe which events were consumed. The existing cursor-poll recipe and optional server wait are separate contracts.
- **Sources:** `skills/octo-messaging/SKILL.md:193–207`; `octo-server:modules/bot_api/events.go:50–80,255–273`.
- **Boundary:** default limit 20, maximum 100, immediate polling without wait and the 30-second wait cap matched the inspected implementation; they are not new findings.
- **Status / handling (2026-10-23):** Ordering guidance aligned.
  - **Implementation:** the messaging guide describes the next ascending batch and the exclusive cursor.
  - **Verification:** the embedded guide matches source and the queue-read source was reviewed; no live queue ordering or event acknowledgment was tested.

### AUD3-012 — Group member arrays lack the backend's nonempty constraint

- **Classification:** request-constraint coverage gap.
- **Observation:** create/member-add/member-remove require the `members` property but do not declare `minItems: 1`.
- **Runtime:** `group create --data '{"members":[]}' --dry-run` exits 0 and constructs the empty array. The backend rejects empty lists for all three operations. Create/add also enforce an upper bound of 200, which is already represented in the relevant spec.
- **Impact:** local acceptance is weaker than the backend contract for an operation whose primary input is the member list.
- **Sources:** `internal/registry/specs/group.json:169–183,240–284`; `octo-server:modules/bot_api/groups.go:428–435,632–641,735–742`.
- **Boundary:** the recorded runtime case covers create; add/remove conclusions are source comparisons, not three independently executed backend failures.
- **Status / handling (2026-10-23):** Nonempty member-list contract enforced locally.
  - **Implementation:** all three member array schemas declare `minItems:1`, retaining the existing create/add maximum and no new remove maximum.
  - **Verification:** three regression cases first reached HTTP with empty arrays, then passed by rejecting them before HTTP; legitimate nonempty arrays still reach the local receiver. All three CLI dry-runs exercise both directions.

### AUD3-013 — Empty group updates pass local request construction

- **Classification:** request-constraint coverage gap.
- **Observation:** the skill says at least one of name/notice must be supplied; the operation schema does not express that rule in a way enforced by this path.
- **Runtime:** `group update audit-group --data '{}' --dry-run` exits 0 with no effective update body. The inspected backend rejects a request with neither update value.
- **Impact:** an operation that cannot perform the stated update can pass the local gate and fail remotely.
- **Sources:** `skills/octo-messaging/SKILL.md` group-update guidance; `internal/registry/specs/group.json:210–235`; `octo-server:modules/bot_api/groups.go:570–578`.
- **Boundary:** merely adding schema keywords would not establish enforcement; the existing strict-object-validation opt-in is relevant to any later handling decision.
- **Status / handling (2026-10-23):** Effective-update contract enforced locally.
  - **Implementation:** only `group.update` opts into strict composition with either `name` or `notice` required. Unknown properties remain permitted alongside a recognized update.
  - **Verification:** five invalid bodies first reached HTTP and now fail locally. Empty-string clears, both fields, a recognized field with unknown input and a null field beside a usable update remain valid. The shared validator is unchanged.

### AUD3-014 — Search descriptions omit backend defaults and input limits

- **Classification:** schema/help coverage gap.
- **Observation:** message/all/files/media search help describes page size without the backend default/range; keyword length is not constrained in the schema. The conditional channel-type requirement is separately recorded as AUD3-005.
- **Backend contract:** page size omitted/zero defaults to 20; nonzero values outside 1–100 are refused. Nonempty keywords are limited to 64 Unicode code points. The inspected default ordering is `time_desc`; supported sort values are already declared.
- **Impact:** callers cannot reliably select window sizes or predict validation failures from the CLI contract alone.
- **Sources:** `internal/registry/specs/message.json` search request bodies; `octo-server:modules/messages_search/validate.go:9–17,114–177`; rendered search help.
- **Boundary:** the absence of a displayed Cobra default is not proof that a zero is sent or that the backend default is wrong.
- **Status / handling (2026-10-23):** Search constraints and defaults documented.
  - **Implementation:** search schemas/help describe page size, 64-code-point keywords where supported and backend default ordering `time_desc`.
  - **Verification:** actual help and schema introspection expose those contracts. Search is not newly opted into strict validation, and no client-injected numeric or sort default is claimed.

### AUD3-015 — Thread-list pagination changes both defaults and response shape

- **Classification:** documentation/schema coverage gap.
- **Observation:** the guide exposes page-number controls but does not distinguish the unpaged handler response from the explicitly paged response.
- **Backend contract:** with neither pagination input, the bot handler requests page 1 with 100 entries and returns a bare array. With pagination inputs it returns `{count,list}`. Service-level page index is clamped to at least 1; page size at or below zero defaults to 15 and sizes above 100 are capped.
- **Impact:** consumers can parse the wrong shape or assume one unchanged default when adding a pagination argument.
- **Sources:** `internal/registry/specs/thread.json:38–52`; `skills/octo-messaging/SKILL.md` thread section; `octo-server:modules/bot_api/threads.go:149–166`; `octo-server:modules/thread/service.go:721–746`; `octo-server:modules/thread/const.go:37–39`.
- **Boundary:** manual page-number support is already present; this is not a request for cursor-based `--page-all`.
- **Status / handling (2026-10-23):** Thread-list response contract aligned.
  - **Implementation:** operation help, parameter descriptions and the guide distinguish unpaged 1/100 array mode from paged `{count,list}` mode and its 15/100 size behavior; schema uses `oneOf`.
  - **Verification:** actual help and schema show both modes. Response semantics remain backend-source evidence; no live thread list was fetched.

### AUD3-016 — Group-name truncation is not described

- **Classification:** backend-behavior documentation gap.
- **Observation:** create/update schemas and the skill do not explain the group-name length behavior.
- **Backend contract:** the inspected service limits names to 50 Unicode code points by truncating them, rather than returning an overlength validation error.
- **Impact:** the stored name can differ from the caller's supplied name despite a successful operation.
- **Sources:** `internal/registry/specs/group.json` create/update name fields; `octo-server:modules/group/const.go:13–16`; `octo-server:modules/group/service.go:1337–1340,2221–2225`.
- **Boundary:** whether the CLI should describe truncation or adopt a different local contract is not selected by this finding.
- **Status / handling (2026-10-23):** Backend truncation documented.
  - **Implementation:** create/update name descriptions and the guide state the 50-Unicode-code-point truncation behavior.
  - **Verification:** actual command help and embedded guidance expose it. No local `maxLength` refusal or truncation was introduced; stored-name behavior remains source evidence.

### AUD3-017 — Markdown update limits and blank-thread-content behavior are underdescribed

- **Classification:** request-constraint/documentation gap.
- **Observation:** group/thread markdown schemas require string `content` but do not describe the runtime byte limit. The thread handler additionally rejects whitespace-only content.
- **Backend contract:** both handlers check byte length against `TS_GROUPMDMAXSIZE`; the inspected default is 10,240 bytes, but configuration can change it. The thread blank-content refusal is a distinct condition.
- **Impact:** valid-looking text can be refused for a byte limit that cannot be inferred from character count, or for blank content in the thread path.
- **Sources:** `internal/registry/specs/group.json:133–153`; `internal/registry/specs/thread.json:128–148`; `octo-server:modules/bot_api/groups.go:255–265`; `octo-server:modules/bot_api/threads.go:326–346`; `octo-server:modules/group/service.go:1078–1087`.
- **Boundary:** a configurable byte ceiling is not a universal fixed Unicode `maxLength`. The finding does not select local enforcement.
- **Status / handling (2026-10-23):** Markdown constraints documented.
  - **Implementation:** group/thread content help and the guide describe the configurable byte ceiling and the thread-only blank-content refusal.
  - **Verification:** actual help and embedded guidance expose those distinctions. The configured ceiling is not encoded as a fixed Unicode length limit or newly enforced locally.

## File and bot-housekeeping findings

### AUD3-018 — Upload recovery conflates file policy limits with other payload-size errors

- **Classification:** confirmed error/recovery-guidance mismatch plus an external policy dependency.
- **Observation:** the error table maps upload `PAYLOAD_TOO_LARGE` to presigned/credentials upload. Elsewhere the guide says direct upload does not bypass file policy.
- **Backend contract:** multipart file-policy excess emits `err.server.bot_api.file_too_large` with HTTP 400. The separate `err.server.bot_api.payload_too_large` / HTTP 413 has other uses, including the voice-transcription proxy. Presigned issuance checks the same `file.MaxUploadSize()` policy against `fileSize` and can reject the same overlarge file.
- **Credentials boundary:** the STS credentials handler does not take a size input or perform that size check; it validates filename/extension and signs access to one object key. Equivalent external COS enforcement was not established.
- **Impact:** a caller can repeat a known-disallowed upload through presigned issuance, misclassify a policy error as a gateway/request-body limit, or rely on an unverified credentials-policy exception.
- **Sources:** `skills/octo-files/SKILL.md:31,115–123`; `octo-server:modules/bot_api/file.go:54–85,174–285`; `octo-server:modules/bot_api/api_i18n.go:73–85`; `octo-server:pkg/errcode/bot_api.go:51–65`.
- **Boundary:** the shared generic HTTP-413 taxonomy is not itself proof of the file handler's emitted code. No real overlimit upload or COS policy probe was performed.
- **Status / handling (2026-10-23):** Recovery guidance aligned; external COS policy remains unverified.
  - **Implementation:** the files guide distinguishes HTTP-400 file policy from HTTP-413 payload size and explains that presigned issuance shares the file-size cap.
  - **Verification:** inspected error/handler source supports the guide, and the embedded guide matches it. No real overlimit upload or external COS-policy test was performed.

### AUD3-019 — Registration metadata guidance does not distinguish User Bot from App Bot

- **Classification:** confirmed applicability-description gap.
- **Observation:** `agent-platform`, `agent-version` and `plugin-version` are described as registration metadata without a token-kind qualification.
- **Backend contract:** User Bot registration parses/persists the metadata; the App Bot branch explicitly treats the agent metadata as unsupported and does not persist it. The same identity boundary applies to the `agent_hosting` capability in AUD3-007.
- **Impact:** an App Bot caller can reasonably expect a metadata update that the handler ignores.
- **Sources:** `skills/octo-files/SKILL.md:80–86`; `octo-server:modules/bot_api/register.go:326–468`.
- **Boundary:** successful parsing/registration does not prove that metadata was stored. This is not a claim that App Bot registration itself is unsupported.
- **Status / handling (2026-10-23):** Metadata applicability documented.
  - **Implementation:** registration help and the files guide distinguish User Bot persistence from App Bot metadata ignore behavior.
  - **Verification:** actual help and embedded guidance match the inspected registration branches. No live metadata persistence was tested.

### AUD3-020 — Exactly-once registration guidance is not identified as a workflow policy

- **Classification:** workflow-policy clarification dependency.
- **Observation:** the guide says to register exactly once per bot lifecycle/publish and cache owner UID rather than registering again.
- **Backend contract:** registration is repeatable, returns current connection information and refreshes User Bot IM token/cache. Source also describes registration as part of recovery after disconnection.
- **Impact:** callers may treat an owner-UID caching recommendation as a backend prohibition on valid runtime registration/reconnection.
- **Sources:** `skills/octo-files/SKILL.md:80–99`; `octo-server:modules/bot_api/register.go:326–425`.
- **Decision boundary:** clarify whether the restriction applies to this CLI housekeeping recipe only or is an intended runtime policy. No reconnect/re-registration behavior is selected here.
- **Status / handling (2026-10-23):** Housekeeping policy clarified.
  - **Implementation:** the files guide recommends caching owner UID for CLI housekeeping while permitting runtime registration for current connection information and recovery.
  - **Verification:** embedded guidance matches source and the inspected repeatable registration flow; no runtime reconnect implementation or live recovery test was added.

## Docs findings

### AUD3-021 — Search help and skill omit the supported `html_ppt` type

- **Classification:** confirmed discovery-description mismatch.
- **Observation:** prose lists `doc, sheet, board, html`, while the schema enum also includes `html_ppt`.
- **Runtime:** `docs search --keyword audit --doc-type html_ppt --dry-run` exits 0 and constructs `{"docType":["html_ppt"],"q":"audit"}`.
- **Backend contract:** the document-type model and search/index paths include `html_ppt`.
- **Impact:** callers can incorrectly conclude PPT documents cannot be selected in search.
- **Sources:** `skills/octo-docs/SKILL.md:96–98`; `internal/registry/specs/docs.json:451–493`; `octo-docs-backend:src/db/docType.ts:1–37`; `octo-docs-backend:src/api/routes/docs.ts:547–550`; `octo-docs-backend:src/search/osClient.ts:198–225`; `octo-docs-backend:src/search/docIndexQueue.ts:52–63`.
- **Status / handling (2026-10-23):** Supported search type made discoverable.
  - **Implementation:** Docs search help, field descriptions and the guide include `html_ppt`; the existing enum is unchanged.
  - **Verification:** fresh dry-run constructs `docType:["html_ppt"]`; actual help/schema and the embedded guide expose the type.

### AUD3-022 — Member-list permission is documented as admin-only

- **Classification:** confirmed permission-description mismatch.
- **Observation:** help says `List a document's members (needs admin)`; common guidance also requires admin.
- **Backend contract:** GET members uses `requireDocRole(..., 'reader')`. The source comment explicitly describes the relaxation from admin to reader. Administrative writes are different operations.
- **Impact:** a legitimate reader workflow can be skipped or misdiagnosed as requiring an unnecessary permission upgrade.
- **Sources:** `skills/octo-docs/common.md:135–136`; `internal/registry/specs/docs.json:1771–1775`; `octo-docs-backend:src/api/routes/members.ts:30–88`.
- **Boundary:** source authorization was inspected; a real reader credential was not exercised.
- **Status / handling (2026-10-23):** Member-read permission descriptions aligned.
  - **Implementation:** only member-list summary and the common guide say reader or higher; member-write authorization is unchanged.
  - **Verification:** actual help and embedded guidance match the inspected reader gate. No live reader-identity permission test was performed.

### AUD3-023 — Member response schema excludes legitimate null `grantedBy`

- **Classification:** confirmed response-schema mismatch.
- **Observation:** the schema declares `grantedBy` as a string.
- **Backend contract:** the member-list handler redacts it to null for non-admin callers, including the synthesized owner entry.
- **Impact:** consumers built from schema inspection can reject a legitimate response or assume grant attribution is always available.
- **Sources:** `internal/registry/specs/docs.json:1786–1824`; `octo-docs-backend:src/api/routes/members.ts:69–84`.
- **Boundary:** related to AUD3-022, but correcting only the permission prose would leave the response contract inaccurate. No live response was fetched.
- **Status / handling (2026-10-23):** Nullable grant attribution represented.
  - **Implementation:** the member response schema uses string/null `oneOf`, preserving both alternatives in registry introspection without changing the loader.
  - **Verification:** fresh `schema docs.members.list` exposes both alternatives. Null response behavior remains handler-source evidence, not a live response observation.

### AUD3-024 — Share-role vocabulary does not match the backend's document-type policy

- **Classification:** demonstrated contract difference with a product-policy decision boundary.
- **Observation:** CLI/skill expose `read|edit`. The backend also accepts `comment`; for `doc_type: html`, the allowed nonrestricted roles are only `read|comment`, not `edit`. Other document types allow `read|comment|edit`.
- **Runtime:** `docs share set d_audit --scope anyone_in_space --role comment --permissionEpoch 0 --dry-run` exits 2 with `ENUM_NOT_ALLOWED`, message `--role: "comment" is not an accepted value`, and hint `pass one of: read, edit`.
- **Impact:** comment-only sharing cannot pass the local enum gate, while the advertised edit choice is not valid for an HTML document. This is separate from forbidden authenticated/global-sharing controls.
- **Sources:** `skills/octo-docs/common.md:144–166`; `internal/registry/specs/docs.json:1923–2027`; `octo-docs-backend:src/api/routes/docs.ts:986–1017`; `octo-docs-backend:src/permission/shareScope.ts:35–78`.
- **Decision boundary:** backend support alone does not authorize expanding the CLI's permission vocabulary. The intended Bot API surface and document-type guidance need an explicit disposition. Restricted sharing normalizes its role separately.
- **Status / handling (2026-10-23):** Retained by user decision. Keep the existing `read|edit` vocabulary and the current sharing operation unchanged; the observed backend difference is not claimed as repaired.

### AUD3-025 — Attachment presign lacks the backend positive-size requirement

- **Classification:** request-constraint coverage gap.
- **Observation:** `sizeBytes` is a required integer, but schema/help does not state a positive minimum; help only describes the tier cap.
- **Runtime:** `docs attachments presign d_audit --data '{"fileName":"audit.png","mime":"image/png","sizeBytes":0}' --dry-run` exits 0 and retains zero.
- **Backend contract:** `sizeBytes <= 0` is refused with HTTP 400.
- **Impact:** callers can pass local validation for an attachment size the backend will never accept.
- **Sources:** `internal/registry/specs/docs.json:2854–2917`; `skills/octo-docs/common.md:191–197`; `octo-docs-backend:src/api/routes/attachments.ts:230–269`.
- **Boundary:** the existing positive-size example is not broken; the schema/description fails to express the full requirement. Enforcement placement is not selected.
- **Status / handling (2026-10-23):** Positive integer size enforced locally.
  - **Implementation:** only attachment presign opts into strict validation and declares `minimum:1`; help and the common guide describe the positive integer and MIME-tier cap.
  - **Verification:** five nonpositive flag/body/merge cases first reached HTTP and now fail before it; four positive/precedence cases still reach the receiver. Fresh CLI dry-runs reject zero/negative sizes and accept 1/1024. Integer type is the existing CLI contract; this does not claim the backend rejects every positive fractional number.

## HTML findings

### AUD3-026 — Grant role is advertised as optional with a reader default

- **Classification:** confirmed default/requiredness mismatch.
- **Observation:** help/spec say role defaults to reader, and only `uid` is required.
- **Runtime:** `html grant add d_audit --data '{"uid":"audit-user"}' --dry-run` exits 0 and emits a body containing only `uid`; the CLI does not insert reader.
- **Backend contract:** `handleAddGrant` accepts only explicit `reader|commenter|writer`; an empty/missing role returns `invalid_grant` with `role must be reader|commenter|writer`.
- **Impact:** an invocation permitted by the advertised default produces a request the inspected handler refuses.
- **Sources:** `internal/registry/specs/html.json:633–669`; `octo-docs-html:internal/transport/httpx/handlers_grants.go:60–71`.
- **Prior boundary:** step-1 VERIFY-002 found an explicit reader example valid. It did not establish an omitted-role default; that valid example remains valid.
- **Status / handling (2026-10-23):** Retained by user decision to leave all HTML surfaces unchanged. The role-requiredness discrepancy remains recorded, not repaired.

### AUD3-027 — Grant description overstates reader comment/reaction permission

- **Classification:** confirmed permission-description mismatch.
- **Observation:** grant help describes reader as read published versions plus comment/react.
- **Backend contract:** reader maps to `CapRead`; comment/reply/react require `CapComment`, provided by commenter or higher. The HTML skill's commenter requirement is already aligned.
- **Impact:** an agent can grant insufficient permission for its intended comment workflow or misinterpret the resulting refusal.
- **Sources:** `internal/registry/specs/html.json:635`; `skills/octo-html/SKILL.md` comment-permission guidance; `octo-docs-html:internal/service/capability.go:19–64`; `octo-docs-html:internal/transport/httpx/handlers_comments.go:242–249`.
- **Prior boundary:** step-2 HTML-001 addressed comment-operation guidance; the stale grant-operation description is a separate surface.
- **Status / handling (2026-10-23):** Retained by user decision to leave all HTML surfaces unchanged. The reader-permission description remains recorded, not repaired.

### AUD3-028 — HTML version-list response schema has the wrong outer shape and timestamp field

- **Classification:** confirmed response-schema mismatch.
- **Observation:** the schema describes an array with `n` and `created`.
- **Backend contract:** the unwrapped payload is `{slug,title,versions:[...]}`; version timestamps use optional `created_at`. The spec's root `x-octo-response-unwrap: data` does not extract `versions` from that payload.
- **Impact:** schema-driven consumers can apply the wrong iteration path or expect a nonexistent timestamp name.
- **Sources:** `internal/registry/specs/html.json:11,392–430`; `octo-docs-html:internal/transport/httpx/handlers_docs.go:533–545`; `octo-docs-html:internal/transport/httpx/dto.go:78–104`.
- **Verification:** the runtime dry-run confirmed the GET URL, not the response. Shape evidence is the backend DTO/handler.
- **Status / handling (2026-10-23):** Retained by user decision to leave all HTML surfaces unchanged. The version response-schema difference remains recorded, not repaired.

### AUD3-029 — HTML grant-list response schema omits its object wrapper

- **Classification:** confirmed response-schema mismatch.
- **Observation:** the schema describes a `{uid,role}` array.
- **Backend contract:** the unwrapped payload is `{items:[{uid,role,source,grantedBy}]}`. Root response unwrapping does not extract `items`.
- **Impact:** schema-derived parsing can target an array where the CLI receives an object; source/grant-attribution fields are also undiscoverable from that declaration.
- **Sources:** `internal/registry/specs/html.json:592–630`; `octo-docs-html:internal/transport/httpx/handlers_grants.go:19–49`.
- **Verification:** GET request construction was exercised; actual response structure was source-reviewed, not fetched live.
- **Status / handling (2026-10-23):** Retained by user decision to leave all HTML surfaces unchanged. The grant-list response-schema difference remains recorded, not repaired.

### AUD3-030 — HTML asset-list response schema disagrees with the DTO

- **Classification:** confirmed response-schema mismatch.
- **Observation:** the schema describes an array with `sha256`, `name`, `size` and `mime`.
- **Backend contract:** the unwrapped payload is `{slug,assets:[...]}`. Asset entries include `original_name`, `created` and `url`; the name field is not `name`. Asset-add responses also contain additional slug/mime/size information beyond their narrow schema.
- **Impact:** consumers can choose the wrong array path or field name; complete metadata is not discoverable through schema inspection.
- **Sources:** `internal/registry/specs/html.json:733–775,813–829`; `octo-docs-html:internal/transport/httpx/handlers_assets.go:57–76`.
- **Verification:** GET request construction was exercised; response comparison is source-only.
- **Status / handling (2026-10-23):** Retained by user decision to leave all HTML surfaces unchanged. The asset response-schema differences remain recorded, not repaired.

### AUD3-031 — Grant-management help equates manage permission with creator-only access

- **Classification:** confirmed permission-summary mismatch.
- **Observation:** grant help uses author-only/creator-only wording.
- **Backend contract:** grant list/add/remove routes use `requireDocManage`; capability resolution includes qualifying document admins, not only the creator. Grant operations retain their verified active-Space requirements.
- **Impact:** help can wrongly exclude a legitimate administrator or encourage unnecessary creator-credential use.
- **Sources:** `internal/registry/specs/html.json` grant operation summaries/descriptions; `octo-docs-html:internal/transport/httpx/server.go:125–130`; `octo-docs-html:internal/service/capability.go:48–64`.
- **Boundary:** this does not justify weakening management authorization or changing grant tenancy. No live admin/creator comparison was performed.
- **Status / handling (2026-10-23):** Retained by user decision to leave all HTML surfaces unchanged. The grant-management permission summary remains recorded, not repaired.

### AUD3-032 — Asset write help says author-only while routes accept writer-plus capability

- **Classification:** confirmed permission-summary mismatch.
- **Observation:** `html asset add` and `html asset rm` summaries say author-only.
- **Backend contract:** upload/delete routes use `requireDocEdit`; their route comment identifies writer-plus access. Read/list has a separate reader gate.
- **Impact:** a supported editing workflow can be mistaken for a creator-only operation.
- **Sources:** `internal/registry/specs/html.json:777–838`; `octo-docs-html:internal/transport/httpx/server.go:132–137`.
- **Boundary:** only source authorization was reviewed; no asset was uploaded or deleted to test permission differences.
- **Status / handling (2026-10-23):** Retained by user decision to leave all HTML surfaces unchanged. The asset-write permission summary remains recorded, not repaired.

### AUD3-033 — Legacy deletion needs explicit reconciliation with the Bot prohibition

- **Classification:** static security/policy verification dependency, not a demonstrated live bypass.
- **Documented contract:** skill/help say Bots cannot delete HTML documents; `html rm` itself has no local Bot-token refusal.
- **Inspected paths:** canonical deletion delegates to docs-backend. A legacy path reaches `RemoveAuthorizedWithPublisherToken`; after the HTTP management gate, non-user-published legacy data can reach blob deletion without a separate Bot-specific prohibition in that service method. User-published/registered documents have additional provenance/registration checks.
- **Impact:** a broad prohibition in agent guidance may not be enforced uniformly across historical storage paths. Incorrect handling here could either permit destructive access or wrongly remove a legitimate user workflow.
- **Sources:** `skills/octo-html/SKILL.md:77–87`; `internal/registry/specs/html.json:359–385`; `octo-docs-html:internal/service/doc.go:1980–2058`; related HTTP management/provenance gates.
- **Boundary:** no DELETE, live credential probe or deployment-gateway check was performed. Reachability for a specific Bot and document remains unproven. The finding does not authorize relaxing the prohibition or implementing a new deletion policy.
- **Status / handling (2026-10-23):** Retained by user decision to leave all HTML surfaces unchanged. No deletion policy or implementation was changed. The external policy/deployment uncertainty remains unverified; no destructive probe is authorized or claimed.

## Loop and Marketplace guidance findings

### AUD3-034 — Task-filter and ordering semantics are incomplete in the Loop skill

- **Classification:** documentation enhancement; current flags/help/backend agree.
- **Observation:** the skill lists `open-only`, `scheduled` and sort/direction controls without all their boundary semantics.
- **Backend/help contract:** `open_only` excludes `done` and `cancelled`; `scheduled` requires at least one start/due date; `direction` is ignored when sorting by `position`. List-only switches are correctly absent from group help.
- **Impact:** agents can choose valid filters that do not mean what they intended, despite correct registration and request serialization.
- **Sources:** `skills/octo-loop/SKILL.md:91–120`; `internal/registry/specs/loop.json:6401–6436`; `octo-fleet:internal/handler/issue.go:861–885`; `octo-fleet:internal/handler/issue_filters.go:11–85`.
- **Boundary:** supported task unions, identity/date/attachment fields and CSV encodings matched the inspected CLI/backend. This does not reopen step-2 LOOP-004/005's added fields.
- **Status / handling (2026-10-23):** Filter and ordering guidance aligned.
  - **Implementation:** the Loop guide explains open-only exclusions, scheduled date presence and fixed ascending position order.
  - **Verification:** embedded guidance matches source and existing actual help; query-handler behavior remains source-reviewed, not a live task-list observation.

### AUD3-035 — Marketplace install guidance omits dependency-visibility refusal

- **Classification:** workflow-documentation gap.
- **Observation:** expert/team installation guidance does not state that invisible declared dependencies can make the existing install operation fail.
- **Backend contract:** the legacy install service compares declared relationships with those visible to the caller and refuses an incomplete graph. This is a permission prerequisite, not simply a malformed plugin ID.
- **Impact:** an agent can repeatedly attempt an install or misdiagnose a dependency-visibility refusal. A visible root plugin alone does not prove its entire install graph is accessible.
- **Sources:** `skills/octo-marketplace/expert.md:150–165`; `octo-marketplace:internal/service/plugin/install.go:60–75,134–155`.
- **Boundary:** the existing `POST /market/api/v1/plugins/install` route remains supported. No live catalog/permission/install operation was exercised. The existing one-level relation read is not equivalent to the transitive graph candidate in AUD3-037.
- **Status / handling (2026-10-23):** Install prerequisite guidance aligned.
  - **Implementation:** the expert/team guide explains dependency visibility and the limited direct-relation view.
  - **Verification:** embedded guidance matches the inspected installation and relation-read source. The supported legacy install route is unchanged; no live install or transitive graph operation was performed.

## Additive capability candidates

These entries describe inspected backend capabilities outside the current declared CLI surface. Their existence does not establish a requirement to implement them. Naming, identity support and user-facing commitments are intentionally unselected.

### AUD3-036 — Fleet capability installation has no declared CLI operation

- **Classification:** additive capability candidate.
- **Backend capability:** public operation `capability.install` installs a capability definition with bindings. It requires `X-Workspace-ID`, `Idempotency-Key` and an authorized action scope for Loop credentials.
- **CLI gap:** the operation is absent from the embedded Loop spec/skill. Generic `api` has a workspace selector but no arbitrary header input for the mandatory idempotency key, so it is not an equivalent ready-made path.
- **Impact:** the backend exposes a public user workflow that cannot currently be discovered or fully expressed through the declared CLI surface.
- **Sources:** `octo-fleet:openapi/public-v1.yaml:57–83` and its capability-install schemas; `internal/registry/specs/loop.json`; `cmd/api.go:100–127`.
- **Boundary:** this is distinct from intentionally hidden daemon/device/runtime/claim operations. Adding it would require a scope/authorization/idempotency decision, not just un-hiding daemon APIs.
- **Status / handling (2026-10-23):** Out of scope by user decision. Do not add capability installation; retain the existing Loop command surface.

### AUD3-037 — Marketplace transitive relation-graph read is absent from CLI discovery

- **Classification:** additive capability candidate.
- **Backend capability:** `plugin.graph.get`, GET `/plugins/detail_graph` with query `plugin_id`, returns the root projection and a flat, deduplicated transitive relation closure. Each related node/edge is filtered by visibility; `related_plugins` is a lookup table, not a nested tree.
- **CLI gap:** the operation is absent from the embedded Marketplace spec. Existing `plugin get --include-relations` provides a narrower relation view.
- **Impact:** full dependency-graph discovery is not represented as an operation/skill workflow; callers must not infer transitive completeness from the existing option.
- **Sources:** `octo-marketplace:internal/api/handler/plugin/handler.go:408–445`; `octo-marketplace:docs/openapi/swagger.yaml`, operation `plugin.graph.get`; `internal/registry/specs/marketplace.json`.
- **Boundary:** this is not a demonstrated defect in the existing one-level command. No live graph was fetched.
- **Status / handling (2026-10-23):** Out of scope by user decision. Do not add the graph operation; retain the current one-level relation command.

### AUD3-038 — Marketplace has an additive, user-only installation contract

- **Classification:** additive capability/migration candidate with identity and idempotency boundaries.
- **Backend capability:** `plugin.installation.create`, POST `/market/api/v1/plugins/{plugin_id}/installations`, requires end-user identity, `X-Workspace-ID` and `Idempotency-Key`. It rejects Bot identity. Inputs include `resource_name`, `runtime_id` and optional `custom_env`; documented header multiplicity and Space-selector restrictions apply.
- **Current CLI contract:** `plugin install` still targets the supported legacy `/plugins/install` route with body `plugin_id`, `workspace_id`, `runtime_id` and optional `custom_env`. The new route is absent from the spec; generic API cannot supply the required arbitrary idempotency header.
- **Impact:** a new installation capability is not available through the existing CLI shape. Redirecting the current Bot workflow would change identity support and request semantics rather than merely updating a path.
- **Sources:** `internal/registry/specs/marketplace.json:844–899`; `octo-marketplace:docs/openapi/swagger.yaml:6466–6510`; `octo-marketplace:internal/api/handler/plugin/installations.go:58–91`; `octo-marketplace:docs/api/plugin-installations.md:3–5,42–68`.
- **Boundary:** backend documentation calls this route additive and explicitly leaves legacy client migration separate. No migration, new credential flow or installation API behavior is approved by this entry.
- **Status / handling (2026-10-23):** Out of scope by user decision. Do not add or migrate to the user-only installation contract; retain the existing legacy installation route and identity support.

## Persisted runtime observations

The following concise results preserve the material evidence even after the temporary JSON exports disappear. Command fragments below were arguments to `/tmp/octo-cli-current-audit` in the isolated audit environment.

| Probe | Observed result |
| --- | --- |
| `message send --data` with `channel_id`, `channel_type:1`, and a type-8 `payload` | Exit 0; attachment payload retained in dry-run |
| `loop task create` with member Octo UID, attachment IDs, due date and status | Exit 0; fields retained and `X-Workspace-ID`/Loop Accept header described |
| `schema drive.upload.file`, `schema drive.download.file`, `schema drive.share.create` | Exit 2 each; unknown operation |
| `thread create ... --on-behalf-of audit-user --dry-run` | Exit 2; unrecognized flag |
| `bot register --agent-hosting audit --dry-run` | Exit 2; unrecognized flag |
| `bot space-members --page 2 --dry-run` | Exit 2; unrecognized flag |
| `message search around` baseline plus `--page-size 10 --dry-run` | Exit 2; unrecognized flag; baseline with explicit channel type/anchor exits 0 |
| `message search groups --keyword audit --sequence 1 --dry-run` | Exit 2; unrecognized flag |
| The four corresponding body fields supplied through `--data` | Exit 0 each; exact fields retained; this does not apply to the Space-member GET query |
| `docs search --keyword audit --doc-type html_ppt --dry-run` | Exit 0; `docType:["html_ppt"]` retained |
| `docs share set ... --role comment --permissionEpoch 0 --dry-run` | Exit 2; `ENUM_NOT_ALLOWED`, allowed values `read, edit` |
| `docs attachments presign ...` with `sizeBytes:0` | Exit 0; zero retained |
| `html grant add ...` with only `uid` | Exit 0; role absent from body |
| `html versions`, `html grant list`, `html asset ls` with `--dry-run` | Exit 0 each; expected GET URL described, no response-shape proof |
| `drive search --filters '{}' --dry-run` | Exit 2; unrecognized flag |
| Drive search with `--q` and scalar `filters.type` in `--data` | Exit 0; scalar retained |
| Drive search with `--q` and array `filters.types` in `--data` | Exit 0; array retained |
| Five single-channel skill search shapes without channel type | Exit 0 each; no `channel_type` injected |
| Single-channel search with explicit `--channel-type 2` | Exit 0; value retained |
| `group create --data '{"members":[]}' --dry-run` | Exit 0; empty member array retained |
| `group update audit-group --data '{}' --dry-run` | Exit 0; no effective update body |

Temporary evidence exports from the audit session were:

- `/tmp/octo-cli-current-audit-summary.json`: executable identity, scope and coverage totals.
- `/tmp/octo-cli-current-commands.json` and `/tmp/octo-cli-current-help.json`: registered tree/flags and actual help output.
- `/tmp/octo-cli-current-skills.json`: embedded-document comparison and parsed command snippets.
- `/tmp/octo-cli-current-schemas.json`: inspected operation details.
- `/tmp/octo-cli-current-smokes.json`: all 34 targeted cases with arguments, exit code, stdout and stderr.
- `/tmp/octo-cli-current-help-references.json`: prose option references, including intentional references to other commands.
- `/tmp/octo-cli-current-skill-schema-refs.json`: concrete schema-query checks.
- `/tmp/octo-cli-current-findings.json`: grouped findings used to prepare this register; its 21 groups are decomposed here into 38 independently trackable entries.

These paths are evidence provenance, not permanent repository dependencies. For later follow-up, recheck the relevant current contract, retain its stable ID and record changed evidence in this document. The accepted implementation scope does not assert production deployment readiness.

## Implementation acceptance — 2026-10-23

### Source-built executable

```sh
go build -o /tmp/octo-cli-step3-after ./cmd/octo-cli
```

SHA-256: `0a4481a8f93cc14963cabf816c8c340e187bae430db236240170fbc0bd49e861`.

This binary was built in the checkout containing this document. The source tree includes uncommitted work, so the binary hash is an executable identifier, not a claim of clean-commit provenance. No installed binary was replaced.

### Observed checks

| Check | Result |
| --- | --- |
| `go test ./... -count=1` | All 12 packages passed |
| `go vet ./...` | Passed |
| In-scope command/group help | 412 invocations; no failures or formal Flags mismatches |
| Registered flag parsing | 5,022 instances accepted by the executable |
| Schema lookup | 293 in-scope operations; no failures |
| Nonpositional schema/flag mappings | 718 checked; no mismatches |
| Embedded skills/source bytes | Eight skills, 16 Markdown documents; identical bytes |
| Skill command snippets, including multiline JSON | 441 parsed; no unknown options or unparsed fragments |
| Concrete skill schema references | 47 lookups; no failures |
| Targeted CLI smoke | 62 cases; expected exit codes and request/schema assertions passed |
| Retained HTML surfaces | Spec and skill SHA-256 values unchanged from implementation baseline |
| Retained Docs sharing operation | Parsed operation unchanged from implementation baseline |
| Excluded capability candidates | All three remain unknown operation IDs |

The schema/flag check honors explicit positional parameters and the existing positional `file_id` of `drive.share.blob-create`; handwritten composite workflows are not represented as invented operation IDs.

### Regression evidence and review corrections

Before implementation, 13 negative subcases failed for the predicted reason: invalid input returned no validation error and reached the local HTTP receiver. These were three empty member-array operations, five group updates without a recognized non-null value, and five nonpositive attachment-size flag/body/merge cases. Their legitimate counterparts passed before the fix and remain passing afterward.

`cmd/service/step3_messaging_test.go` and `cmd/service/step3_docs_test.go` retain the consumer-visible request-boundary coverage. The obsolete `TestDocsStrictSchemaAppliesOnlyToExplicitOperations` metadata-list assertion was removed; it pinned implementation configuration rather than request behavior. The existing enum contract table was extended using the remote Drive validator, including its accepted empty ownership selector.

During the initial implementation, Main review completed group-name, Markdown, sort-default and thread-list help descriptions without changing the shared request validator or search routing. The subsequent nullable-filter review fix below updates the shared schema projection and null gates.

### Evidence retention and limits

Temporary acceptance exports:

- `/tmp/octo-cli-step3-before-evidence.json`: pre-change missing-flag failures.
- `/tmp/octo-cli-step3-acceptance-summary.json`: fresh coverage totals and binary hash.
- `/tmp/octo-cli-step3-acceptance-help.json`: actual help output.
- `/tmp/octo-cli-step3-acceptance-schemas.json`: actual registry introspection.
- `/tmp/octo-cli-step3-acceptance-snippets.json`: parsed current skill commands.
- `/tmp/octo-cli-step3-acceptance-smokes.json`: 62 targeted cases with arguments, stdout, stderr and exit status.

The concise results above remain useful when temporary files disappear. All business-operation probes used synthetic credentials, isolated configuration, a loopback API origin and `--dry-run`. Regression tests used isolated local HTTP receivers. No deployed-service authorization, event ordering, storage policy, actual response DTO, install, upload or deletion behavior was exercised. Those backend semantics are source evidence only; HTML deletion and external COS policy remain explicitly unverified within the retained scope.

### Review-fix acceptance — 2026-10-23

Both P2 review findings are locally resolved:

| Finding | Selected handling | Observed verification |
| --- | --- | --- |
| Optional Drive filters rejected legitimate null values | OpenAPI 3.1 nullable types, explicit null enum membership and schema-aware uint64 null handling | The real-command null regression failed before implementation and passes afterward; legal non-null boundaries remain lossless and invalid non-null values fail before HTTP |
| Grouped-search sequence schema used the response root | Declare the field inside the backend data object; retain runtime wrapping | Before the change, schema-driven extraction could not locate the declared root field; the fresh CLI schema now locates value 42 in a controlled HTTP response at `.data.data.sequence` |

The nullable-filter regression is in `cmd/service/drive_test.go`; the global constrained-null census continues to exercise non-nullable IDs and now checks explicit nullable contracts in both directions. No special-case filter-name logic or blanket null exemption was added.

Fresh source build:

```sh
go build -o /tmp/octo-cli-step3-review-fixed ./cmd/octo-cli
```

SHA-256: `c4ae631ab5e138afb6dd47a72cedc88a8310160bf2fbc828fc1d48a14629fec3`.

- `go test ./... -count=1`: all 12 packages passed.
- `go vet ./...`: passed.
- Eleven direct CLI smoke cases passed: individual/combined null filters, exact large uint64 boundaries, legitimate ownership, invalid ownership, negative/overflow/fractional sizes and non-nullable `parent_id:null` refusal.
- Invalid ownership diagnostics render the allowed empty selector and JSON `null` vocabulary; non-nullable enum and uint64 gates remain unchanged.
- The response schema and local HTTP execution agree on the sequence field's nesting.
- All 412 recorded in-scope command/help paths and 293 current in-scope schema lookups succeeded against the fresh executable.

Runtime evidence is retained in `/tmp/octo-cli-step3-review-fix-smokes.json`, `/tmp/octo-cli-step3-review-fix-sequence.json` and `/tmp/octo-cli-step3-review-fix-discovery.json`. These are local checks with invented credentials and a loopback origin, not deployed-service acceptance. The previously retained HTML, Docs-role and additive-capability decisions remain unchanged.
