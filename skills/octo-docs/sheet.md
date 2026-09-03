# octo-docs — Spreadsheet cells, layout & rules (`doc_type: sheet`)

Read this when the target is a **spreadsheet** (`doc_type: sheet`) and you need to
read or edit its cells, column widths / row heights, floating images, hyperlinks,
merged ranges, sheet tabs, freeze panes, shared filters, sorting, data
validation/dropdowns, or export it.
All commands call `$OCTO_API_BASE_URL/v1/bot/docs/*`. Auth & space rules are in
`SKILL.md`.

Export a sheet to XLSX with a required, matching destination extension:

```bash
octo-cli docs export <docId> --export-format xlsx -o spreadsheet.xlsx
```

A spreadsheet stores a flat cell map on the Y.Doc, keyed `sheetId!row:col` (e.g.
`default!0:0`), with values `{v,f,s,p,t}` — `v` a string/number/boolean/null,
`f` an optional formula, `s` an opaque resolved style object, `p` Univer's
rich-text snapshot, and `t` the cell type. All five fields round-trip untouched.
Same read-token-then-guarded-write discipline as the body surface.

```bash
# Read the LIVE cells + dims + hyperlinks + merges + sheet tabs + freeze +
# filters + validation/dropdown rules + baseVersion
# (reader). Response:
#   { docId, sheetCells: { "sheetId!row:col": {v,f,s,p,t} },
#     sheetDims: { "${logicalId}:c<idx>|${logicalId}:r<idx>|c<idx>|r<idx>": px },
#     sheetHyperLinks: { "sheetId!linkId": {id,row,column,payload,display?} },
#     sheetMerges: { "logicalId:sr:sc:er:ec": true },
#     sheetList: { "logicalId": {name,order} },
#     sheetFreeze: { "logicalId": {startRow,startColumn,xSplit,ySplit} },
#     sheetFilters: { "logicalId": {ref,filterColumns?:[{colId,filters}]} },
#     sheetDataValidations: { "logicalId": [rules of every validation type] },
#     baseVersion }
octo-cli docs sheet get <docId>

# Batch-edit cells (writer). --base-version is REQUIRED and is sent as the
# If-Match header; the cells batch goes through --data as a JSON object.
# A cell value of null DELETES that cell.
octo-cli docs sheet edit <docId> --base-version "<token>" \
  --data '{"cells":{"default!0:0":{"v":"hi"},"default!1:0":null}}'
```

### Freeze panes

Freeze rows and columns with one compact record per worksheet. Coordinates are
0-based. `xSplit` is the number of frozen columns and `ySplit` the number of
frozen rows. `startColumn` / `startRow` are the first scrollable column / row, so
freezing the first N columns / rows normally uses N for both the start and split
on that axis. Use `-1` as the `start*` value for an unfrozen axis. The server
also accepts `0` on an unfrozen axis but normalizes it to `-1` on readback.

```bash
# Freeze the header row and first column. Read a fresh baseVersion first.
octo-cli docs sheet edit <docId> --base-version "<token>" --data '{
  "freeze":{"default":{"startRow":1,"startColumn":1,"xSplit":1,"ySplit":1}}
}'

# Freeze only the header row (column axis remains unfrozen).
octo-cli docs sheet edit <docId> --base-version "<token>" \
  --data '{"freeze":{"default":{"startRow":1,"startColumn":-1,"xSplit":0,"ySplit":1}}}'

# Freeze only the first column (row axis remains unfrozen).
octo-cli docs sheet edit <docId> --base-version "<token>" \
  --data '{"freeze":{"default":{"startRow":-1,"startColumn":1,"xSplit":1,"ySplit":0}}}'

# Remove all frozen panes from the default sheet.
octo-cli docs sheet edit <docId> --base-version "<token>" \
  --data '{"freeze":{"default":null}}'
```

### Shared filter and sorting

