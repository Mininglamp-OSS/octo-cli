---
name: octo-html
version: 0.2.0
description: HTML docs domain (octo-doc) — create and govern self-contained interactive HTML documents, immutable versions, drafts, sharing, media, comments, and agent element edits. This is a DIFFERENT backend from the `octo-docs` (CRDT/Yjs) domain. Load after octo-shared.
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
  the saved `data.slug` in the server's legacy-named `slug` field.
- Do not infer a mode from `mount_type`, `registered`, `status`, or whether
  `data.doc_id` is non-empty. `registered` and `status` report operational state,
  not identity.

Query and JSON-body fields remain named `slug` for wire compatibility. Put the
saved document reference in them. Path help displays `<doc-ref>`, and old legacy
slugs are accepted. The CLI does not persist the reference.

**Minimum rollout dependency:** this contract requires the canonical-create
server changes in octo-docs-backend#166 and octo-docs-html#33 to be merged and
deployed before this CLI is released.

## Auth & space

- Authenticate with a stored bot profile (`--profile` / `--bot-id`) or
  `OCTO_BOT_TOKEN`; confirm the selected identity with `octo-cli config show`.
- Do not pass `--space`. octo-doc resolves identity and space server-side.
- Write operations require author/write capability. Reads need at least reader
  capability; backend failures are normalized into the CLI's
  `{ok:false,error:{type,code,message,hint,detail}}` envelope.

## 1. Create and publish

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

# Publish a later immutable version. Keep the wire field name `slug` and omit
# idempotency_key. Pass ONLY a slug the server returned earlier: an unregistered
# slug does not create a canonical document — it produces a legacy unregistered
# one that never appears in the sidebar file list. Never invent a slug.
octo-cli html publish --data '{"slug":"<doc-ref>","html":"<html><body><h1>Runbook v2</h1></body></html>","meta":{"title":"Runbook"}}'

# An explicit --idempotency-key <same-operation-key> is supported only for
# retrying this exact creation operation. The CLI-generated key is created once
# per invocation and the HTTP retry loop reuses the same serialized request.
# Reusing a key with different HTML returns the old document and discards the
# new HTML. Once its document is deleted, that key is unusable.
#
# UNATTENDED CALLERS: supply your own stable --idempotency-key and persist it
# before the call. A generated key lives only for that invocation, so if a
# timeout or 5xx leaves the outcome unknown, a plain re-run creates a SECOND
# document — and the first one's reference was never returned, so it can be
# neither addressed nor deleted. With your own key the re-run resumes the same
# creation. A failed create also reports the key it used in the error envelope's
# detail (and hint), so an ambiguous failure stays recoverable either way.

# List, inspect, list versions, and soft-delete.
octo-cli html list
octo-cli html get <doc-ref>
octo-cli html versions <doc-ref>
octo-cli html rm <doc-ref>
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

- `401 / 403` — missing or insufficient capability.
- `404` — document reference (canonical doc_id or legacy slug), comment, or aid
  not found.
- `400 html_contains_javascript` — the published or draft HTML carries
  JavaScript (see the no-JavaScript rule in §1). `details.violations` names every
  offending construct with its `kind` / `tag` / `attr` / `line`; regenerate the
  document without script rather than retrying it unchanged.
- Element replacement rejects multiple top-level elements, scripts/styles,
  inline event handlers, and `javascript:` URLs.

## Schema lookup

```bash
octo-cli schema --list html
octo-cli schema html.publish
octo-cli schema html.element.replace
octo-cli schema html.reply
```
