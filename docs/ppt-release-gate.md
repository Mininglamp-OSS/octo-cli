# PPT gallery release prerequisite

The CLI creation policy accepts only `blank`, `signal`, `terra`, `orbital`, and
`picnic`. Deploy the companion backend catalogue and frontend picker before
publishing this CLI. A merged MR, successful source build, or local preview is
not evidence that the release target supports the catalogue.

## Operator setup

Configure the GitHub environment **`ppt-gallery-release`** with required human
reviewers and deployment branches restricted to **`main`**. Store:

- Environment variable `PPT_RELEASE_API_ORIGIN`: the intended release target's
  HTTPS gateway origin only (no `/api`, credentials, query, or fragment).
- Environment variable `PPT_RELEASE_SPACE_ID`: a dedicated acceptance Space on
  that target; do not use a personal working Space.
- Environment secret `PPT_RELEASE_BOT_TOKEN`: a dedicated `app_*` or `bf_*` Bot
  authorized to create and read documents in that Space. Do not use a human key,
  put this secret at repository scope, or reuse a broad production Bot.

Before approving the environment job, verify the target origin and confirm the
frontend picker deployment from [octo-docs-module !105](https://codex.mlamp.cn/dmwork/octo-web-enterprise/octo-docs-module/-/merge_requests/105)
and backend [octo-docs-backend !146](https://codex.mlamp.cn/dmwork/octo-docs-backend/-/merge_requests/146).
The API probe validates backend create/read capability, **not** the frontend UI
or every tenant/replica. Record the companion deployment refs in release evidence.
Recheck after a backend rollback; probe success is point-in-time evidence.

Dispatch publishing workflows **from `main`**, supplying the release tag as the
`tag` input. The preflight runs trusted tooling from the dispatch commit, never
scripts from an arbitrary input tag. The existing CI-evidence/tag checks remain
required; this gate does not replace them. Missing configuration blocks real
publication. This change does not configure secrets or release anything itself.

## What runs and what it writes

Both `release-publish.yml` (before GitHub Release publication) and
`npm-publish.yml` (including manual real uploads and `from_artifact` calls) depend
on `ppt-release-preflight.yml`. It calls `scripts/ppt-release-preflight.mjs` to
create **five fresh documents**, then read each PPT deck back through the Bot API.
It checks receipt template/title/Space/document identity and nonempty Bento decks.
Unique probe UUIDs/idempotency keys prevent old completed receipts from proving
current capability. Any error stops the gate: no redirects, automatic retries,
template substitution, or credential fallback. Responses and requests are bounded.

Probe documents have titles `CLI release probe <UUID> <templateId>` and their IDs
are logged immediately after a valid receipt. They are **retained**, not deleted
by the Bot (Bot deletion is not supported). A failed or timed-out POST may still
have created a document; use the logged UUID/title to inspect the dedicated Space
before operator cleanup. No user documents are rewritten or deleted.

A complete GitHub + npm run probes twice (up to ten documents), so a backend
rollback between the two publication stages also blocks npm. A rerun uses a new
UUID and creates new probes; account for quota and retention before approval.
Draft GitHub Releases and npm dry runs skip live probes and receive no Bot secret;
they are **not** capability evidence. Dry runs use `ppt-gallery-dry-run`, which
requires no credentials. Direct manual Release publication or publishing outside
these workflows is outside this gate; repository/environment protections and
release-operator policy must prohibit bypassing the supported workflow.

## Tests

### Backend receipt contract

The preflight's wire contract was checked against **octo-docs-backend !146 at
`711ad69be5c75a5820a6c4e8af23e71daf46f5b4`**, not inferred from request echo mocks:

- [`src/api/routes/docs.ts`](https://codex.mlamp.cn/dmwork/octo-docs-backend/-/blob/711ad69be5c75a5820a6c4e8af23e71daf46f5b4/src/api/routes/docs.ts#L169)
  sends `createPptDocument(req)` directly with status 201. The Bot create receipt
  is **flat**, unlike PPT readback's `{data: ...}` envelope.
- [`src/api/ppt/docs.ts`](https://codex.mlamp.cn/dmwork/octo-docs-backend/-/blob/711ad69be5c75a5820a6c4e8af23e71daf46f5b4/src/api/ppt/docs.ts#L185)
  builds the receipt with `docId`, `docType`, `templateId`, `title`, `spaceId`,
  revisions, ownership and URLs. It records that same payload for replay.
- [`src/util/ids.ts`](https://codex.mlamp.cn/dmwork/octo-docs-backend/-/blob/711ad69be5c75a5820a6c4e8af23e71daf46f5b4/src/util/ids.ts#L12)
  mints `d_` plus 24 hex characters. This is a new-PPT source contract, not a
  reason to rewrite old IDs or tighten shared CLI response validation.
- [`src/api/ppt/live.ts`](https://codex.mlamp.cn/dmwork/octo-docs-backend/-/blob/711ad69be5c75a5820a6c4e8af23e71daf46f5b4/src/api/ppt/live.ts#L41)
  returns PPT readback under `data` with `docId`, `deck`, and `baseRevision`.

`internal/registry/specs/docs.json` records these fields and a **synthetic,
source-backed receipt example** with invented identifiers, identity, title, URLs
and timestamp. It is a fixture, not a captured live response or deployment proof.
The mock backend reuses this example; tests pin field types and flat shape.
PPT-only fields remain optional for the shared `docs.create` endpoint, with no
response-side template enum: completed historical receipts can carry retired IDs.
Wrong-template tests change only the echoed ID in an otherwise valid receipt,
so missing unrelated fields cannot mask removal of that check.

### Guard execution

README/CHANGELOG-only changes run the existing Go and Node checks on both PRs
and main pushes because the guards read those files. Other documentation-only
changes retain their skip behavior; embedded skill Markdown continues to run CI.

`node --test scripts/ppt-release-preflight.test.mjs` uses mocked HTTP responses
and inert credentials. It covers all five creates/readbacks, fresh keys, old
catalogues, auth/redirect/transport failures, bounded/malformed responses, receipt
and deck mismatch, configuration refusal, and both workflow entry points.
No live deployment readiness is inferred from these unit tests.
