# octo-docs — Common features (comments, versions, members, attachments)

Read this for capabilities that apply across all doc kinds. Jump to the section
you need:

- [Comments](#comments) — add/list/reply/resolve, on a doc text range or a sheet cell
- [Versions](#versions) — snapshot / preview / rename / delete / restore
- [Members & sharing](#members--sharing) — roles, forward-grant
- [Invite links](#invite-links) — create/list/revoke/accept a link that grants a role
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
octo-cli docs members list   <docId>                             # admin
octo-cli docs members set    <docId> --uid <uid> --role writer   # add/upsert; role: reader|commenter|writer|admin
octo-cli docs members remove <docId> <uid>                       # admin; owner cannot be removed

# Grant-only forward access (never downgrades). Grantable: reader|commenter|writer (admin is not).
octo-cli docs forward-grant  <docId> --uid <uid> --role reader

# Space-level share scope — read/set who in the space can reach the doc.
octo-cli docs share get <docId>                                       # reader; {docId, shareScope, shareRole}
octo-cli docs share set <docId> --scope restricted                    # admin; only owner/members can access
octo-cli docs share set <docId> --scope anyone_in_space --role read   # admin; every space member gets read
octo-cli docs share set <docId> --scope anyone_in_space --role edit   # admin; every space member gets edit
```

`docs members set` is a PUT-upsert: it adds the member if absent or changes the
role if already present. The target uid must be a real Octo user — a miss returns
`404 user_not_found` and writes no ghost member. Schema: `octo-cli schema docs.members.set`.

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

## Invite links

An invite is a **link token** that grants a role to whoever accepts it. Use it
when you cannot name the recipients up front (`members set` needs a uid); use
`members set` when you can.

```bash
# Create (admin). Prints {inviteToken, role, expiresAt}.
octo-cli docs invite create <docId> [--role writer] [--expiresInDays 3] [--maxUses 0]

# List the live invites of a doc (admin): token, role, maxUses, usedCount, expiresAt.
octo-cli docs invite list <docId>

# Revoke (admin). NOTE: addressed by the TOKEN — docs has no separate invite id.
octo-cli docs invite revoke <docId> <inviteToken>
octo-cli docs invite revoke <docId> --invite-token <inviteToken>   # token starting with "-"

# Accept as the credential in use. Takes the bare token OR the whole invite link.
octo-cli docs invite accept <inviteToken>
octo-cli docs invite accept "$OCTO_API_BASE_URL/docs/invite/<inviteToken>"
```

**Role.** `reader` | `commenter` | `writer` | `admin` — the same member-role
ladder as `docs members set`, in increasing order of capability. **Omitting**
`--role` is what selects the `writer` default; an unrecognised value is rejected
by the backend with `400 role must be reader|commenter|writer|admin`, and the CLI
refuses it locally first (`ENUM_NOT_ALLOWED`, exit 2) so it costs no round trip.
Note that `commenter` sits **between** `reader` and `writer` despite its stored
numeric code being `4` — that number is historical, the rank is not.

**Lifetime.** `--expiresInDays` is 1–7 days, default 3. There is no permanent
invite link; a value outside the range is clamped by the backend without an
error, so pass something in range if you care which. `--maxUses 0` (the default)
means unlimited accepts.

**Accept is idempotent.** Accepting twice, or accepting into a document you are
already a member of, is still `200` with the resulting role, and never downgrades
a higher role you already hold. An unknown, revoked, expired or exhausted token
is **`410 invite_invalid`** — not 404. Revoking does not remove members who
already accepted; it only stops further accepts.

**The link form.** The web app serves invites at
`{octo web origin}/docs/invite/<inviteToken>`, and `invite accept` takes that
whole link so you can paste what someone sent you. The CLI **never fetches the
link**: it parses out the token locally, requires the link's host to match the
configured `OCTO_API_BASE_URL` origin, and calls the configured API. A link on
any other host, with embedded credentials, with a percent-encoded path, or with
extra path segments is refused (`INVALID_INVITE_URL`, exit 2).

> **The inviteToken is a credential.** Anyone holding it can claim the role on
> that document. Hand it over out of band, never paste `invite create` /
> `invite list` output into a ticket or a shared channel, and revoke it when the
> invite has served its purpose. The CLI masks the token in `--dry-run` and
> `--verbose` traces for exactly this reason — but `invite create`'s success
> output necessarily contains it in full, because it is the thing you asked for.

```bash
# Typical flow: mint a reader link good for a day, hand over the URL, revoke later.
TOKEN=$(octo-cli docs invite create d_1 --role reader --expiresInDays 1 \
  -q '.data.inviteToken' | tr -d '"')
echo "$OCTO_API_BASE_URL/docs/invite/$TOKEN"    # hand this over out of band
octo-cli docs invite revoke d_1 -- "$TOKEN"
```

Schemas: `octo-cli schema docs.invite.create` | `docs.invite.list` |
`docs.invite.revoke` | `docs.invite.accept`.

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
