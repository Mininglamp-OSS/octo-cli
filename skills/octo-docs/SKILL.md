---
name: octo-docs
version: 0.1.0
description: Docs domain — create and govern documents, read and incrementally edit a doc's live body, read and batch-edit spreadsheet cells (with paged reads for large sheets), read and batch-edit whiteboard scenes, members and sharing, inline comments, versions/snapshots, and attachment metadata as a bot. Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-docs — documents, body content, sheet cells, board scenes, members, comments, versions, attachments

> **Live body editing IS available for `doc_type: doc`.** You can read a
> document's live body with `docs content get` and apply **incremental block-op
> edits** with `docs content edit` under an `If-Match` base-version guard. These
> are **not** a free-form "set the whole body" — each edit is a batch of insert /
> replace / delete ops addressed by block path, and concurrency is controlled by
> a base-version token: read to get the token, then edit with that token so the
> server can prove the body did not change underneath you (a stale token is
> rejected with `412 base_version_stale`). Only `doc_type: doc` bodies are
> editable this way; a **spreadsheet** (`doc_type: sheet`) has its own read/edit
> surface (§3) and a **whiteboard / board** (`doc_type: board`) has its own scene
> surface (§4) — both return `409 unsupported_doc_type` from the `content`
> endpoints. The document *outline* still lives only in the Yjs session and is not
> a REST surface. Creating a doc still makes an **empty** document; seed its text
> with an initial `docs content edit`.

All commands call `$OCTO_API_BASE_URL/v1/bot/docs/*`.

## Auth & space

- Authenticate with a bot token via a stored profile (`--profile` / `--bot-id`)
  or `OCTO_BOT_TOKEN`; both `app_*` and `bf_*` tokens work. Confirm with
  `octo-cli config show`.
- **Do not pass a space flag for docs.** The bot mount resolves the space
  server-side from the token and deliberately ignores any client-supplied space
  header (anti-spoof). Role enforcement (reader / writer / admin) also happens
  server-side, so the CLI surfaces the backend's `403`/`404` envelopes unchanged.

## 1. Document lifecycle

```bash
# Create an empty doc (caller becomes owner/admin). Seed the body with `docs content edit` (§2).
octo-cli docs create [--title "Runbook"] [--folderId f_123] [--docType doc]

# List docs you own or are a member of. Page-based (see the pagination note below).
octo-cli docs list [--folderId f_123] [--page 1] [--pageSize 20] [--sort updatedAt:desc]

octo-cli docs get    <docId>                 # metadata + your role
octo-cli docs rename <docId> --title "New title"
octo-cli docs delete <docId>                 # soft delete (admin)
```

## 2. Document body (read + incremental edit)

Available for `doc_type: doc` only. Read the live body and its base-version
token, then apply a batch of incremental block ops guarded by that token.

```bash
# Read the LIVE body + baseVersion (reader). Response:
#   { docId, doc: <ProseMirror JSON>, schemaVersion, baseVersion }
octo-cli docs content get <docId>

# Apply an incremental edit (writer). --base-version is REQUIRED and is sent as
# the If-Match header; the ops batch goes through --data as a JSON array.
octo-cli docs content edit <docId> --base-version "<token>" --data '<ops JSON>'
```

Ops are addressed by **block path** (child indices from the doc root) and come
in three shapes — this is not a whole-body replace:

- `insert` — `{"type":"insert","at":{"path":[i,...],"position":"before|after|inside_start|inside_end"},"content":[<block nodes>]}`. Use `path: []` with `inside_start`/`inside_end` to write the first block of an empty doc.
- `replace` — `{"type":"replace","range":{"from":{"path":[...]},"to":{"path":[...]}},"content":[<block nodes>]}`
- `delete` — `{"type":"delete","range":{"from":{"path":[...]},"to":{"path":[...]}}}`

Range endpoints must share a parent. Each op may carry `"expect":{"type":"<nodeType>"}` to assert the addressed node type. On success the response returns a **new** `baseVersion` — use it for the next edit.

### Worked example: read, then append a paragraph