A filter has a 0-based rectangular `ref`. Its first row is the header row and
is intentionally kept visible; criteria apply to the data rows below it. The
example below keeps rows whose column B raw value is `待处理`. This is a value
filter, not a font-color filter—cell font color does not decide whether a row
matches. `colId` is the absolute 0-based worksheet column, not an offset from
`ref.startColumn`. Filter state is shared, so collaborators see the same hidden
rows. If you send `filterColumns:[]`, the server keeps the filter range but omits
`filterColumns` from readback.

```bash
octo-cli docs sheet edit <docId> --base-version "<token>" --data '{
  "filters":{"default":{
    "ref":{"startRow":0,"startColumn":0,"endRow":100,"endColumn":1},
    "filterColumns":[{"colId":1,"filters":{"filters":["待处理"]}}]
  }}
}'

# Remove the filter range and its criteria.
octo-cli docs sheet edit <docId> --base-version "<token>" \
  --data '{"filters":{"default":null}}'
```

Sorting is different: there is no durable `sort` rule. It is a raw coordinate
rewrite, not a spreadsheet-native sort. Read the current cells, move the complete
selected row block, and carry every cell's complete `{v,f,s,p,t}` shape to its
destination in one guarded edit. Use `null` for destinations that become empty;
omitting them leaves old cells in place. The server stores `f` verbatim and does
not re-anchor relative formula references. Rewrite relative references for each
destination, or do not sort that range through this API. Never sort only the key
column; move every cell in each selected row so values, styles, rich text, and
cell types stay together.

The same raw-coordinate rule applies to any state keyed by or containing a
coordinate: row `dims`, `sheetDataValidations` ranges, `sheetHyperLinks`,
`sheetMerges`, drawings, and cell comments all stay at their old coordinates.
Freeze panes and filter ranges/columns also remain fixed, which is normally the
intended behavior. Remap affected row dims, validation ranges, hyperlink anchors,
and merge keys in the same atomic edit; because `dataValidations` is replace-
style, resend every rule you need to keep. `sheetDims` is one workbook-level map
whose keys may be sheet-qualified. When sorting a non-default tab, remap that
tab's `${logicalId}:r<idx>` keys; bare `r<idx>` keys address the legacy default
sheet. Reads return dimension keys exactly as stored.
Bare and prefixed keys for the default sheet are distinct stored entries. When
migrating a legacy bare key, read `sheetDims` first and clear the bare twin in
the same batch, for example `{"c0":null,"default:c0":200}`; otherwise both
entries remain and downstream precedence is undefined.
Drawings are not returned by `docs sheet get`, and cell comments are managed
outside the sheet-edit surface, so do not sort an affected range unless you
already have authoritative metadata and a safe way to preserve those anchors.

Rows hidden by an active shared filter still appear in `docs sheet get`. A full
rectangle rewrite therefore includes them. To sort only visible rows, evaluate
the current `sheetFilters` criteria before editing and build a sparse permutation
that leaves hidden row coordinates unchanged; clearing the filter does not
preserve the visible subset.

```bash
# Example result of sorting two A:D data rows by column A ascending. The old
# second row had an empty C cell, so its new destination is explicitly deleted.
# Column D held row-relative formulas; each f is rewritten for its destination.
# Every non-null value shown must come from the immediately preceding sheet get.
octo-cli docs sheet edit <docId> --base-version "<token>" --data '{
  "cells":{
    "default!1:0":{"v":"a"},"default!1:1":{"v":"row-a"},"default!1:2":null,"default!1:3":{"f":"=LEN(B2)"},
    "default!2:0":{"v":"b"},"default!2:1":{"v":"row-b"},"default!2:2":{"v":"kept-with-row-b"},"default!2:3":{"f":"=LEN(B3)"}
  }
}'
```

### Single-select and multi-select dropdown lists

Dropdown lists are stored as data-validation rules. The public REST contract is
one array per logical sheet for every validation family; an array may mix
`list`, `listMultiple`, `checkbox`, and any other rules returned by the server.
Do not send the internal Y.Map's flat `${logicalId}!${uid}` key shape through
`dataValidations`; checkbox is not the sole supported rule family.

