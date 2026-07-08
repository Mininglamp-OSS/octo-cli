---
name: octo-docs
version: 0.1.0
description: Docs domain — create and govern documents, members and sharing, inline comments, versions/snapshots, and attachment metadata as a bot. Live body/outline editing is NOT available over the CLI. Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-docs — documents, members, comments, versions, attachments

> **Live document content editing is NOT available from the CLI.** A document's
> body and outline live in a Yjs (Hocuspocus) websocket session, not in any REST
> endpoint. This skill drives everything *around* the content — metadata,
> members, sharing, comments, versions, and attachment references — but it cannot
> read or write the live body. Creating a doc makes an **empty** document; you
> cannot seed or edit its text here. To inspect content, take a snapshot
> (`docs versions create`) and read it back with `docs versions state`, which
> returns a **read-only** decoded preview of that snapshot.

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
# Create an empty doc (caller becomes owner/admin). Body content is NOT set here.
octo-cli docs create [--title "Runbook"] [--folderId f_123] [--docType doc]

# List docs you own or are a member of. Page-based (see the pagination note below).
octo-cli docs list [--folderId f_123] [--page 1] [--pageSize 20] [--sort updatedAt:desc]

octo-cli docs get    <docId>                 # metadata + your role
octo-cli docs rename <docId> --title "New title"
octo-cli docs delete <docId>                 # soft delete (admin)
```

## 2. Members & sharing

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

## 3. Comments

Comments are anchored to a text range and live out-of-band from the body.

```bash
# List thread roots + replies. --includeResolved 1 also returns resolved threads.
octo-cli docs comments list <docId> [--includeResolved 1] [--cursor <id>] [--limit 50]

# Root comment: requires both anchors (opaque base64 Yjs positions).
octo-cli docs comments add <docId> \
  --body "Please clarify this" \
  --anchorStart <base64> --anchorEnd <base64> [--anchorText "the quoted span"]

# Reply to a thread root: set --parentId, omit anchors.
octo-cli docs comments add <docId> --body "Agreed" --parentId <rootId>

# Edit your own comment's text, OR resolve/reopen a thread root (pass one).
octo-cli docs comments edit   <docId> <id> --body "edited text"
octo-cli docs comments edit   <docId> <id> --resolved true      # writer; false reopens

octo-cli docs comments delete <docId> <id>          # soft delete (author)
octo-cli docs comments delete <docId> <id> --hard 1 # hard delete (admin)
```

Anchors are opaque base64-encoded Yjs positions produced by the editor — the
backend never parses them. A bot that does not have live positions typically only
posts replies (`--parentId`) or resolves threads.

## 4. Versions (snapshots)

```bash
octo-cli docs versions list    <docId> [--kind manual|auto|all] [--cursor <id>] [--limit <n>]
octo-cli docs versions create  <docId> [--label "before restructure"]   # writer
octo-cli docs versions state   <docId> <versionId>   # read-only decoded preview (reader)
octo-cli docs versions rename  <docId> <versionId> --label "v2"          # writer
octo-cli docs versions delete  <docId> <versionId>                       # admin
octo-cli docs versions restore <docId> <versionId>                       # admin
```

`versions state` is the sanctioned way to read historical content: it returns the
snapshot decoded to ProseMirror JSON (`{doc, sheetCells, sheetDims, ...}`). It is a
preview of a *past* version, not the live document, and is not writable. `restore`
is non-destructive and records a safety snapshot first, so it is itself undoable.

## 5. Attachments (metadata only)

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

## Pagination note

The docs list endpoints do **not** use the shared `{data, pagination}` envelope,
so `--page-all` is intentionally not offered on them:

- `docs list` is **page-based** — response is `{total, items}`. Walk it with
  `--page` / `--pageSize`.
- `docs comments list` and `docs versions list` are **cursor-based** — response is
  `{items, nextCursor}`. Pass the returned `nextCursor` back via `--cursor` to get
  the next page; stop when `nextCursor` is null.

## Not in this version

`docs attachments upload` (binary helper), invites, access-requests, link-card,
and any live-content/body editing are out of scope here.

## Schema lookup

```bash
octo-cli schema docs.create
octo-cli schema docs.members.set
octo-cli schema docs.comments.add
octo-cli schema docs.attachments.presign
```
