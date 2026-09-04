---
name: octo-html
version: 0.1.0
description: HTML docs domain (octo-doc) — publish and govern self-contained interactive HTML documents, list/get/versions, author drafts, per-doc share codes, media assets, inline comments, and the agent element read/replace + reply paths. This is a DIFFERENT backend from the `octo-docs` (CRDT/Yjs) domain. Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-html — HTML documents (octo-doc): publish, versions, drafts, share, assets, comments, agent element edit

> **This is NOT the `octo-docs` domain.** `octo-cli docs …` talks to the CRDT/Yjs
> collaborative docs-backend (live rich-text/sheet/board over websockets).
> `octo-cli html …` talks to **octo-doc** — the self-hosted, prompt-native
> *interactive HTML document* service. A doc here is a full self-contained HTML
> page, published as an **immutable version** at `/d/<slug>/v/<n>`, with anchored
> inline comments. An agent authors by publishing HTML, and edits by **replacing a
> single stamped artifact** (located by its content-hash `aid`) which republishes
> a new version. Pick the domain that matches the backend you are pointed at.

All commands call `$OCTO_API_BASE_URL/docs-html/v1/*` and return the `{data}/{error}` envelope.

## Auth & space

- Authenticate with a bot token via a stored profile (`--profile` / `--bot-id`)
  or `OCTO_TOKEN` (legacy alias `OCTO_BOT_TOKEN`), sent as
  `Authorization: Bearer`. Confirm with
  `octo-cli config show`.
- **Do not pass a space flag.** octo-doc resolves identity/space server-side
  (via the reverse-proxy trust headers on the fusion deploy); the CLI does not
  send a client space header.
