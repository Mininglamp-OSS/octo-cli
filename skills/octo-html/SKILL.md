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

- **Canonical create has no document reference.** Omit `slug`, include a fresh
  `idempotency_key`, and provide `html`. A display name belongs in `meta.title`;
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

```bash
# Canonical create: no --slug. idempotency_key makes retries safe.
octo-cli html publish --data '{"html":"<html><body><h1>Runbook</h1></body></html>","idempotency_key":"create-runbook-001","meta":{"title":"Runbook"},"mount_type":"group","group_no":"<group_no>"}'
# → data: { doc_id, slug, version, url, share_url, size, aids,
#           merged_comments, registered, status }
# Save data.slug; for this new document data.slug == data.doc_id.

# Unmounted creation follows the same identity contract and also gets doc_id.
octo-cli html publish --data '{"html":"<html><body><h1>Private draft</h1></body></html>","idempotency_key":"create-private-001","meta":{"title":"Private draft"}}'

# Publish a later immutable version. Keep the wire field name `slug` and omit
# idempotency_key. An unknown legacy slug is rejected; it cannot create a doc.
octo-cli html publish --data '{"slug":"<doc-ref>","html":"<html><body><h1>Runbook v2</h1></body></html>","meta":{"title":"Runbook"}}'

# List, inspect, list versions, and soft-delete.
octo-cli html list
octo-cli html get <doc-ref>
octo-cli html versions <doc-ref>
octo-cli html rm <doc-ref>
```

Mounts (`group`, `space`, or `thread`) control placement/registration only. For
`group`, pass `group_no`; for `thread`, pass `thread_id`. They do not choose the
document-reference format.

## 2. Author drafts

A draft is an author-only working slot and does not mint an immutable version
until promoted.

```bash
# Create a canonical draft without a document reference. Save response data.slug.
octo-cli html draft create --html '<html><body><h1>WIP</h1></body></html>' --idempotency-key draft-runbook-001

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
