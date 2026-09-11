# octo-docs — Presentations (`doc_type: html_ppt`)

Read this when the target is an **Octo PPT** and you need to create a
presentation, edit slides, work with comments or versions, or export HTML.
Auth and Space rules are in `SKILL.md`; document metadata, members and sharing
use the commands in `SKILL.md` and `common.md`.

Read the current presentation and its revision, apply the requested changes,
then submit and read back. Use the commands below for PPT content, comments,
versions and export.

The backend enforces PPT-specific requirements on shared commands: replay keys,
revision checks, and root-anchor versus reply exclusivity. These commands also
serve ordinary documents, sheets and boards; the CLI does not infer the document
type from its ID or impose PPT anchor requirements on those types.

Local validation is deliberately narrower than backend validation. `docs ppt edit`
checks `baseRevision` as an integer in 0–9007199254740991, including `--data`.
Shared version `baseRevision`, comment `revision`, anchor coordinates and IDs,
and path/query numeric bounds are checked by the backend. A local dry run does
not prove these values or permissions are valid. Supply the fresh revisions
returned by the relevant read commands, not a value inferred from CLI acceptance.

## Create a presentation

Choose a template and provide a title:

```bash
octo-cli docs create --docType html_ppt --title "Quarterly Review" --templateId report --idempotency-key <unique-key>
```

Templates: `blank`, `pitch`, `report`, `lesson`. Save the returned `docId` for
subsequent commands; `editorUrl` opens the editor and `shareUrl` opens the shared
presentation subject to its access rules. The Bot becomes the owner, and its
human owner also receives admin access. Identity and Space come from the Bot's
credential. Retry a timeout with the identical idempotency key and body to avoid
creating a second presentation.

## Read, modify, submit, read back

```bash
octo-cli docs get <docId>
octo-cli docs ppt get <docId>
octo-cli schema docs.ppt.edit
octo-cli docs ppt edit <docId> --data @edit.json
octo-cli docs ppt get <docId>
```

`get` returns `.data.deck` and `.data.baseRevision`. `edit.json` must contain
`{"baseRevision":7,"deck":{...the complete edited deck...}}`, with only those two
top-level fields. Preserve `deck.docId`, unknown fields inside `deck`, and stable
slide/element IDs, and the returned `format` / `version` fields. Do not copy the
read response's top-level `docId` or `contentHash` into the edit request.
`contentHash` is advisory and may be absent or empty; concurrency uses revisions. Other
online editors receive the submitted changes.

On `409 CONFLICT`, read again and reapply only the intended changes to the new
deck. Never replace the new revision number on an old deck and resubmit it.
After a timeout, read back first: the edit may have committed before the reply
was lost. No-op edits do not advance the revision.

## Permissions

- Reader: read and view. Cannot edit slides or add comments.
- Commenter: reader capabilities plus the permissioned comment workflow.
- Writer: edit slide content and create versions. Cannot change members, restore
  versions, or rename the document.
- Admin: writer capabilities plus document/member management and version restore.

The Bot has its OWN document grant. Its owner's access does not implicitly grant
the Bot access. Ask an admin to add the Bot as a member. Do not self-grant, use a
Human session, substitute another Bot profile, or spoof `X-Space-Id`. The server
resolves the Bot's current Space from its credential. Check `.identity` in CLI
output. Body `collab`, `readonly` and `template` are not permission controls and
must not be supplied on edit. Never embed credentials in slides or assets.

## Create and restore versions

Content edits are saved by `docs ppt edit`; they do not need a separate save or publish command.
`docs versions create` creates a fixed version of the current presentation. It does not make the document public or change
member permissions. Use it when the user requests a saved version.

```bash
octo-cli docs versions list <docId>
octo-cli docs versions state <docId> <versionSeq>
octo-cli docs ppt get <docId>
octo-cli docs versions create <docId> --baseRevision 7 --label "Release" --idempotency-key <unique-key>
```

Use the actual fresh revision, not the example `7`. Lists return `nextCursor`;
pass it as `--cursor` for older pages. All four roles can read fixed versions.

Restore is admin-only and changes the live deck. Read the current deck first,
and check that the version matches the requested restore target:

```bash
octo-cli docs versions restore <docId> 3 --baseRevision 8 --idempotency-key <unique-key>
```

The receipt includes `newDocVersionSeq`, preserving pre-restore work, and
`restoredFrom`, identifying the restored version.
PPT versions are immutable and cannot be deleted; other document types retain their existing version-delete behavior.

For version creation/restore, retry a timeout with the **identical payload and key**.
Do not fetch a new base revision and reuse it in a retry. After an explicit
revision conflict, reread and decide whether a new operation is still intended;
use a new key for that new operation.

## Export HTML

```bash
octo-cli docs ppt export <docId> --file-format html --output slides.html
```

Export uses the current presentation; creating a version first is not
required. All four roles can export. `--output` saves the actual file; without
it the CLI prints only response metadata. HTML is a self-contained offline
player, not PDF or editable PPTX. It includes embedded assets only and cannot
load remote URLs.

## Slide comments and replies

Comments can target the whole presentation, a slide, an element or a position.
Use `anchor` for a new thread and `parentId` to reply to an existing thread.

