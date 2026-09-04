# octo-docs — Spreadsheet cells, dims & images (`doc_type: sheet`)

Read this when the target is a **spreadsheet** (`doc_type: sheet`) and you need to
read or edit its cells, column widths / row heights, floating images, hyperlinks,
merged ranges, sheet tabs, checkbox rules, or export it.
All commands call `$OCTO_API_BASE_URL/v1/bot/docs/*`. Auth & space rules are in
`SKILL.md`.

Export a sheet to XLSX with a required, matching destination extension:

```bash
octo-cli docs export <docId> --export-format xlsx -o spreadsheet.xlsx
```

A spreadsheet stores a flat cell map on the Y.Doc, keyed `sheetId!row:col` (e.g.
`default!0:0`), with values `{v,f,s}` — `v` a string/number/boolean/null, `f` an
optional formula, `s` an opaque resolved style object. (Cells authored in the web
may also carry Univer's `p` rich-text snapshot and `t` cell-type; both round-trip
untouched.) Same read-token-then-guarded-write discipline as the body surface.

```bash
# Read the LIVE cells + dims + hyperlinks + merges + sheet tabs + checkbox rules + baseVersion
# (reader). Response:
#   { docId, sheetCells: { "sheetId!row:col": {v,f,s} }, sheetDims: { "c<idx>|r<idx>": px },
#     sheetHyperLinks: { "sheetId!linkId": {id,row,column,payload,display?} },
#     sheetMerges: { "logicalId:sr:sc:er:ec": true },
#     sheetList: { "logicalId": {name,order} },
#     sheetDataValidations: { "logicalId!uid": {uid,type:"checkbox",ranges:[...]} }, baseVersion }
octo-cli docs sheet get <docId>

# Batch-edit cells (writer). --base-version is REQUIRED and is sent as the
# If-Match header; the cells batch goes through --data as a JSON object.
# A cell value of null DELETES that cell.
octo-cli docs sheet edit <docId> --base-version "<token>" \
  --data '{"cells":{"default!0:0":{"v":"hi"},"default!1:0":null}}'
```

```bash
# Set column widths / row heights via the optional `dims` batch, keyed
# `c<idx>` (column) or `r<idx>` (row) -> pixels; a null value deletes a dim.
# Provide at least one non-empty supported batch. These are the same
# `sheetDims` values `docs sheet get` returns.
octo-cli docs sheet edit <docId> --base-version "<token>" \
  --data '{"dims":{"c0":200,"r3":40,"c5":null}}'

# Insert / remove a FLOATING IMAGE via the optional `drawings` batch, keyed
# `${sheetId}!${drawingId}`. The value is a serialized Univer ISheetImage with
# the bytes inline as a base64 data URL in `source`; `drawingId` MUST equal the
# key's id. A null value deletes that image. `transform` is the pixel box;
# `sheetTransform` anchors it to a cell range (from/to). NOT returned by
# `docs sheet get` (other sheet maps, including checkbox rules, are readable).
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

### Checkboxes — the `dataValidations` batch

A checkbox is a cell value plus a checkbox validation rule. Rule keys are
`${logicalId}!${uid}` and `uid` must equal the key suffix. Ranges use 0-based,
inclusive coordinates. Only `type:"checkbox"` is accepted, and rules must target
an existing sheet. `formula1` / `formula2` are optional, distinct strings. They
default to `"1"` / `"0"`, whose cell values are numeric `1` (checked) and `0`
(unchecked). A custom pair such as `"YES"` / `"NO"` instead uses those strings as
the cell values. The server forces `allowBlank:true`, so an empty checkbox cell
stays valid.

The complete optional rule surface is `formula1`, `formula2`, `error`,
`errorTitle`, `prompt`, `promptTitle`, `showDropDown`, `showErrorMessage`,
`showInputMessage`, `operator`, `imeMode`, `errorStyle`, `renderMode`, and
`bizInfo:{showTime?}`. The text fields are bounded strings; formulas are literal
values and cannot start with `=`. Boolean fields must be booleans. `operator` is
one of `between`, `equal`, `greaterThan`, `greaterThanOrEqual`, `lessThan`,
`lessThanOrEqual`, `notBetween`, or `notEqual`; `imeMode` is 0..10 and
`errorStyle` / `renderMode` are 0..2.

Rules must not overlap the complete rule set after the edit. Updating an existing
`uid` replaces the complete rule before overlap validation, so it may move away
from its old range. Read the existing entry from `sheetDataValidations` and copy
every optional field you intend to preserve; omitted fields are cleared. Set a
rule to `null` to delete it. Deleting a sheet tab automatically deletes all
checkbox rules owned by that tab.

If a read or write returns `413 sheet_too_large` with `baseVersion` and
`repairDataValidationKey` under the CLI error `detail`, the server could not
finish its bounded checkbox-rule inspection. Delete only that returned key with
the returned version, read again, and repeat if another repair key is returned:

```bash
octo-cli docs sheet edit <docId> --base-version "<error.detail.baseVersion>" \
  --data '{"dataValidations":{"<error.detail.repairDataValidationKey>":null}}'