The server requires every rule to have a unique `uid` within the sheet and at
least one non-empty `ranges:[{startRow,startColumn,endRow,endColumn}]` entry. A
`uid` is 1–128 characters and must contain neither `!` nor control characters.
For a working colored dropdown, use `type:"list"` for a single selection or
`type:"listMultiple"` for multiple selections; set `formula1` to a
JSON-serialized string array, `formula2` to the same-length comma-separated
color list, `showDropDown:true`, and `renderMode:2`. Those dropdown fields are
recommended client-rendering inputs, not additional server-required fields.
Do not add an `operator` to either list type. A multi-select cell value uses the
same serialized JSON-array form (for example `"[\"待处理\",\"已完成\"]"`),
while a single-select cell stores the selected label directly.

`dataValidations` is replace-style per logical sheet: the array you send
replaces that sheet's entire rule set. Before adding or changing a dropdown,
read `sheetDataValidations` with `docs sheet get`, merge the intended change,
and resend every existing rule you need to keep. Do not make a replace-style
write if the deployed read surface cannot enumerate every validation family on
that sheet: a filtered read cannot preserve omitted rules.

```bash
# One atomic edit creates both dropdown types and sets initial values. This
# array is complete only when the sheet has no other rules; otherwise start
# from sheetDataValidations and include every existing rule you need to keep.
octo-cli docs sheet edit <docId> --base-version "<token>" --data '{
  "cells":{
    "default!1:0":{"v":"待处理"},
    "default!1:1":{"v":"[\"待处理\",\"已完成\"]"}
  },
  "dataValidations":{"default":[
    {
      "uid":"bot-status-single","type":"list",
      "formula1":"[\"待处理\",\"处理中\",\"已完成\"]",
      "formula2":"#E4F4FE,#FEF0E6,#EFFBD0",
      "showDropDown":true,"renderMode":2,
      "ranges":[{"startRow":1,"startColumn":0,"endRow":100,"endColumn":0}]
    },
    {
      "uid":"bot-status-multiple","type":"listMultiple",
      "formula1":"[\"待处理\",\"处理中\",\"已完成\"]",
      "formula2":"#E4F4FE,#FEF0E6,#EFFBD0",
      "showDropDown":true,"renderMode":2,
      "ranges":[{"startRow":1,"startColumn":1,"endRow":100,"endColumn":1}]
    }
  ]}
}'

# Replace all rules on the sheet with an empty set (null works too).
octo-cli docs sheet edit <docId> --base-version "<token>" \
  --data '{"dataValidations":{"default":[]}}'
```

Each successful edit returns a new `baseVersion`; use that new value for the
next edit. Reusing an older token correctly fails with `412 base_version_stale`.

### Column widths, row heights, and images

