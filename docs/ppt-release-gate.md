# PPT deployment compatibility

The CLI creation policy accepts only `blank`, `signal`, `terra`, `orbital`, and
`picnic`. The intended environment must have the companion backend catalogue and
frontend picker deployed. A merged MR, successful source build, or local preview
is not evidence that a particular deployment supports the catalogue.

## Publishing and deployment acceptance

GitHub Release and npm publishing use the existing CI-evidence, build, packaging
and checksum checks. They do not connect to an Octo deployment, require a
PPT-specific GitHub Environment or Bot credential, or create test documents.
The npm publication credential is separate and remains required where applicable.

This intentionally restores the publishing behavior from before PR #173: no
main-only dispatch-ref check and no PPT-specific environment approval. Both were
introduced as part of that PR's live probe, not pre-existing release requirements.
The main-only check is deliberately not retained as a separate control. An
authorized operator can dispatch from a non-main ref; GitHub executes the workflow
definition from the selected ref. Operators are responsible for selecting the
intended workflow ref and release tag. The retained tag and CI-evidence checks
validate the requested tag and the tagged commit's CI evidence, not the selected
workflow definition or the artifacts it produces. This is an explicit policy
choice, not a claim that those checks replace ref restrictions or human approval.

Live acceptance is an operator-run check when related backend/frontend services
change, not a mandatory task for every CLI release. Use an explicitly authorized
test environment and identity, confirm the template picker and rendered effects,
and record create/read and sharing results as deployment evidence. Creating test
documents or changing sharing is a write operation: agree on scope and cleanup
before doing it. No such operations are triggered by the publishing workflows.

## Current rollout dependencies

The gallery has separate data, player, catalogue and UI prerequisites.
First merge and deploy all gallery data (Signal, Picnic, Terra and Orbital) and
Player prerequisites; then merge and deploy Catalogue activation with the
Frontend picker. Capacity is the separate 32 MiB document / 40 MiB full-edit
request prerequisite for large PPTs. This table specifies compatibility conditions,
not current merge or deployment status or an automated publishing gate.

| Component | Release condition |
| --- | --- |
| Terra | Merge and deploy before activation |
| Orbital | Merge and deploy before activation |
| Player | Merge and deploy before activation |
| Catalogue activation | Merge and deploy after prerequisites; all five choices must work |
| Capacity | Deploy migration, request/proxy and MySQL packet settings before large-deck acceptance |
| Frontend | Confirm the picker and rendered template effects in the actual deployed UI |
| Media ingestion | Deploy native media ingestion, editor resolution and offline export before using uploaded media |

Media ingestion is independent of gallery activation: new uploaded/public-URL
media needs a backend returning native `ppt-media:` references, a matching editor
resolver and an exporter that embeds registered same-document attachments.
Existing bundled template assets keep working without using the ingest endpoint.
This row adds no release-time probe or publishing configuration.

Keep implementation links, source refs and exact deployed backend/frontend SHAs
in access-controlled operational evidence. Source refs alone are not proof of
deployment. Do not put internal repository or personal namespace details in this
public guide, its tests, or public PR descriptions.

Update this dependency table and the expected rows in
`skills/ppt_templates_test.go` together when compatibility conditions change.
Embedded skills and CHANGELOG link here instead of copying operational refs.

## Backend receipt contract

- Bot create returns HTTP 201 with a **flat** receipt containing `docId`,
  `docType`, `templateId`, `title`, `spaceId`, revisions, ownership and URLs.
  Idempotent replay returns that same completed receipt.
- Newly created PPT IDs use `d_` plus 24 hex characters. This does not justify
  rewriting historical IDs or tightening shared CLI response validation.
- PPT readback uses a `{data: ...}` envelope with `docId`, `deck` and
  `baseRevision`.
- Each newly created template, including `blank`, starts with at least one slide.
  Blank means an empty initial slide, not an empty slide array. This is not a
  constraint on ordinary read responses for edited or historical documents.

`internal/registry/specs/docs.json` records these fields and a synthetic,
source-backed receipt example with invented identifiers, identity, title, URLs
and timestamp. It is a fixture, not a captured live response or deployment proof.
Tests pin field types and flat shape. PPT-only fields remain optional for the
shared `docs.create` endpoint, with no response-side template enum: completed
historical receipts can carry retired IDs.

## Sharing and format contracts

Bot share settings are flat, with `docId`, `shareScope`, `shareRole` and
`permissionEpoch`. Read the epoch before updating sharing. A successful PUT
increments it by one; stale epochs are rejected atomically with HTTP 409,
`error: share_settings_conflict` and `current`. Re-read and reconsider the intended
change after a conflict; do not automatically retry or broaden access.
The documented Bento format version is 1.

## Local checks

`go test ./cmd/service ./skills` covers CLI template creation, validation, sharing
epochs and embedded usage guidance. `node --test scripts/release-workflows.test.mjs`
checks the documented receipt contract and publishing workflow wiring without
credentials or live API calls. README/CHANGELOG/this-guide changes run the existing
Go and Node checks on PRs and main pushes; other documentation-only changes retain
their skip behavior. These checks do not establish live deployment readiness.
