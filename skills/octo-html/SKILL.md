---
name: octo-html
version: 0.1.0
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
  `html`. The CLI generates `idempotency_key`; an explicit key is optional. Set a
  display name with the first-class `--title` flag; it is metadata, not identity.
- **Save `data.slug` from the response.** New documents always return
  `data.doc_id` and `data.slug`, with `data.slug == data.doc_id`, whether mounted
  or unmounted. Use `data.slug` for every later operation.
- **Legacy documents keep their old reference.** For an old document, use its
  legacy slug wherever this skill says `<doc-ref>`.
- **No alias identity and no same-name republish.** Creating again with the same
  `--title` creates a different document. To publish another version, supply
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

## Display name (`--title`)

`html publish`, `html draft create`, `html draft save`, and `html draft promote`
all take `--title "<name>"`. It sets the human display name shown in listings
and the sidebar. It is **metadata, never identity** — the document reference
(`data.slug` / `data.doc_id`) is the only identity, so passing the same title
again does not address an existing document.

On `publish` and `draft create` the legacy `meta.title` inside `--data` is still
accepted as a fallback; when both are present the top-level `--title` wins.
Prefer `--title`. On `draft save` / `draft promote`, omit `--title` to leave the
current display name unchanged — a bare `html draft promote <doc-ref>` sends no
body, exactly as before.

## 1. Create and publish

```bash
# Canonical create: no --slug. The CLI generates the idempotency key.
octo-cli html publish --title 'Runbook' --data '{"html":"<html><body><h1>Runbook</h1></body></html>","mount_type":"group","group_no":"<group_no>"}'
# → data: { doc_id, slug, version, url, share_url, size, aids,
#           merged_comments, registered, status }
# Save data.slug; for this new document data.slug == data.doc_id.

# Unmounted creation follows the same identity contract and also gets doc_id.
octo-cli html publish --html '<html><body><h1>Private draft</h1></body></html>' --title 'Private draft'

# Publish a later immutable version. Keep the wire field name `slug` and omit
# idempotency_key. Pass ONLY a slug the server returned earlier: an unregistered
# slug does not create a canonical document — it produces a legacy unregistered
# one that never appears in the sidebar file list. Never invent a slug.
octo-cli html publish --slug '<doc-ref>' --title 'Runbook' --html '<html><body><h1>Runbook v2</h1></body></html>'

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
octo-cli html draft create --html '<html><body><h1>WIP</h1></body></html>' --title 'WIP runbook'

# Retitle while saving, or leave --title off to keep the current display name.
octo-cli html draft save <doc-ref> --html '<html><body><h1>WIP</h1></body></html>' --title 'Runbook (draft)'
octo-cli html draft promote <doc-ref>
octo-cli html draft promote <doc-ref> --title 'Runbook'
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
- Element replacement rejects multiple top-level elements, scripts/styles,
  inline event handlers, and `javascript:` URLs.

## Schema lookup

```bash
octo-cli schema --list html
octo-cli schema html.publish
octo-cli schema html.element.replace
octo-cli schema html.reply
```
