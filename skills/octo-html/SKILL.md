---
name: octo-html
version: 0.3.0
description: HTML docs domain (octo-doc) — create and govern self-contained interactive HTML documents, immutable versions, drafts, sharing, media, comments, and agent element edits. Bots cannot delete documents. This is a DIFFERENT backend from the `octo-docs` (CRDT/Yjs) domain. Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-html — interactive HTML documents

> **This is NOT the `octo-docs` body-editing domain.** `octo-cli html …` talks
> to **octo-doc**, where a document is a self-contained HTML page published as
> immutable versions. `octo-cli docs …` talks to the separate CRDT/Yjs backend.

All commands call `$OCTO_API_BASE_URL/docs-html/v1/*` and return the standard
`{ok, identity, data, ...}` success envelope.

## Document-reference contract

- **Canonical create has no document reference.** Omit `slug` and provide
  `html`. The CLI generates `idempotency_key`; an explicit key is optional. A display name belongs in `meta.title`;
  it is metadata, not identity.
- **Save `data.slug` from the response.** New documents always return
  `data.doc_id` and `data.slug`, with `data.slug == data.doc_id`, whether mounted
  or unmounted. Use `data.slug` for every later operation.
- **Legacy documents keep their old reference.** For an old document, use its
  legacy slug wherever this skill says `<doc-ref>`.
- **No alias identity and no same-name republish.** Creating again with the same
  `meta.title` creates a different document. To publish another version, supply
  the saved `data.slug` in the server's legacy-named `slug` field and set
  `--version` to the version read with `html source` plus one.
- Do not infer a mode from `mount_type`, `registered`, `status`, or whether
  `data.doc_id` is non-empty. `registered` and `status` report operational state,
  not identity.

Query and JSON-body fields remain named `slug` for wire compatibility. Put the
saved document reference in them. Path help displays `<doc-ref>`, and old legacy
slugs are accepted. The CLI does not persist the reference.

**Minimum rollout dependency:** this contract requires the canonical-create
server changes in octo-docs-backend#166 and octo-docs-html#33 to be merged and
deployed before this CLI is released. Source reads and guarded bot updates also
require octo-docs-html#34. **The HTML backend will be deployed first.** The CLI
release and rollout of its updated skills follow only after the source endpoint
and publish guard are deployed and verified on every HTML-serving instance.
Do not release this workflow during a mixed old/new backend rollout.

If `html source` unexpectedly returns a route-level 404, check the same
reference with `octo-cli html get <doc-ref>` using the same identity and gateway.
If metadata is readable, the document exists: report that the source endpoint
is unavailable and the backend deployment or gateway routing needs checking.
Do not report the document as missing or create a replacement. If both reads
fail, a 404 alone cannot distinguish a missing/inaccessible document from a
deployment problem; retain the reference and report the uncertainty.
Stop this update until source access is restored. Do not infer a version from
metadata, publish without a version, or switch endpoints to bypass the guard.
Once the backend is ready, recover actual 409/428 version errors autonomously
as described below.

## Auth & space

- Authenticate with a stored bot profile (`--profile` / `--bot-id`) or
  `OCTO_BOT_TOKEN`; confirm the selected identity with `octo-cli config show`.
- Do not pass `--space`. octo-doc resolves identity and space server-side.
- Write operations require author/write capability. Reads need at least reader
  capability; backend failures are normalized into the CLI's
  `{ok:false,error:{type,code,message,hint,detail}}` envelope.

## 1. Create and publish

**Bots cannot delete documents, even as author, owner, or admin.** Do not run
`octo-cli html rm <doc-ref>` or use `docs delete` / raw `api DELETE` as an
alternative. Ask a human with document admin permission to delete it in Octo.
This includes cleanup of test documents and applies to canonical IDs and legacy
slugs. Creating, publishing versions, editing, and permitted asset/comment
operations are unchanged.

`html rm` targets `/docs-html/v1/docs/{doc_id}`, a different service entry from
the docs-backend `/v1/bot/docs/octo-doc/:octoDocSlug` deletion route. Do not infer
that one route's deployment proves the other's enforcement, and do not probe
the other route as a workaround.

**Documents are declarative: no JavaScript.** The backend rejects any publish or
draft whose HTML carries script, with `400` and the stable code
`html_contains_javascript`. This is not advisory — there is no flag to opt out.
When generating a document, never emit:

- `<script>` elements (including inside `<svg>`),
- `on*` event-handler attributes (`onclick`, `onload`, `onerror`, …),
- `javascript:` / `vbscript:` / scriptable `data:` URLs in `href`, `src`,
  `xlink:href`, `action`, `formaction`, `object[data]`, or a non-empty `srcdoc`.

Express interaction with CSS instead — `:hover`, `:target`, `:checked` +
sibling selectors, `<details>`/`<summary>`, transitions and animations all work
and cover most of what a document needs. Do not assume script is merely inert:
the host application embeds documents in a sandboxed iframe, but the document's
own version URL serves the stored HTML as a top-level page, so script that got
stored would execute for every reader who opens that link. CSS, inline `style`,
`<iframe>`, `<noscript>` and `<meta http-equiv=refresh>` are all still allowed.

