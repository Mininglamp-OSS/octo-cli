# octo-docs — Whiteboard scenes & image export (`doc_type: board`)

Read this when the target is a **whiteboard** (`doc_type: board`) and you need to
read/edit its scene or render it to an image. A non-board target returns
`409 unsupported_doc_type`.

A whiteboard stores its Excalidraw scene on the Y.Doc — an ordered list of
`elements` (in fractional-index / z-order) plus a `files` map of image/file refs.
Same read-token-then-guarded-write discipline as the body and sheet surfaces.

```bash
# Read the LIVE scene + baseVersion (reader). Response:
#   { docId, elements: [ <element> ], files: { <fileId>: {attachId} }, schemaVersion, baseVersion }
octo-cli docs scene get <docId>

# Batch-edit the scene (writer). --base-version is REQUIRED and is sent as the
# If-Match header; the batch goes through --data as a JSON object.
#   elements          — full elements to upsert (CAS: higher `version` wins)
#   deletedElementIds — element ids to soft-delete (tombstone)
#   files             — file refs to upsert
# Every element MUST carry a valid fractional-index `index` (z-order key); the
# CLI rejects an index-less or malformed-index element locally before sending.
octo-cli docs scene edit <docId> --base-version "<token>" \
  --data '{"elements":[{"id":"e1","type":"rectangle","version":4,"index":"a0"}],"deletedElementIds":["e2"],"files":{}}'
```

> **`index` is mandatory on every upserted element.** It must be a valid
> fractional-index (z-order) key — the same jitterbug / Excalidraw key format the
> board uses, generated with the `fractional-indexing` rules (e.g. `a0`, `a1`,
> `a0V`). Never omit `index`, and never fabricate one as an `r`+digits string, a
> plain integer, or a timestamp: an index-less element used to slip through to a
> buggy backend repair path that rewrote it into an invalid key (like
> `r00000003`) and broke the board. The CLI now rejects such elements with a
> non-zero exit before the request is sent.

The upsert is element-level: only the ids named in `elements` / `deletedElementIds`
are touched (the rest of the board is left as-is). On a key collision the element
with the higher `version` (then smaller `versionNonce`) wins, and a delete is a
soft-delete tombstone with a superseding version so it converges under CAS.

## Excalidraw file import

Import a standard `.excalidraw` JSON envelope (maximum 25 MiB) directly into an
existing board. The CLI validates `type: "excalidraw"`, a positive integer
`version`, an `elements` array, and a `files` object before sending the request.

```bash
# Safe default: merge imported elements while preserving the existing board.
octo-cli docs import <docId> --file ./diagram.excalidraw

# Explicit overwrite: replace the board with the imported scene.
octo-cli docs import <docId> --file ./diagram.excalidraw --mode replace
```

`--mode` is only valid for `.excalidraw` imports and accepts `merge` (default) or
`replace`. Merge preserves existing board elements. Replace explicitly overwrites
the board; the backend first creates a safety snapshot and enforces concurrency
protection. Use `--dry-run` to inspect the mode, endpoint, and these safety
semantics without applying the import.

**Concurrency / errors.** The base version is optimistic-concurrency: if the scene
changed since your `docs scene get`, an edit is rejected with
`412 base_version_stale` — re-read for a fresh token. Other gates:
`409 unsupported_doc_type` (target is a doc/sheet), `409 board_snapshot_invalid`
(the live scene decodes to a wrong-kind/corrupt blob, on read),
`413 too_many_elements` / `413 element_too_large` / `413 doc_too_large` (size
caps), `400 invalid_body` (missing base version or malformed shape),
`422 board_element_invalid` (an element fails the whitelist), and
`422 board_file_invalid` (a file ref carries no usable `attachId`).

## Whiteboard image export

Render a whiteboard's **live** Excalidraw scene to an image on the server. The
response body is binary, so `--output`/`-o` is required and saves it atomically
to the matching `.png` or `.svg` destination.

```bash
# PNG. -o writes the bytes to disk and the envelope echoes the saved path.
octo-cli docs export <docId> --export-format png -o board.png

# SVG (vector).
octo-cli docs export <docId> --export-format svg -o board.svg
```

> `--export-format` is `png` or `svg`; any other value is rejected locally.
> (The flag is named `--export-format`, not `--format`, so it
> does not collide with the global `--format` output-envelope flag; the wire
> query parameter is still `format`.) The export reflects the scene as it is live
> right now (shapes, text, and embedded images), not a persisted snapshot.
> `-o` overwrites an existing destination file.

## Schema lookup

```bash
octo-cli schema docs.scene.get
octo-cli schema docs.scene.edit
```