```bash
# 1. Read the current body and capture the base version.
BV=$(octo-cli docs content get d_123 --format json | jq -r '.data.baseVersion')

# 2. Append a paragraph at the end of the doc, guarded by that base version.
octo-cli docs content edit d_123 --base-version "$BV" --data '{
  "ops": [
    {
      "type": "insert",
      "at": { "path": [], "position": "inside_end" },
      "content": [
        { "type": "paragraph", "content": [ { "type": "text", "text": "Appended by the bot." } ] }
      ]
    }
  ]
}'
```

**Concurrency / errors.** The base version is optimistic-concurrency: if the
live body changed since your `docs content get`, the edit is rejected with
`412 base_version_stale` — re-read to get a fresh token and rebuild your ops.
Other backend gates surface unchanged: `409 unsupported_doc_type` (target is a
board/whiteboard), `413 too_many_ops` / `413 op_content_too_large` /
`413 doc_too_large` (size caps), `400 path_too_deep` or `400 invalid_body`
(missing base version or malformed shape), and `422` for a bad anchor
(`anchor_not_found` / `anchor_mismatch`), invalid ops (`invalid_ops`), an
attachment that is not this doc's (`attachment_not_found`), or content the
schema rejects (`schema_incompatible`). Whiteboard and board bodies remain
un-editable through this surface.

## 3. Spreadsheet cells (read + batch edit)

Available for `doc_type: sheet` only. A spreadsheet stores a flat cell map on the
Y.Doc, keyed `sheetId!row:col` (e.g. `default!0:0`), with values `{v,f,s}` —
`v` a string/number/boolean/null, `f` an optional formula, `s` an opaque resolved
style object. Same read-token-then-guarded-write discipline as the body surface.

```bash
# Read the LIVE cells + dims + baseVersion (reader). Response:
#   { docId, sheetCells: { "sheetId!row:col": {v,f,s} }, sheetDims: { "c<idx>|r<idx>": px }, baseVersion }
octo-cli docs sheet get <docId>

# Batch-edit cells (writer). --base-version is REQUIRED and is sent as the
# If-Match header; the cells batch goes through --data as a JSON object.
# A cell value of null DELETES that cell.
octo-cli docs sheet edit <docId> --base-version "<token>" \
  --data '{"cells":{"default!0:0":{"v":"hi"},"default!1:0":null}}'
```

### Reading a large sheet in pages

A whole-sheet `docs sheet get` of a grid over the server's ~1MB read cap returns
`413 sheet_too_large`. Pass `--limit <n>` to read it in pages instead: each
response carries `hasMore` and an opaque `nextCursor`; feed that back via
`--cursor` until `hasMore` is false. `sheetDims` comes back on the first page
only. Each page is bounded by both `--limit` and the byte cap, so no page
exceeds ~1MB regardless of `--limit`.

```bash
cursor=""
while : ; do
  page=$(octo-cli docs sheet get d_123 --limit 1000 ${cursor:+--cursor "$cursor"} --format json)
  echo "$page" | jq '.data.sheetCells'          # process this page
  more=$(echo "$page" | jq -r '.data.hasMore')
  [ "$more" = "true" ] || break
  cursor=$(echo "$page" | jq -r '.data.nextCursor')
done
```

**Concurrency / errors.** The base version is optimistic-concurrency: if the
sheet changed since your `docs sheet get`, an edit is rejected with
`412 base_version_stale` — re-read for a fresh token. During a paged read, if the
sheet is written between pages your `--cursor` is rejected with
`409 sheet_changed` (restart from the first page for a consistent snapshot).
Other gates: `409 unsupported_doc_type` (target is a doc/board/whiteboard),
`413 too_many_cells` / `413 cell_too_large` (write size caps),
`400 invalid_body` (missing base version or malformed shape),
`400 invalid_limit` / `400 invalid_cursor` (bad pagination params), and
`422 sheet_snapshot_invalid` (a cell violates the `{v,f,s}` contract or the
`sheetId!row:col` key shape).

## 4. Whiteboard scenes (read + batch edit)

Available for `doc_type: board` only. A whiteboard stores its Excalidraw scene on
the Y.Doc — an ordered list of `elements` (in fractional-index / z-order) plus a
`files` map of image/file refs. Same read-token-then-guarded-write discipline as
the body and sheet surfaces.

