# octo-docs — Document body (`doc_type: doc`)

Read this when the target is a **rich-text document** (`doc_type: doc`) and you
need to read or edit its live body. Only `doc_type: doc` bodies are editable this
way; a **spreadsheet** (`sheet`) uses `sheet.md` and a **whiteboard** (`board`)
uses `board.md` — both return `409 unsupported_doc_type` from `content`.

Read the live body and its base-version token, then apply a batch of incremental
block ops guarded by that token. A newly-created doc is **empty** — seed its text
with a first `docs content edit`.

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

## Worked example: read, then append a paragraph

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
board/whiteboard/sheet), `413 too_many_ops` / `413 op_content_too_large` /
`413 doc_too_large` (size caps), `400 path_too_deep` or `400 invalid_body`
(missing base version or malformed shape), and `422` for a bad anchor
(`anchor_not_found` / `anchor_mismatch`), invalid ops (`invalid_ops`), an
attachment that is not this doc's (`attachment_not_found`), or content the
schema rejects (`schema_incompatible`).

Inserting an image into the body references an uploaded attachment — see
`common.md` (Attachments) for the presign → upload → reference flow.

## Schema lookup

```bash
octo-cli schema docs.content.get
octo-cli schema docs.content.edit
```
