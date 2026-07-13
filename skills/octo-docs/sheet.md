# octo-docs — Spreadsheet cells, dims & images (`doc_type: sheet`)

Read this when the target is a **spreadsheet** (`doc_type: sheet`) and you need to
read or edit its cells, column widths / row heights, floating images, or export it.
All commands call `$OCTO_API_BASE_URL/v1/bot/docs/*`. Auth & space rules are in
`SKILL.md`.

A spreadsheet stores a flat cell map on the Y.Doc, keyed `sheetId!row:col` (e.g.
`default!0:0`), with values `{v,f,s}` — `v` a string/number/boolean/null, `f` an
optional formula, `s` an opaque resolved style object. (Cells authored in the web
may also carry Univer's `p` rich-text snapshot and `t` cell-type; both round-trip
untouched.) Same read-token-then-guarded-write discipline as the body surface.

```bash
# Read the LIVE cells + dims + baseVersion (reader). Response:
#   { docId, sheetCells: { "sheetId!row:col": {v,f,s} }, sheetDims: { "c<idx>|r<idx>": px }, baseVersion }
octo-cli docs sheet get <docId>

# Batch-edit cells (writer). --base-version is REQUIRED and is sent as the
# If-Match header; the cells batch goes through --data as a JSON object.
# A cell value of null DELETES that cell.
octo-cli docs sheet edit <docId> --base-version "<token>" \
  --data '{"cells":{"default!0:0":{"v":"hi"},"default!1:0":null}}'

# Set column widths / row heights via the optional `dims` batch, keyed
# `c<idx>` (column) or `r<idx>` (row) -> pixels; a null value deletes a dim.
# Provide cells, dims, or drawings (at least one non-empty). These are the same
# `sheetDims` values `docs sheet get` returns.
octo-cli docs sheet edit <docId> --base-version "<token>" \
  --data '{"dims":{"c0":200,"r3":40,"c5":null}}'

# Insert / remove a FLOATING IMAGE via the optional `drawings` batch, keyed
# `${sheetId}!${drawingId}`. The value is a serialized Univer ISheetImage with
# the bytes inline as a base64 data URL in `source`; `drawingId` MUST equal the
# key's id. A null value deletes that image. `transform` is the pixel box;
# `sheetTransform` anchors it to a cell range (from/to). NOT returned by
# `docs sheet get` (the read surface is cells+dims only).
octo-cli docs sheet edit <docId> --base-version "<token>" --data '{
  "drawings": { "default!img1": {
    "drawingId": "img1", "drawingType": 0, "imageSourceType": "BASE64",
    "source": "data:image/png;base64,iVBORw0KGgo...",
    "transform": {"left":100,"top":100,"width":80,"height":80,"angle":0,"flipX":false,"flipY":false,"skewX":0,"skewY":0},
    "sheetTransform": {"from":{"column":1,"row":1,"columnOffset":0,"rowOffset":0},"to":{"column":2,"row":3,"columnOffset":0,"rowOffset":0}},
    "axisAlignSheetTransform": {"from":{"column":1,"row":1,"columnOffset":0,"rowOffset":0},"to":{"column":2,"row":3,"columnOffset":0,"rowOffset":0},"angle":0,"flipX":false,"flipY":false,"skewX":0,"skewY":0}
  } }
}'
```

## Reading a large sheet in pages

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
`422 sheet_cell_invalid` (a cell/dim/drawing violates its contract or key shape).

## Exporting a sheet to Excel (.xlsx)

There is no server-side sheet export endpoint. A bot exports by **reading the
sheet and serializing it locally**: `docs sheet get` returns everything needed —
`sheetCells` (`{v,f,s}` per cell) and `sheetDims` (column widths / row heights) —
so feed those into whatever spreadsheet library the bot's runtime has (e.g.
`xlsx-js-style` in Node, `openpyxl` in Python): map `default!r:c` → row r / col c,
write `v` (or `f` as a formula), apply `s` as the cell style, and set widths from
`c<idx>` / heights from `r<idx>`. For a large grid, page the read with
`--limit`/`--cursor` and stream rows into the workbook. (Note: merged-cell ranges
are not exposed through the bot read surface yet, and floating images are
write-only, so an exported workbook carries values + styles + dimensions but not
merges/images.)

## Commenting on a cell

A sheet cell comment anchors to the cell key, not a text range — see
`common.md` (Comments → "Commenting on a spreadsheet cell").

## Schema lookup

```bash
octo-cli schema docs.sheet.get
octo-cli schema docs.sheet.edit
```