```bash
# Read the LIVE scene + baseVersion (reader). Response:
#   { docId, elements: [ <element> ], files: { <fileId>: {attachId} }, schemaVersion, baseVersion }
octo-cli docs scene get <docId>

# Batch-edit the scene (writer). --base-version is REQUIRED and is sent as the
# If-Match header; the batch goes through --data as a JSON object.
#   elements          — full elements to upsert (CAS: higher `version` wins)
#   deletedElementIds — element ids to soft-delete (tombstone)
#   files             — file refs to upsert
octo-cli docs scene edit <docId> --base-version "<token>" \
  --data '{"elements":[{"id":"e1","type":"rectangle","version":4}],"deletedElementIds":["e2"],"files":{}}'
```

The upsert is element-level: only the ids named in `elements` / `deletedElementIds`
are touched (the rest of the board is left as-is). On a key collision the element
with the higher `version` (then smaller `versionNonce`) wins, and a delete is a
soft-delete tombstone with a superseding version so it converges under CAS.

**Concurrency / errors.** The base version is optimistic-concurrency: if the scene
changed since your `docs scene get`, an edit is rejected with
`412 base_version_stale` — re-read for a fresh token. Other gates:
`409 unsupported_doc_type` (target is a doc/sheet), `409 board_snapshot_invalid`
(the live scene decodes to a wrong-kind/corrupt blob, on read),
`413 too_many_elements` / `413 element_too_large` / `413 doc_too_large` (size
caps), `400 invalid_body` (missing base version or malformed shape),
`422 board_element_invalid` (an element fails the whitelist), and
`422 board_file_invalid` (a file ref carries no usable `attachId`).

## 5. Members & sharing

```bash
octo-cli docs members list   <docId>                        # admin
octo-cli docs members set    <docId> --uid <uid> --role writer   # add/upsert; role: reader|writer|admin
octo-cli docs members remove <docId> <uid>                  # admin; owner cannot be removed

# Grant-only forward access (never downgrades). Only reader|writer are grantable.
octo-cli docs forward-grant  <docId> --uid <uid> --role reader
```

`docs members set` is a PUT-upsert: it adds the member if absent or changes the
role if already present. The target uid must be a real Octo user — a miss returns
`404 user_not_found` and writes no ghost member.

## 6. Comments

Comments are anchored to a text range and live out-of-band from the body.

```bash
# List thread roots + replies. --includeResolved 1 also returns resolved threads.
octo-cli docs comments list <docId> [--includeResolved 1] [--cursor <id>] [--limit 50]

# Root comment: needs an anchor. With a live editor selection, pass the opaque
# base64 Yjs positions. Without one (a bot), pass --anchorText and the backend
# resolves it to an anchor.
octo-cli docs comments add <docId> \
  --body "Please clarify this" \
  --anchorStart <base64> --anchorEnd <base64> [--anchorText "the quoted span"]

# Bot root comment (no live positions): anchor by text. Disambiguate a text that
# appears more than once with --blockPath (comma-separated child-index path) and
# --occurrence (1-based match index); an ambiguous match returns 422.
octo-cli docs comments add <docId> \
  --body "Please clarify this" --anchorText "the quoted span" \
  [--blockPath "0,2"] [--occurrence 2]

# Reply to a thread root: set --parentId, omit anchors.
octo-cli docs comments add <docId> --body "Agreed" --parentId <rootId>

# Edit your own comment's text, OR resolve/reopen a thread root (pass one).
octo-cli docs comments edit   <docId> <id> --body "edited text"
octo-cli docs comments edit   <docId> <id> --resolved=true      # writer; false reopens

octo-cli docs comments delete <docId> <id>          # soft delete (author)
octo-cli docs comments delete <docId> <id> --hard 1 # hard delete (admin)
```

Anchors are opaque base64-encoded Yjs positions produced by the editor — the
backend never parses base64 anchors. A bot that has no live editor selection can
still start an anchored thread with `--anchorText`: the backend locates that text
in the stored document and computes the anchor. When the text occurs more than
once the request fails loudly with `422 ambiguous_anchor` (never a silent guess)
— narrow it with `--blockPath` and/or `--occurrence`. Text that is not found
returns `422 anchor_text_not_found`.