```bash
octo-cli docs comments list <docId>
octo-cli docs comments replies <docId> <rootId> --page-all
octo-cli docs comments get <docId> <commentId>
octo-cli docs ppt get <docId>
octo-cli docs comments add <docId> --idempotency-key <unique-key> --data @comment.json
octo-cli docs comments add <docId> --parentId <rootId> --body "Done" --idempotency-key <unique-key>
octo-cli docs comments edit <docId> <rootId> --resolved=true --revision 1
```

A live root's `comment.json` is
`{"body":"Review this slide","anchor":{"kind":"slide","slideId":"<actual-slide-id>","versionSeq":null,"baseRevision":7}}`.
The target can be `{ "kind": "document" }` for the entire PPT,
`{ "kind": "slide", "slideId": "..." }`,
`{ "kind": "element", "slideId": "...", "elementId": "..." }`, or
`{ "kind": "point", "slideId": "...", "x": 0.25, "y": 0.75 }`.
Point coordinates are normalized to the slide (0 through 1), independent of zoom.
Element IDs must belong to the specified slide in the authoritative source.
Use the actual fresh baseRevision and stable IDs from get. For a fixed
version, use its positive `versionSeq` and omit `baseRevision`. Replies have
only body and parentId; no anchors, and no nested replies. Resolved/deleted
roots reject new replies. Lists omit resolved roots by default; use --includeResolved 1 to include them.
Replies have separate pagination.

Comment idempotency keys are 1–128 printable ASCII characters without spaces;
creation and version actions allow 1–255 characters. The CLI rejects an explicitly
blank key; the backend enforces length and character bounds. Omitting the key
remains possible for other document types but is rejected for PPT writes.
Retry an ambiguous create with the identical key AND body. A 409 may indicate a pending request or a stale anchor. Keep the original key
for a pending request; refresh the target before starting a new operation.
Comment edits require the current row's `revision`, not the deck baseRevision.
Writer authors may edit with `--body "..."`; writer+ may resolve
or reopen (`--resolved=false`). Commenter authors may soft-delete with
`docs comments delete <docId> <commentId> --revision <revision>`. Admins may hard-delete another author's comment/thread with
`docs comments delete <docId> <commentId> --revision <revision> --hard 1`. Never treat an identical uid in a different
Space as the author. On an uncertain edit/delete result, read back first.

### Modify a presentation from a comment

1. Read the comment with `docs comments get`. The response contains
   `comment` and `root`; use `root.anchor` to locate the target, including when
   the request is a reply.
2. Read the live presentation and its current `baseRevision`. Match slides and
   elements by their IDs, not their displayed page numbers. A fixed-version
   anchor refers to historical content; only edit the current presentation when
   that is the user's intended target.
3. Apply the requested change, preserving other elements and styles. Submit
   with `docs ppt edit` and read back the affected content before claiming success.
4. Reply in the original root thread. When the comment task runtime posts the
   final reply, return the result to that runtime instead of adding a duplicate
   comment or sending an IM message. For a direct CLI reply, use `parentId` and
   a stable idempotency key.

Creating or replying to a comment does not itself modify the presentation.
A resolved comment or an increased revision alone does not prove that the
requested edit was made. Verify the actual target content.

## Slide and element fields

Read the existing `size` and `theme`; 1280 x 720 is common, not guaranteed. A
slide needs stable `id`, `elements`, `background`, `transition` and `notes`.
Elements share `id`, `type`, `x`, `y`, `w`, `h`, `rotation`, `opacity`.
Text uses `html`, `fontSize`, `fontFamily`, `fontWeight`, `color`, `align`,
`valign`, `lineHeight`. Do not use the legacy `text` field.
Code uses **`content`**, not `code`, `text`, or `html`. Example:

```json
{"id":"code_1","type":"code","x":100,"y":180,"w":1000,"h":360,"rotation":0,"opacity":1,"content":"const total = 30 + 40;\nconsole.log(total);","grammarName":"js","fontSize":32,"align":"left","valign":"middle","lineHeight":1.25}
```

Use `js`, `ts`, `py` and the language IDs shown by the editor. Read back `content`
explicitly; an accepted additive field such as `code` does not make visible code.
The UI edits this content inline on the canvas with properties in the right panel.

Use the same element ID on adjacent slides and `transition: "morph"` for an
intentional transition. IDs must be unique within a slide, not across all
slides. Use real chart/table elements for data. Bar/line chart series take plain
numbers. Preserve the existing chart/table structure when editing data.
Keep assets embedded as data URIs (`deck.assets.hero`, element
`src: "asset:hero"`); external URLs do not load in presentations or exported HTML.
Keep writes small. Split large edits using a fresh get between batches.

Inspect every affected slide in the actual Octo editor after readback. JSON
validation cannot prove text fits, charts are legible, images load, or animations
look right. Test refresh and a second editor as well. Report any unverified
visual behavior; do not call a deck finished solely because an HTTP call passed.

## Schema lookup

```bash
octo-cli schema docs.create
octo-cli schema docs.ppt.get
octo-cli schema docs.ppt.edit
octo-cli schema docs.comments.add
octo-cli schema docs.versions.create
octo-cli schema docs.versions.restore
octo-cli schema docs.ppt.export
```
