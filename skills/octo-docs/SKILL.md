---
name: octo-docs
version: 0.2.0
description: Docs domain — create and govern documents, read and incrementally edit a doc's live body, read and batch-edit spreadsheets including cells, layout, shared filters, sorting, freeze panes, and validation/dropdowns, read and batch-edit whiteboard scenes, members and sharing, inline comments, versions/snapshots, and attachment metadata as a bot. Load after octo-shared.
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
| Read/edit a **spreadsheet** (`doc_type: sheet`): cells, formulas, styles, layout, floating **images**, freeze panes, shared filters, sorting, data validation/dropdowns, paged reads, xlsx export | **`sheet.md`** |
| Read/edit a rich-text **document body** (`doc_type: doc`): incremental block ops | **`doc.md`** |
| Read/edit a **whiteboard** (`doc_type: board`): scene elements/files, image export | **`board.md`** |
| Continue from a searchable **HTML document** (`doc_type: html`): resolve its document reference, then use immutable versions/drafts/assets/comments | **`../octo-html/SKILL.md`** |
| Cross-cutting features — **comments** (doc range or sheet cell), **versions** (snapshot/restore), **members & sharing**, **attachments** (presign/upload and external-image ingest) | **`common.md`** |

> The first four split by `doc_type` (what kind of document you're handling);
> `common.md` holds the features that apply across kinds. Read a reference with
> your file tool (it sits beside this SKILL.md, e.g. `sheet.md`), or reprint the
> whole skill set anytime with `octo-cli skills octo-docs`.

Pick by `doc_type`: a **doc** body → `doc.md`; a **sheet** → `sheet.md`; a
**board** → `board.md`; an **html** result → the separate `octo-html` skill.
Using the wrong surface returns `409 unsupported_doc_type`. For an HTML search
result, run `docs get <docId>` and use its `octoDocSlug` value as the document reference
with `html get <octoDocSlug>` (or another `html` command). Do not retry HTML through `docs content`,
`docs sheet`, or `docs scene`.
`docs get <docId>` reports the `doc_type`, your role, and `octoDocSlug` for HTML.
That value is the canonical `doc_id` for new documents and the retained legacy
slug for old documents; callers do not infer the distinction from mount state.

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

# Full-text search every doc the bot may read. Repeat --doc-type to combine kinds.
# Search is cursor-based; --page-all follows nextCursor automatically.
octo-cli docs search --keyword "quarterly plan" [--doc-type doc|sheet|board|html] [--page-size 20] [--page-all]

octo-cli docs get    <docId>                 # metadata + doc_type + your role

# Import a local file into an existing target. .md/.markdown/.docx require a doc;
# .xlsx requires a sheet and imports its first visible worksheet.
octo-cli docs import <docId> --file ./input.md

# Export to a local file. -o is required and its extension must match.
# --export-format is distinct from global --format (the envelope renderer).
octo-cli docs export <docId> --export-format pdf -o ./output.pdf
# Other accepted matching pairs: md/.md, docx/.docx, xlsx/.xlsx, png/.png, svg/.svg

octo-cli docs rename <docId> --title "New title"
octo-cli docs delete <docId>                 # soft delete (admin)
```

## Pagination note

Pagination depends on the endpoint's response contract:

- `docs list` is **page-based** — response is `{total, items}`. Walk it with
  `--page` / `--pageSize`; `--page-all` is not offered.
- `docs search` is **cursor-based** — response is `{total, items, nextCursor}`.
  Pass `nextCursor` back via `--cursor`, or use `--page-all` to follow it
  automatically; `--page-limit` caps automatic requests (default 10).
- `docs comments list` and `docs versions list` are **cursor-based** — response is
  `{items, nextCursor}`. Pass the returned `nextCursor` back via `--cursor` to get
  the next page; stop when `nextCursor` is null.

## Not in this version

`docs attachments upload` (binary helper), invites, access-requests, and
link-card are out of scope here. Body editing is limited to `doc_type: doc`
incremental block ops (`doc.md`), the spreadsheet batches documented in
`sheet.md`, and `doc_type: board` scene batches (`board.md`). `doc_type: html`
belongs to the separate `html` domain, uses its returned document reference, and
is published as
immutable versions; it cannot be read or edited through these three body
surfaces. The document outline is not editable through the CLI.

## Schema lookup

Any operation's parameters + response schema come from the embedded registry:

```bash
octo-cli schema docs.create
octo-cli schema docs.search
octo-cli schema docs.content.edit     # + docs.sheet.edit / docs.scene.edit / docs.comments.add / …
```
