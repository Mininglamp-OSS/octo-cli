# PPT gallery release prerequisite

The CLI creation policy accepts only `blank`, `signal`, `terra`, `orbital`, and
`picnic`. Deploy the companion backend catalogue and frontend picker before
publishing this CLI. A merged MR, successful source build, or local preview is
not evidence that the release target supports the catalogue.

## Operator setup

Configure the GitHub environment **`ppt-gallery-release`** with required human
reviewers and deployment branches restricted to **`main`**. Store:

- Environment variable `PPT_RELEASE_API_ORIGIN`: the intended release target's
  HTTPS gateway origin only (no path prefix, including `/api` or `/octo`,
  credentials, query, or fragment). Path-prefixed deployments are unsupported.
- Environment variable `PPT_RELEASE_SPACE_ID`: a dedicated acceptance Space on
  that target; do not use a personal working Space.
- Environment secret `PPT_RELEASE_BOT_TOKEN`: a dedicated `app_*` or `bf_*` Bot
  authorized to create/read documents and manage sharing on its own probe documents
  in that Space. Do not use a human key,
  put this secret at repository scope, or reuse a broad production Bot.

Before approving the environment job, verify the target origin and confirm the
frontend picker and backend deployment against the [current rollout dependencies](#current-rollout-dependencies).
The API probe validates backend create/read and sharing epoch capability, **not** the frontend UI
or every tenant/replica. The environment reviewer is the target trust boundary:
confirm the origin belongs to the intended deployment before releasing the secret.
Record the companion deployment refs in access-controlled release evidence.
Recheck after a backend rollback; probe success is point-in-time evidence.

Dispatch publishing workflows **from `main`**, supplying the release tag as the
`tag` input. The preflight runs trusted tooling from the dispatch commit, never
scripts from an arbitrary input tag. The existing CI-evidence/tag checks remain
required; this gate does not replace them. Missing configuration blocks real
publication. This change does not configure secrets or release anything itself.

## Current rollout dependencies

The gallery has separate data, player, catalogue and UI prerequisites.
First merge and deploy all gallery data (Signal, Picnic, Terra and Orbital) and
Player prerequisites; then merge and deploy Catalogue activation with the
Frontend picker. Capacity is the separate 32 MiB document / 40 MiB full-edit
request prerequisite for large PPTs. This table specifies release conditions,
not current merge or deployment status.

| Component | Release condition |
| --- | --- |
| Terra | Merge and deploy before activation |
| Orbital | Merge and deploy before activation |
| Player | Merge and deploy before activation |
| Catalogue activation | Merge and deploy after prerequisites; all five choices must work |
| Capacity | Deploy migration, request/proxy and MySQL packet settings before large-deck acceptance |
| Frontend | Confirm the picker and rendered template effects in the actual deployed UI |

Keep implementation links, source refs and exact deployed backend/frontend SHAs
in access-controlled operational evidence, together with the target environment,
five-template create/read results and UI acceptance. Source refs alone are not
proof of deployment. Do not put internal repository or personal namespace details
in this public guide, its tests, or public PR descriptions.

Update this dependency table **and the expected rows in
`skills/ppt_templates_test.go`** together when release conditions change.
The guard rejects missing, duplicate, unknown or malformed component rows and
premature activation conditions; it does not pin internal submission vehicles.
Embedded skills and CHANGELOG link here instead of copying operational refs.
Retire the table and its guard together when this rollout gate is replaced.

The sharing contract must also be deployed: `docs share get` returns
`permissionEpoch`, and `docs share set` rejects stale epochs with HTTP 409.
The preflight tests this contract on the freshly created blank probe: read its
restricted settings and safe integer epoch, PUT `shareScope: restricted` with that
epoch, require advancement by one, then send the old epoch once and require HTTP
409 `share_settings_conflict` with matching current settings. A final GET must
confirm the new epoch and restricted/read settings. This deliberate negative
test is not a retry/recovery policy. No access is broadened, no existing user
document is selected, and a conflict is never automatically retried.

## What runs and what it writes

Both `release-publish.yml` (before GitHub Release publication) and
`npm-publish.yml` (including manual real uploads and `from_artifact` calls) depend
on `ppt-release-preflight.yml`. It calls `scripts/ppt-release-preflight.mjs` to
create **five fresh documents**, then read each PPT deck back through the Bot API.
It checks receipt template/title/Space/document identity and nonempty Bento decks,
then performs four sharing requests on the new blank document (GET/PUT/stale PUT/GET).
Unique probe UUIDs/idempotency keys prevent old completed receipts from proving
current capability. Any error stops the gate: no redirects, automatic retries,
template substitution, or credential fallback. Each of the 14 sequential requests
has a 30-second timeout and each response an 8 MiB limit; the request-time budget
is at most seven minutes within the ten-minute job timeout. Setup overhead may
still exhaust the job timeout; that fails closed, not as capability evidence.
Failures report only the request method plus an HTTP status or fixed check name;
URLs, credentials, response bodies and raw transport/parser messages are omitted.
Fetch-level redirects and timeouts are grouped under transport failures.

Probe documents have titles `CLI release probe <UUID> <templateId>` and their IDs
are logged immediately after a valid receipt. They are **retained**, not deleted
by the Bot (Bot deletion is not supported). A failed or timed-out POST may still
have created a document; use the logged UUID/title to inspect the dedicated Space
before operator cleanup. No user documents are rewritten or deleted.

A complete GitHub + npm run probes twice (up to ten documents), so a backend
rollback between the two publication stages also blocks npm. A rerun uses a new
UUID and creates new probes; account for quota and retention before approval.
The nested call chain is `release-publish.yml -> npm-publish.yml ->
ppt-release-preflight.yml`. With required environment reviewers, the second
probe can require **a second approval after the GitHub Release is public**.
If that approval is denied or the probe fails, GitHub assets may already exist
while npm remains unpublished. Treat this as a partial release: correct the
target/approval issue, then use the manual npm workflow with the same tag and
`from_artifact: false` to use the published assets and rerun the gate. Do not
bypass the second probe or republish a different build under the same version.
Draft GitHub Releases and npm dry runs skip live probes and receive no Bot secret;
they are **not** capability evidence. Dry runs use `ppt-gallery-dry-run`, which
requires no credentials. Direct manual Release publication or publishing outside
these workflows is outside this gate; repository/environment protections and
release-operator policy must prohibit bypassing the supported workflow.

## Tests

### Backend receipt contract

The public wire contract is:

- Bot create returns HTTP 201 with a **flat** receipt containing `docId`,
  `docType`, `templateId`, `title`, `spaceId`, revisions, ownership and URLs.
  Idempotent replay returns that same completed receipt.
- Newly created PPT IDs use `d_` plus 24 hex characters. This does not justify
  rewriting historical IDs or tightening shared CLI response validation.
- PPT readback uses a `{data: ...}` envelope with `docId`, `deck` and
  `baseRevision`.
- Each newly created template, including `blank`, must read back at least one
  slide. Blank means an empty initial slide, not an empty slide array. This
  minimum is a fresh-create release requirement, not a new constraint on
  ordinary read responses for edited or historical documents.

Source checks and prior isolated runtime acceptance support this contract;
their exact refs and captured responses belong in access-controlled evidence.
They do not establish readiness of the current release target.

`internal/registry/specs/docs.json` records these fields and a **synthetic,
source-backed receipt example** with invented identifiers, identity, title, URLs
and timestamp. It is a fixture, not a captured live response or deployment proof.
The mock backend reuses this example; tests pin field types and flat shape.
PPT-only fields remain optional for the shared `docs.create` endpoint, with no
response-side template enum: completed historical receipts can carry retired IDs.
Wrong-template tests change only the echoed ID in an otherwise valid receipt,
so missing unrelated fields cannot mask removal of that check.

### Guard execution

README/CHANGELOG/release-gate-only changes run the existing Go and Node checks on both PRs
and main pushes because the guards read those files. Other documentation-only
changes retain their skip behavior; embedded skill Markdown continues to run CI.
GitHub push path patterns are ordered: a later positive pattern re-includes a
path excluded earlier. The unit test checks that workflow configuration and the
job-level filter wiring; it does not execute GitHub's event matcher or prove a
hosted push run. Confirm the actual CI run on the release commit before publishing.

`node --test scripts/ppt-release-preflight.test.mjs` uses mocked HTTP responses
and inert credentials. It covers all five creates/readbacks, fresh keys, old
catalogues, auth/redirect/transport failures, bounded/malformed responses, receipt
and deck mismatch, sharing read/update/conflict/readback, missing/unsafe epochs,
scope/identity drift, safe diagnostics, configuration refusal, and both workflow entry points.
No live deployment readiness is inferred from these unit tests.

### Sharing and format contracts

Bot share settings are **flat**, with `docId`, `shareScope`, `shareRole` and
`permissionEpoch`; conflicts return HTTP 409 with `error: share_settings_conflict`
and `current`. A successful PUT increments the epoch by one even for a
restricted-to-restricted update; stale epochs are rejected atomically. The
probe fixture models this shape, not the PPT `{data: ...}` envelope.
The documented Bento format version is 1; the probe rejects unknown versions.
A coordinated format upgrade must update that contract and probe.