```bash
# Set column widths / row heights via the optional `dims` batch. Use
# `${logicalId}:c<idx>` / `${logicalId}:r<idx>` for a specific tab; bare
# `c<idx>` / `r<idx>` addresses the legacy default sheet. The two forms are
# separate stored keys, so clear a legacy bare twin while migrating. A null
# deletes a dim.
# `dims` may be the only non-empty surface in a guarded batch; across the whole
# request, at least one sheet edit surface must be non-empty.
# These are the same `sheetDims` values `docs sheet get` returns.
octo-cli docs sheet edit <docId> --base-version "<token>" \
  --data '{"dims":{"c0":null,"default:c0":200,"default:r3":40,"default:c5":null}}'

# Insert / remove a FLOATING IMAGE via the optional `drawings` batch, keyed
# `${sheetId}!${drawingId}`. The value is a serialized Univer ISheetImage with
# the bytes inline as a base64 data URL in `source`; `drawingId` MUST equal the
# key's id. A null value deletes that image. `transform` is the pixel box;
# `sheetTransform` anchors it to a cell range (from/to). Drawings are the one
# worksheet resource not returned by `docs sheet get`.
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

### Math formulas — a float-DOM drawing in the same `drawings` batch

A math formula is stored as a drawing too (same `sheetDrawings` map), but as a
**float-DOM** drawing (`drawingType: 8`), NOT an image: it has no `source` —
instead `componentKey: "octo-math-formula"` and the LaTeX in `data.latex`.
`sheetTransform.from` anchors it to a cell (`row`/`column`, 0-based). Backslashes
in LaTeX must be JSON-escaped (`\\frac`, `\\sqrt`). `data.id` must equal the
`drawingId`. Like all drawings it is **write-only** — `docs sheet get` does NOT
return it, so you can't read a formula back to verify.

```bash
octo-cli docs sheet edit <docId> --base-version "<token>" --data '{
  "drawings": { "default!formula-1": {
    "drawingId": "formula-1", "drawingType": 8, "componentKey": "octo-math-formula",
    "data": { "latex": "\\frac{-b \\pm \\sqrt{b^2-4ac}}{2a}", "id": "formula-1", "fontSize": 20 },
    "transform": {"left":100,"top":80,"width":120,"height":40},
    "sheetTransform": {"from":{"column":1,"row":1,"columnOffset":0,"rowOffset":0},"to":{"column":2,"row":2,"columnOffset":0,"rowOffset":0}}
  } }
}'
```

### Cell hyperlinks — the `hyperlinks` batch

A cell hyperlink is its OWN edit surface, not a cell field: a link lives in the
`sheetHyperLinks` map and points back at a cell by `row`/`column`. Put the
visible text in the cell's `v` separately. Each entry is keyed
`${sheetId}!${linkId}` (linkId alnum/`-`/`_`) and
is `{ id, row, column, payload, display? }` where `id` MUST equal the key's linkId,
`payload` is the URL (only `http`/`https`/`mailto`, or an internal `#…` jump — other
schemes are rejected), and `display` is an optional label. A null value deletes a
link. Unlike drawings, hyperlinks ARE returned by `docs sheet get` (as
`sheetHyperLinks`), so you can read them back.

```bash
# Cell A1 shows the text "官网" and links to a URL (two surfaces in one edit).
octo-cli docs sheet edit <docId> --base-version "<token>" --data '{
  "cells": { "default!0:0": { "v": "官网" } },
  "hyperlinks": { "default!lnk1": {
    "id": "lnk1", "row": 0, "column": 0,
    "payload": "https://example.com", "display": "官网"
  } }
}'
```

### Merged cells — the `merges` batch

A merged range is its own edit surface (the `sheetMerges` map), keyed
`${logicalId}:sr:sc:er:ec` — 0-based `startRow:startCol:endRow:endCol`. The value
is `true` to merge that block, or `null` to un-merge it. `logicalId` is the sheet's
id (`default` for the first tab). Merging keeps only the top-left cell's value; write
that cell's `v` in the same edit if you want text in the block. Merges ARE returned
by `docs sheet get` (as `sheetMerges`), so you can read them back and an xlsx export
can carry them.

```bash
# Merge A1:C2 (rows 0-1, cols 0-2) and put a title in the top-left cell.
octo-cli docs sheet edit <docId> --base-version "<token>" --data '{
  "cells": { "default!0:0": { "v": "2025 年度报表" } },
  "merges": { "default:0:0:1:2": true }
}'

# Un-merge that block.
octo-cli docs sheet edit <docId> --base-version "<token>" \
  --data '{"merges":{"default:0:0:1:2":null}}'
```

### Multiple sheet tabs — the `sheets` batch

A workbook's tabs live in the `sheetList` map, keyed by `logicalId` with value
`{name, order}` (order sorts the tabs left→right). The first/default tab has
logicalId `default`. This is its own edit surface (the `sheets` batch) and IS
returned by `docs sheet get` (as `sheetList`), so **read the current tabs first** to
learn their logicalIds before renaming, reordering, or adding one.