## 7. Versions (snapshots)

```bash
octo-cli docs versions list    <docId> [--kind manual|auto|all] [--cursor <id>] [--limit <n>]
octo-cli docs versions create  <docId> [--label "before restructure"]   # writer
octo-cli docs versions state   <docId> <versionId>   # read-only decoded preview (reader)
octo-cli docs versions rename  <docId> <versionId> --label "v2"          # writer
octo-cli docs versions delete  <docId> <versionId>                       # admin
octo-cli docs versions restore <docId> <versionId>                       # admin
```

`versions state` reads *historical* content: it returns a past snapshot decoded
by the doc's kind — a document/sheet as `{kind:"document", doc, sheetCells,
sheetDims, ...}`, a board as `{kind:"board", scene, ...}` — is a preview of that
version rather than the live document, and is not writable. To read the **live**
body instead, use `docs content get` (§2), `docs sheet get` (§3), or
`docs scene get` (§4). `restore` is non-destructive and records a safety snapshot
first, so it is itself undoable.

## 8. Attachments (metadata only)

The docs backend is **presign-only**: the CLI never streams the binary. Uploading
is a two-step flow — presign to register the row and get a signed PUT URL, then PUT
the bytes yourself directly to object storage.

```bash
# Step 1: register + get a presigned PUT target.
octo-cli docs attachments presign <docId> \
  --fileName report.pdf --mime application/pdf --sizeBytes 20481

# The response contains: attachId, uploadUrl, headers{...}, expiresInSec, ...
# Step 2: upload the bytes yourself (NOT through octo-cli — the URL is a
# pre-authorized object-store URL that must not carry the bot Authorization header).
curl -X PUT --upload-file report.pdf \
  -H "Content-Type: application/pdf" \
  "<uploadUrl>"
# Add every header returned under "headers" to the PUT exactly as given.

# Read back a single attachment's signed download URL, or resolve many at once.
octo-cli docs attachments get     <docId> <attachId>
octo-cli docs attachments resolve <docId> --attachIds a1 --attachIds a2
```

> There is no `docs attachments upload` command in this version — the raw PUT
> cannot go through `octo-cli api` either, because that path always prepends
> `OCTO_API_BASE_URL` and attaches the bot bearer token, both wrong for a
> presigned object-store URL. Do the PUT with `curl` (or any HTTP client) as
> shown above.

## 8. Whiteboard image export

Render a whiteboard's **live** Excalidraw scene to an image on the server. Works
only on a `doc_type: board` (a non-board target returns 409 `unsupported_doc_type`).
The response body is binary, so pass `--output`/`-o` to save it to a file; without
`-o` the command only reports the response `{status, content_type, size}`.

```bash
# PNG (default). -o writes the bytes to disk and the envelope echoes the saved path.
octo-cli docs scene export <docId> --format png -o board.png

# SVG (vector).
octo-cli docs scene export <docId> --format svg -o board.svg
```

> `--format` is `png` (default) or `svg`; any other value returns 400
> `invalid_format`. The export reflects the scene as it is live right now
> (shapes, text, and embedded images), not a persisted snapshot.


## Pagination note

The docs list endpoints do **not** use the shared `{data, pagination}` envelope,
so `--page-all` is intentionally not offered on them:

- `docs list` is **page-based** — response is `{total, items}`. Walk it with
  `--page` / `--pageSize`.
- `docs comments list` and `docs versions list` are **cursor-based** — response is
  `{items, nextCursor}`. Pass the returned `nextCursor` back via `--cursor` to get
  the next page; stop when `nextCursor` is null.

## Not in this version

`docs attachments upload` (binary helper), invites, access-requests, and
link-card are out of scope here. Body editing is limited to `doc_type: doc`
incremental block ops (§2), `doc_type: sheet` cell batches (§3), and
`doc_type: board` scene batches (§4); the document outline is not editable
through the CLI.

## Schema lookup

```bash
octo-cli schema docs.create
octo-cli schema docs.content.get
octo-cli schema docs.content.edit
octo-cli schema docs.scene.get
octo-cli schema docs.scene.edit
octo-cli schema docs.members.set
octo-cli schema docs.comments.add
octo-cli schema docs.attachments.presign
```