On rejection the error `details.violations` lists every offending construct with
its `kind`, `tag`, `attr` and 1-based `line`. Fix all of them in one pass — the
list is complete (capped at 50, with `details.truncated` set when it overflows).
Do not retry the same document unchanged, and do not try to work around the gate
by encoding or splitting the script.

```bash
# Canonical create: no --slug. The CLI generates the idempotency key.
octo-cli html publish --data '{"html":"<html><body><h1>Runbook</h1></body></html>","meta":{"title":"Runbook"},"mount_type":"group","group_no":"<group_no>"}'
# → data: { doc_id, slug, version, url, share_url, size, aids,
#           merged_comments, registered, status }
# Save data.slug; for this new document data.slug == data.doc_id.

# Unmounted creation follows the same identity contract and also gets doc_id.
octo-cli html publish --data '{"html":"<html><body><h1>Private draft</h1></body></html>","meta":{"title":"Private draft"}}'

# Read HTML and its version together, then edit data.html.
octo-cli html source <doc-ref>
# → data: { slug, version, html }
# Publish version 2 only if the source read returned version 1.
# Keep the wire field name `slug` and omit
# idempotency_key. Pass ONLY a slug the server returned earlier: an unregistered
# slug does not create a canonical document — it produces a legacy unregistered
# one that never appears in the sidebar file list. Never invent a slug.
octo-cli html publish --version 2 --data '{"slug":"<doc-ref>","html":"<html><body><h1>Runbook v2</h1></body></html>","meta":{"title":"Runbook"}}'

# An explicit --idempotency-key <same-operation-key> is supported only for
# retrying this exact creation operation. The CLI-generated key is created once
# per invocation and the HTTP retry loop reuses the same serialized request.
# Reusing a key with different HTML returns the old document and discards the
# new HTML. Once its document is deleted, that key is unusable.
#
# UNATTENDED CALLERS: supply your own stable --idempotency-key and persist it
# before the call. A generated key lives only for that invocation, so if a
# timeout or 5xx leaves the outcome unknown, a plain re-run creates a SECOND
# document without returning the first one's reference. Bots must not delete
# documents as cleanup. With your own key the re-run resumes the same
# creation. A failed create also reports the key it used in the error envelope's
# detail (and hint), so an ambiguous failure stays recoverable either way.

# List, inspect, and list versions.
octo-cli html list
octo-cli html get <doc-ref>
octo-cli html versions <doc-ref>
```

`html list` returns the backend's offset envelope as `data` plus `_pagination`
(`total`, `page`, `page_size`). It has no cursor flags or `--page-all` support.

Mounts (`group`, `space`, or `thread`) control placement/registration only. For
`group`, pass `group_no`; for `thread`, pass `thread_id`. They do not choose the
document-reference format.

WITHOUT `mount_type` the backend skips docs-backend registration, so the HTML
never shows up in the sidebar file list — this is the #1 "my doc didn't appear"
gotcha. An unmounted document still receives a canonical `doc_id`; registration
and identity are separate concerns.

### Concurrent edits and the existing `--version` flag

Before editing an existing HTML document, run `octo-cli html source <doc-ref>`.
Retain `data.html` and `data.version` from that same response. It reads
`GET /docs-html/v1/docs/{doc-ref}/source` and returns the full stored HTML without
the viewer overlay. To inspect a fixed historical source, use
`octo-cli html source <doc-ref> --version 7`; omit `--version` to read latest.
`html get` only returns metadata; do not pair a separately fetched version
number with older HTML.

`--version` is the **new output version**. If the source read was version N,
edit that source and publish with `--version N+1` (a concrete integer, such as
`--version 2` above). The server's concurrency guard requires this value to
match its current latest version + 1. Updates must not use omitted or zero
version, because automatic allocation cannot detect a stale edit. First
publication can still omit the flag. No new CLI parameter is needed.

HTTP 428 / `version_required` means the update omitted its output version.
HTTP 409 / `version_conflict` means the requested version is stale or skips
past the next version. The backend's `error.details` contains `version`,
`latest_version`, `next_version`, and `source_path`; in CLI output these are
preserved under `error.detail.error.details`. Read that source, inspect others'
changes, and reapply the intended edit before submitting its version plus one.

#### Recover version errors autonomously

**The bot MUST recover from `version_conflict` and `version_required` within
the same task. Do not end the task by reporting the raw version error to the
user or ask the user to perform a routine reread/retry.** CLI exit code 2 is a
tool result for the bot to handle, not the final answer to the user. Only these
specific version error codes trigger this recovery workflow.

1. Keep the original source, the intended edit, and the same document reference.
2. Run `octo-cli html source <doc-ref>` again **without `--version`**. Retain
   the fresh `data.html` and `data.version` together; the version and fixed
   `source_path` in the earlier error may already be outdated.
3. Compare the original source, your proposed edit, and the fresh source.
   Reapply only the user's intended changes to the fresh HTML, preserving
   concurrent edits. If the requested result is already present, verify it and
   report completion without publishing a duplicate version.