- **Rename / reorder** an existing tab: set its logicalId to new `{name, order}`.
- **Add a NEW sheet**: pick a fresh logicalId, set it in `sheets` AND write that
  sheet's cells with keys `${logicalId}!row:col` in the SAME edit — a tab with no
  cells is an empty sheet, and cells whose logicalId has no tab are orphaned (the
  frontend won't render them). logicalId must not contain `:` or `!`.
- **Delete** a tab: set its logicalId to `null` (also delete its cells if you want
  them gone).

```bash
# 1) read the existing tabs
octo-cli docs sheet get <docId> --format json | jq '.data.sheetList'
#   e.g. { "default": { "name": "Sheet1", "order": 0 } }

# 2) rename the default tab AND add a second sheet "明细" with one cell in it
octo-cli docs sheet edit <docId> --base-version "<token>" --data '{
  "sheets": {
    "default": { "name": "汇总", "order": 0 },
    "detail-1": { "name": "明细", "order": 1 }
  },
  "cells": { "detail-1!0:0": { "v": "明细表" } }
}'
```

## Value types & number formats — use `sheet-cell`

Plain values are easy: a JSON number stays a number (`{"v":82}`, NOT the quoted
`{"v":"82"}` — a quoted numeric lands as text with the green "number stored as
text" warning); `true`/`false` a boolean; a string a string; a formula goes in
`f`, not `v`.

**Formatted values are the trap.** Dates, times, percentages, currency, thousands
separators are all stored the same non-obvious way: a plain **number** in `v`
plus a **number-format pattern** in `s.n.pattern`, with `t:2`. Two things bite
every time you hand-assemble this:

- a date is NOT the string `"2025-01-10"` — it's the serial **number** `45667`
  (days since 1899-12-30). Writing the ISO string stores TEXT, not a date.
- it's easy to set `v` right but forget `s.n.pattern`/`t`, so the value shows
  unformatted or as text.

So **do not hand-build the cell object — use the offline `octo-cli sheet-cell`
helper.** You give a natural value, it returns the exact `{v, s:{n:{pattern}}, t:2}`
object (serial computed, percentage converted, pattern attached). No token/network.

```bash
octo-cli sheet-cell --date 2025-01-10          # -> {"v":45667,"s":{"n":{"pattern":"yyyy-mm-dd"}},"t":2}
octo-cli sheet-cell --datetime "2025-01-10 12:00"   # -> v 45667.5, pattern yyyy-mm-dd hh:mm
octo-cli sheet-cell --percent 25               # -> v 0.25, pattern 0%   (stores the FRACTION)
octo-cli sheet-cell --currency 1200            # -> pattern ¥#,##0.00  (--symbol '$' to change)
octo-cli sheet-cell --thousands 1234567        # -> pattern #,##0
octo-cli sheet-cell --number 3.14 --pattern "0.00"   # long tail: any Excel format code
octo-cli sheet-cell --date 2025-01-10 --pattern "yyyy/m/d"   # --pattern overrides the default

# Take .data and drop it under a cell key in a sheet edit (bash embeds the output):
octo-cli docs sheet edit d_123 --base-version "$BV" \
  --data '{"cells":{"default!1:0": '"$(octo-cli sheet-cell --date 2025-01-10 --format json | jq -c .data)"'}}'
```

Pass exactly one value source. `sheet-cell` emits the standard success envelope —
take `.data` for the raw cell object. Dates are exact for any date from 1900-03-01
on (every real-world date).

### What it produces (for reference / the long tail)

`pattern` is a standard **Excel number-format code** — the backend stores it
verbatim and Univer's numfmt engine renders it, so anything Excel supports works
via `--number ... --pattern ...`. Common cases (all round-trip verified):

| Kind | `sheet-cell` flag | `v` stores | `pattern` | Displays |
|------|-------------------|-----------|-----------|----------|
| Date | `--date` | serial | `yyyy-mm-dd` | `2025-01-10` |
| Date-time | `--datetime` | serial + frac. day | `yyyy-mm-dd hh:mm` | `2025-01-10 12:00` |
| Percent | `--percent` | the fraction (0.25) | `0%` | `25%` |
| Currency | `--currency` | the amount | `¥#,##0.00` | `¥1,200.00` |
| Thousands | `--thousands` | the amount | `#,##0` | `1,234,567` |
| Fixed decimals | `--number --pattern "0.00"` | the number | `0.00` | `3.14` |
| Scientific | `--number --pattern "0.00E+00"` | the number | `0.00E+00` | `1.23E-04` |
| Fraction | `--number --pattern "# ?/?"` | the number | `# ?/?` | `3 1/4` |
| Negative red | `--number --pattern "#,##0;[Red](#,##0)"` | the number | … | `(1,200)` |
| Force text | (write `{"v":"…","t":4}` directly) | any | `@` | as-is |

## Reading a large sheet in pages

A whole-sheet `docs sheet get` of a grid over the server's ~1MB read cap returns
`413 sheet_too_large`. Pass `--limit <n>` to read it in pages instead: each
response carries `hasMore` and an opaque `nextCursor`; feed that back via
`--cursor` until `hasMore` is false. Paging slices only the cells; `sheetDims`,
`sheetHyperLinks`, `sheetMerges`, `sheetList`,
`sheetFreeze`, `sheetFilters`, and `sheetDataValidations` all come back on the
first page only. Each page is bounded by both `--limit` and the byte cap, so no
page exceeds ~1MB regardless of `--limit`.

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
`413 too_many_cells` / `413 too_many_sheet_resources` / `413 cell_too_large`
(write size caps),
`400 invalid_body` (missing base version or malformed shape),
`400 invalid_limit` / `400 invalid_cursor` (bad pagination params), and
`422 sheet_cell_invalid` (a cell/dim/drawing/hyperlink/merge/tab/freeze/filter/
data-validation violates its contract or key shape). The server removes an
`operator` from otherwise-valid `list` / `listMultiple` rules; omit it so the
request and readback are identical.

## Exporting a sheet to Excel (.xlsx)

For a standard workbook export, use the server-backed command shown above:
`octo-cli docs export <docId> --export-format xlsx -o spreadsheet.xlsx`.
Only when custom reconstruction is required should a bot read the sheet and
serialize it locally: `docs sheet get` returns the readable worksheet state —
`sheetCells` (`{v,f,s,p,t}` per cell), `sheetDims` (column widths / row heights),
and the other returned worksheet maps. Feed those into the spreadsheet library
available in the bot's runtime (for example, `xlsx-js-style` in Node or
`openpyxl` in Python): split each `${logicalId}!r:c` key into its worksheet ID
and 0-based row / column, create or select that worksheet, write `v` (or `f` as
a formula), apply `s` as the cell style, preserve `p` rich text and `t` cell type
when the library supports them. `sheetDims` is one workbook-level map whose keys
may be sheet-qualified: split `${logicalId}:c<idx>` / `${logicalId}:r<idx>` and
route each width or height to that worksheet. Bare `c<idx>` / `r<idx>` keys
belong to the legacy default sheet. Reads return all keys exactly as stored; do
not duplicate unqualified dimensions across every tab. Translate `sheetFreeze`,
`sheetFilters`, and `sheetDataValidations` into the library's freeze-pane,
auto-filter, and data-validation APIs when supported. For a large grid, page the
read with `--limit`/`--cursor` and stream rows into the workbook. Merged ranges
come back in `sheetMerges` (`logicalId:sr:sc:er:ec`) and tabs in `sheetList`, so an
export can carry each sheet's values, formulas, styles, merges, and worksheet resources;
only drawings remain write-only.

## Commenting on a cell

A sheet cell comment anchors to the cell key, not a text range — see
`common.md` (Comments → "Commenting on a spreadsheet cell").

## Schema lookup

```bash
octo-cli schema docs.sheet.get
octo-cli schema docs.sheet.edit
```