- Write operations (`publish`, `draft`, `share`, `rm`, `asset add/rm`, `reply`,
  `element replace`) require the author/write capability. Reads (`list`, `get`,
  `versions`, `asset ls`, `comment list`, `element get`) need at least a reader
  capability (the doc's share code) — the CLI surfaces the backend's 401/403
  envelopes unchanged.

## 1. Document lifecycle

```bash
# Publish a self-contained HTML document under a slug. New slug = create;
# existing slug = append a new immutable version. --data is a JSON object.
# mount_type is OPTIONAL in the spec but STRONGLY RECOMMENDED (see the
# sidebar-registration note below); include it on every publish that should
# appear in the file sidebar.
octo-cli html publish --data '{"slug":"runbook","html":"<html><body><h1>Runbook</h1></body></html>","meta":{"title":"Runbook"},"mount_type":"group","group_no":"<group_no>"}'
#   → { slug, version, url, size, aids, merged_comments }

# Register the doc into the file sidebar: pass mount_type (non-empty) on every
# publish that should be visible there. Omitting it is valid per the spec, but
# WITHOUT mount_type the backend skips docs-backend registration, so the HTML
# never shows up in the sidebar file list — this is the #1 "my doc didn't
# appear" gotcha. Pass 'group' (+ group_no) or 'space'; 'thread' is accepted but
# intentionally NOT registered. If you want sidebar visibility, treat a
# missing/empty mount_type as the cause, not a valid default.
octo-cli html publish --data '{"slug":"runbook","html":"<html>...</html>","meta":{"title":"Runbook"},"mount_type":"group","group_no":"<group_no>"}'

# List documents (owner-scoped index).
octo-cli html list

# Fetch one document's metadata (slug, title, latest version, timestamps).
octo-cli html get <slug>

# List a document's versions.
octo-cli html versions <slug>

# Soft-delete a document (author-only).
octo-cli html rm <slug>
```

## 2. Author drafts

A draft is the author-only working slot; it does not mint an immutable version
until promoted.

```bash
# Save the draft HTML.
octo-cli html draft save <slug> --data '{"html":"<html><body><h1>WIP</h1></body></html>"}'

# Promote the draft to an immutable version.
octo-cli html draft promote <slug>          # → { slug, version, url }
```

## 3. Sharing

```bash
# Mint / rotate the per-doc read+comment share code (author-only).
octo-cli html share <slug>                   # → { code, url }

# Revoke the share code.
octo-cli html unshare <slug>
```

## 3b. Per-uid access grants (author-only)

Grant a SPECIFIC uid reader access — precise, per-user authorization, unlike the
share code (which is a bearer secret anyone holding can use). A granted uid
resolves to a reader capability on subsequent requests with NO share code needed;
this is how an author lets a named bot/user read+comment a private doc. All three
are author-only (only the doc creator can grant/revoke/list).

```bash
# Grant a uid reader access (upsert by uid). role defaults to 'reader'.
# Either individual flags or --data JSON work; flags override --data.
octo-cli html grant add <slug> --uid <uid> --role reader
octo-cli html grant add <slug> --data '{"uid":"<uid>","role":"reader"}'
#   → { slug, uid, role }

# List a doc's grants (uid + role).
octo-cli html grant list <slug>

# Revoke a uid's grant. The creator cannot be removed (409 conflict).
octo-cli html grant rm <slug> <uid>
```

## 4. Media assets

```bash
# List a document's assets (reader).
octo-cli html asset ls <slug>

# Upload an asset (author). Multipart form-data (field "file"); pass a local path.
octo-cli html asset add <slug> --file ./chart.png

# Delete an asset by its sha256 (author).
octo-cli html asset rm <slug> <sha256>
```

## 5. Comments

Comments anchor to highlighted text or to a stamped artifact (by `aid`). They
survive edits and re-anchor across versions; an artifact that is genuinely
replaced goes `lost` rather than silently re-attaching.

```bash
# List a document's comments. --version accepts a number or 'all'.
octo-cli html comment list --slug <slug> [--version all]

# Create a comment. Omit anchor for an unanchored comment.
octo-cli html comment add --data '{"slug":"runbook","text":"Please clarify this","anchor":{"kind":"element","aid":"<content-hash>"}}'
```

## 6. Agent element edit (read / replace) + reply

This is the core "agent edits the HTML" path. An agent reads one artifact by its
stamped `data-odoc-aid`, replaces it, and the server republishes a new version
(re-stamping aids and reconciling comment anchors).

```bash
# Read one artifact's current outer HTML by aid (version 0 or omitted = latest).
octo-cli html element get --data '{"slug":"runbook","aid":"<content-hash>"}'
#   → { aid, tag, html }

# Replace that artifact and republish. new_html MUST be exactly ONE top-level
# element and free of <script>/<style>, inline on*= handlers, and javascript:
# URLs (the backend rejects otherwise). base_version 0/omitted = latest.
octo-cli html element replace --data '{"slug":"runbook","aid":"<content-hash>","new_html":"<section>updated body</section>"}'
#   → { slug, version, url }

# Reply to a comment thread as the agent (identity odoc-agent) with a verdict.
# status ∈ applied | partial | question (rendered as ✅/🟡/❓ on the parent).
octo-cli html reply --data '{"slug":"runbook","parent_id":"<comment-root-id>","text":"Done, updated the section.","status":"applied"}'
```

**Editing discipline (keep comment anchors alive).** Each `element replace`
republishes and re-stamps every aid, then reconciles anchors. To minimise
comments going `lost`: change an element's *content* only, avoid changing its tag
type or the nearest heading, and prefer narrow single-artifact edits over broad
rewrites.

## Errors

The CLI surfaces the backend envelope unchanged. Common cases:
- `401 / 403` — missing or insufficient capability (need write token / share code).
- `404` — slug or aid not found.
- element replace rejects a fragment that is not exactly one safe top-level
  element (multi-element, `<script>`, `on*=`, `javascript:`).

## Schema lookup

```bash
octo-cli schema --list html
octo-cli schema html.publish
octo-cli schema html.element.replace
octo-cli schema html.reply
```
