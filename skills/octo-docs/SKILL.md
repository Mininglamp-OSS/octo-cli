---
name: octo-docs
version: 0.2.0
description: Docs domain — create and govern documents, read and incrementally edit a doc's live body, read and batch-edit spreadsheet cells (dims + images), read and batch-edit whiteboard scenes, members and sharing, inline comments, versions/snapshots, and attachment metadata as a bot. Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-docs — bot access to Octo documents, spreadsheets & whiteboards

This skill is **progressive**: this file covers the shared essentials (auth,
document lifecycle) and routes you to a focused reference file for each surface.
**Load the one reference that matches your task — don't read them all.** The
reference files sit next to this file in the skill directory.

All commands call `$OCTO_API_BASE_URL/v1/bot/docs/*`.

## When to read which reference

| Your task | Read |
|---|---|
| Read/edit a **spreadsheet** (`doc_type: sheet`): cells, formulas, styles, column widths / row heights (dims), floating **images**, paged reads, xlsx export | **`sheet.md`** |
| Read/edit a rich-text **document body** (`doc_type: doc`): incremental block ops | **`doc.md`** |
| Read/edit a **whiteboard** (`doc_type: board`): scene elements/files, image export | **`board.md`** |
| Cross-cutting features — **comments** (doc range or sheet cell), **versions** (snapshot/restore), **members & sharing**, **attachments** (presign/upload) | **`common.md`** |

> The first three split by `doc_type` (what kind of document you're editing);
> `common.md` holds the features that apply to any kind. Read a reference with
> your file tool (it sits beside this SKILL.md, e.g. `sheet.md`), or reprint the
> whole skill set anytime with `octo-cli skills octo-docs`.

Pick by `doc_type`: a **doc** body → `doc.md`; a **sheet** → `sheet.md`; a
**board** → `board.md`. Using the wrong surface returns `409 unsupported_doc_type`.
`docs get <docId>` reports the `doc_type` and your role.

## Auth & space

- Authenticate with a bot token via a stored profile (`--profile` / `--bot-id`)
  or `OCTO_BOT_TOKEN`; both `app_*` and `bf_*` tokens work. Confirm with
  `octo-cli config show`.
- **Do not pass a space flag for docs.** The bot mount resolves the space
  server-side from the token and deliberately ignores any client-supplied space
  header (anti-spoof). Role enforcement (reader / writer / admin) also happens
  server-side, so the CLI surfaces the backend's `403`/`404` envelopes unchanged.

## Document lifecycle

```bash
# Create an empty doc (caller becomes owner/admin). A new doc has NO body —
# seed a `doc` with `docs content edit` (doc.md), a `sheet` with
# `docs sheet edit` (sheet.md), a `board` with `docs scene edit` (board.md).
octo-cli docs create [--title "Runbook"] [--folderId f_123] [--docType doc|sheet|board]

# List docs you own or are a member of. Page-based (see the pagination note below).
octo-cli docs list [--folderId f_123] [--page 1] [--pageSize 20] [--sort updatedAt:desc]

octo-cli docs get    <docId>                 # metadata + doc_type + your role
octo-cli docs rename <docId> --title "New title"
octo-cli docs delete <docId>                 # soft delete (admin)
```

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
incremental block ops (`doc.md`), `doc_type: sheet` cell/dims/drawings batches
(`sheet.md`), and `doc_type: board` scene batches (`board.md`); the document
outline is not editable through the CLI.

## Schema lookup

Any operation's parameters + response schema come from the embedded registry:

```bash
octo-cli schema docs.create
octo-cli schema docs.content.edit     # + docs.sheet.edit / docs.scene.edit / docs.comments.add / …
```