```

Do not mix cells or other changes into this repair PATCH. The backend only
permits this path when it strictly shrinks the raw checkbox metadata.

```bash
# Create an unchecked checkbox in A2 atomically with its initial value.
octo-cli docs sheet edit <docId> --base-version "<token>" --data '{
  "cells": { "default!1:0": { "v": 0 } },
  "dataValidations": { "default!checkbox-a2": {
    "uid": "checkbox-a2", "type": "checkbox",
    "formula1": "1", "formula2": "0",
    "ranges": [{"startRow":1,"startColumn":0,"endRow":1,"endColumn":0}]
  } }
}'

# Remove the checkbox rule. Delete the cell too if its 0/1 value is unwanted.
octo-cli docs sheet edit <docId> --base-version "<token>" --data '{
  "cells": { "default!1:0": null },
  "dataValidations": { "default!checkbox-a2": null }
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

A cell hyperlink is its OWN edit surface (a fourth batch alongside cells/dims/
drawings), NOT a cell field: a link lives in the `sheetHyperLinks` map and points
back at a cell by `row`/`column`. Put the visible text in the cell's `v`
separately. Each entry is keyed `${sheetId}!${linkId}` (linkId alnum/`-`/`_`) and
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
`413 too_many_cells` / `413 too_many_data_validations` / `413 cell_too_large`
(write batch and size caps),
`400 invalid_body` (missing base version or malformed shape),
`400 invalid_limit` / `400 invalid_cursor` (bad pagination params), and
`422 sheet_cell_invalid` (a cell/dim/drawing/hyperlink/merge/tab/checkbox rule
violates its contract or key shape). A write intersecting a protected range returns
`403 protected_range`.

## Exporting a sheet to Excel (.xlsx)

There is no server-side sheet export endpoint. A bot exports by **reading the
sheet and serializing it locally**: `docs sheet get` returns everything needed —
`sheetCells` (`{v,f,s}` per cell) and `sheetDims` (column widths / row heights) —
so feed those into whatever spreadsheet library the bot's runtime has (e.g.
`xlsx-js-style` in Node, `openpyxl` in Python): map `default!r:c` → row r / col c,
write `v` (or `f` as a formula), apply `s` as the cell style, and set widths from
`c<idx>` / heights from `r<idx>`. For a large grid, page the read with
`--limit`/`--cursor` and stream rows into the workbook. Merged ranges come back in
`sheetMerges` (`logicalId:sr:sc:er:ec`), tabs in `sheetList`, and checkbox rules in
`sheetDataValidations`, so an export can carry values + styles + dimensions + merges
+ checkboxes across every sheet; only floating images / formulas stay write-only
(not in the read surface).

## Commenting on a cell

A sheet cell comment anchors to the cell key, not a text range — see
`common.md` (Comments → "Commenting on a spreadsheet cell").

## Schema lookup

```bash
octo-cli schema docs.sheet.get
octo-cli schema docs.sheet.edit
```
