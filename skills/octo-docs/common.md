# octo-docs — Common features (comments, versions, members, attachments)

Read this for capabilities that apply across all doc kinds. Jump to the section
you need:

- [Comments](#comments) — add/list/reply/resolve, on a doc text range or a sheet cell
- [Versions](#versions) — snapshot / preview / rename / delete / restore
- [Members & sharing](#members--sharing) — roles, forward-grant
- [Attachments](#attachments) — presign + upload file/image bytes

All commands call `$OCTO_API_BASE_URL/v1/bot/docs/*`. Auth & space rules are in
`SKILL.md`.

---

## Comments

Comments live out-of-band from the body and are anchored to a text range (docs)
or a cell (sheets).

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
backend never parses base64 anchors. A bot with no live editor selection can
still start an anchored thread with `--anchorText`: the backend locates that text
in the stored document and computes the anchor. When the text occurs more than
once the request fails loudly with `422 ambiguous_anchor` (never a silent guess)
— narrow it with `--blockPath` and/or `--occurrence`. Text that is not found
returns `422 anchor_text_not_found`.

### Commenting on a spreadsheet cell (doc_type 'sheet')

A `sheet` has no rich-text body, so `--anchorText` does NOT work on it (returns
`409 unsupported_doc_type` — anchor-text resolution needs a ProseMirror fragment).
Instead a sheet comment anchors to a **cell**: pass the base64 of the cell key
`${sheetId}!${row}:${col}` as BOTH `--anchorStart` and `--anchorEnd`, and put the
human-readable A1 label in `--anchorText` (display only, not resolved). `row`/`col`
are 0-based; `sheetId` is the logical sheet id (`default` for a single-sheet doc).

```bash
# Comment on cell A1 (row 0, col 0) of the default sheet.
CELL=$(printf 'default!0:0' | base64)          # -> ZGVmYXVsdCEwOjA=
octo-cli docs comments add <sheetDocId> \
  --body "This total looks off" \
  --anchorStart "$CELL" --anchorEnd "$CELL" --anchorText "A1"
```

List / reply / resolve / delete work identically to document comments; the badge
a reader sees on the cell is driven by this anchor. Schema: `octo-cli schema docs.comments.add`.

---

## Versions

Snapshot / preview / restore, for any doc kind (document / sheet / board).

```bash
octo-cli docs versions list    <docId> [--kind manual|auto|all] [--cursor <id>] [--limit <n>]
octo-cli docs versions create  <docId> [--label "before restructure"]   # writer
octo-cli docs versions state   <docId> <versionId>   # read-only decoded preview (reader)
octo-cli docs versions rename  <docId> <versionId> --label "v2"          # writer
octo-cli docs versions delete  <docId> <versionId>                       # admin
octo-cli docs versions restore <docId> <versionId>                       # admin
```

> **`<versionId>` is the version's `docVersionSeq`** — the integer returned by
> `docs versions create` and shown as `docVersionSeq` in `docs versions list`.
> There is no separate id field; pass that integer.

`versions state` reads *historical* content: a past snapshot decoded by the doc's
kind — a document/sheet as `{kind:"document", doc, sheetCells, sheetDims, ...}`, a
board as `{kind:"board", scene, ...}`. It is a preview, not the live document, and
is not writable. To read the **live** content, use `docs content get` (`doc.md`),
`docs sheet get` (`sheet.md`), or `docs scene get` (`board.md`).

`restore` is non-destructive and records a safety snapshot first, so it is itself
undoable. For a sheet, restore rolls back the full grid — cells, column-width /
row-height dims, AND floating images — to the target version.
Schema: `octo-cli schema docs.versions.restore`.

---

## Members & sharing

```bash
octo-cli docs members list   <docId>                             # admin; items include principalSpaceId
octo-cli docs members set    <docId> --uid <uid> --role writer   # authenticated Bot Space; role: reader|commenter|writer|admin
octo-cli docs members set    <docId> --uid <uid> --role writer --principal-space-id <spaceId>
octo-cli docs members remove <docId> <uid> --principal-space-id <spaceId> # required; owner cannot be removed

# Grant-only forward access (never downgrades). Only reader|writer are grantable.
octo-cli docs forward-grant  <docId> --uid <uid> --role reader

# Space-level share scope — read/set who in the space can reach the doc.
octo-cli docs share get <docId>                                       # reader; {docId, shareScope, shareRole}
octo-cli docs share set <docId> --scope restricted                    # admin; only owner/members can access
octo-cli docs share set <docId> --scope anyone_in_space --role read   # admin; every space member gets read
octo-cli docs share set <docId> --scope anyone_in_space --role edit   # admin; every space member gets edit
```

`docs members set` is a Space-qualified Bot mutation. Omit
`--principal-space-id` to use the authenticated Bot Space, or pass that same
Space explicitly; either form can add a member or update an existing role. A
foreign Space is never queried through the directory and can only update an
existing exact `(docId, uid, principalSpaceId)` row; if that row is absent, the
backend returns `404`. A uid can have distinct member rows in multiple Spaces,
so `docs members remove` always requires `--principal-space-id` and deletes the
exact qualified row. Schemas:
`octo-cli schema docs.members.set` and `octo-cli schema docs.members.remove`.

`docs share set` changes the space-level share scope. Two scopes: `restricted`
(only the owner and explicit members reach the doc — the default) and
`anyone_in_space` (every member of the doc's own space is granted the share
role). The `--role` (`read` | `edit`) is **required** with `--scope
anyone_in_space` and is **ignored** with `--scope restricted` (the backend
normalizes it to `read`), so omit `--role` when restricting. Sharing only ever
*raises* access — an owner or member is never downgraded by a share setting.
Error codes: `400 invalid_scope` (unknown scope), `400 invalid_role`
(anyone_in_space with a missing/invalid role), `403` (caller not admin/owner),
`404` (missing or cross-space doc), `409` (archived doc). Schema:
`octo-cli schema docs.share.set`.

---

## Attachments

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
> presigned object-store URL. Do the PUT with `curl` (or any HTTP client).

Once uploaded, reference the `attachId` from a document body's image node (see
`doc.md`) or a whiteboard file ref (see `board.md`). NOTE: a spreadsheet's floating
images do NOT use this attachment flow — they inline the base64 bytes in the
drawing's `source` field (see `sheet.md`). Schema: `octo-cli schema docs.attachments.presign`.