4. Publish the newly merged HTML to the **same `--slug`**, with the fresh
   version plus one. Do not create a new document or reuse a create
   `idempotency_key` for this update.
5. If another version error occurs, repeat from step 2. Make up to **three
   recovery attempts** after the initial rejection; each attempt must reread
   and merge again. This is a bot workflow, not a CLI HTTP retry of the same body.
6. After a successful publish, verify the returned version's source and report
   the completed update and document URL. Mark an edit/comment as applied only
   after verifying that it was published.

Involve the user only when overlapping changes have incompatible meanings that
cannot be resolved from their request, another error prevents completion, or
all three recovery attempts still conflict. Explain the specific unresolved
change or ongoing contention instead of simply forwarding a version error.

**Never just increase `--version` and resend stale HTML.** Do not switch to
publication through drafts or element replacement to bypass a rejected update;
draft promotion and element replacement do not substitute for guarded
full-document publishing. Element replacement's `base_version` locates source
content; it is not this publish precondition.

## 2. Author drafts

A draft is an author-only working slot and does not mint an immutable version
until promoted.

```bash
# Create a canonical draft without a document reference. Save response data.slug.
octo-cli html draft create --html '<html><body><h1>WIP</h1></body></html>'

octo-cli html draft save <doc-ref> --data '{"html":"<html><body><h1>WIP</h1></body></html>"}'
octo-cli html draft promote <doc-ref>
```

## 3. Sharing and grants

```bash
# Mint/rotate or revoke a bearer share code.
octo-cli html share <doc-ref>
octo-cli html unshare <doc-ref>

# Grant/list/revoke named-reader access.
octo-cli html grant add <doc-ref> --uid <uid> --role reader
octo-cli html grant list <doc-ref>
octo-cli html grant rm <doc-ref> <uid>
```

## 4. Media assets

```bash
octo-cli html asset ls <doc-ref>
octo-cli html asset add <doc-ref> --file ./chart.png
octo-cli html asset rm <doc-ref> <sha256>
```

## 5. Comments

The wire parameter remains `slug`; pass the saved document reference.

```bash
octo-cli html comment list --slug <doc-ref> [--version all]
octo-cli html comment add --data '{"slug":"<doc-ref>","text":"Please clarify this","anchor":{"kind":"element","aid":"<content-hash>"}}'
```

`html comment list` likewise preserves offset metadata in `_pagination`; it has
no cursor flags or `--page-all` support.

## 6. Agent element edit and reply

```bash
# Read one stamped artifact (version 0 or omitted means latest).
octo-cli html element get --data '{"slug":"<doc-ref>","aid":"<content-hash>"}'

# Replace exactly one safe top-level element and publish a new version.
octo-cli html element replace --data '{"slug":"<doc-ref>","aid":"<content-hash>","new_html":"<section>updated body</section>"}'

# Reply to a comment thread with an optional applied/partial/question verdict.
octo-cli html reply --data '{"slug":"<doc-ref>","parent_id":"<comment-root-id>","text":"Done.","status":"applied"}'
```

To preserve comment anchors, prefer narrow element replacements; avoid changing
an element's tag or nearby heading unless necessary.

## Errors

- `428 VALIDATION_ERROR`, with `error.detail.error.details.code=version_required`
  — a bot update omitted its output version or sent zero. Read with `html source`,
  edit that source, then publish its version plus one autonomously using the
  recovery workflow above; do not stop at the tool error.
- `409 CONFLICT`, with `error.detail.error.details.code=version_conflict`
  — the output version is stale or skips ahead. Read the latest source and
  reapply the intended edit while preserving others' changes. Never just increase
  `--version` and resend stale HTML. Both errors exit with code 2; the backend
  recovery hint is preserved at `error.detail.error.hint` and version/source
  fields at `error.detail.error.details`. Complete the recovery workflow above
  before reporting the result to the user.

- `403 bot_delete_forbidden` — terminal bot-deletion policy denial, including
  already-deleted retries on the docs-backend routes. Do not retry, change
  roles/identity/Space, or switch endpoints; ask a human document admin to delete
  it. This is not a successful deletion or a missing-membership problem.
- `401 / 403` — missing or insufficient capability.
- `404` — the requested document, comment, or aid may be missing or inaccessible.
  A route-level 404 from `html source` can instead indicate an unavailable source
  endpoint. Follow the rollout check above before diagnosing the document as
  missing: readable metadata confirms it exists but is never a source/version
  fallback. Do not create a replacement document or publish without the guard.
- `400 html_contains_javascript` — the published or draft HTML carries
  JavaScript (see the no-JavaScript rule in §1). `details.violations` names every
  offending construct with its `kind` / `tag` / `attr` / `line`; regenerate the
  document without script rather than retrying it unchanged.
- Element replacement rejects multiple top-level elements, scripts/styles,
  inline event handlers, and `javascript:` URLs.

## Schema lookup

```bash
octo-cli schema --list html
octo-cli schema html.source
octo-cli schema html.publish
octo-cli schema html.element.replace
octo-cli schema html.reply
```
