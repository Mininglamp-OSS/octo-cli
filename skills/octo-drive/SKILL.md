---
name: octo-drive
version: 0.1.0
description: Octo Drive — spaces, folders, file upload/download, online-document mounts, share links, invites, IM-attachment transfer. Works with a bot token or a user API key; the CLI routes by token kind. Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-drive — network drive operations

45 commands over one backend. Everything is scoped to a **drive space**: either your personal space or a shared one you are a member of.

## 1. Credentials — nothing drive-specific

Drive uses the same credential as every other domain. Set one of:

```bash
export OCTO_TOKEN=<uk_… | bf_… | app_…>      # preferred slot, any token kind
export OCTO_BOT_TOKEN=<bf_… | app_…>         # long-standing variable, still fine
```

`OCTO_TOKEN` wins when both are set, so you can run one command as a real person without disturbing a bot setup:

```bash
OCTO_TOKEN=$UK_KEY octo-cli drive space list
```

A stored profile (`octo-cli auth login`) takes precedence over both. Do **not** create a drive-only profile — there is no such concept.

The token kind decides which identity the backend sees, and the CLI routes the request accordingly:

| token | acts as | mount |
|---|---|---|
| `uk_…` | the real person who owns the key | `/v1/user/drive/*` |
| `bf_…` | the User Bot | `/v1/bot/drive/*` |
| `app_…` | the App Bot | `/v1/bot/drive/*` |

Any other credential fails locally with `TOKEN_KIND_NOT_ALLOWED` (exit 2) — switch credentials, don't retry.

A bot has **no implicit access**: to touch a shared space it must be added as a member, exactly like a person. If a bot gets `permission_denied`, add it with `drive member add`. An `app_…` token may also lack a resolvable space entirely; that surfaces as a 401 (exit 3) from the server, and the fix is a `bf_…` or `uk_…` credential.

Drive never sends `X-Space-Id` — the tenant comes from the verified identity.

## 2. Ids — copy them, never compute with them

| id | shape | produced by | consumed by |
|---|---|---|---|
| drive space id | opaque string, e.g. `personal:<octo-space>:<uid>` or `shared:<uuid>` | `space create/list/get/ensure-personal` → `.data.id` | `--space-id`, `--target-space-id`, positional `<space-id>` |
| drive file id | **decimal string** | `browse`, `folder create`, `upload file`, `doc mount`, `im-transfer create` → `.data.id` | `<file-id>`, `--parent-id` (`"0"` = space root) |
| `doc_id` / `doc_space_id` | opaque strings | `doc candidates`, `doc list`, `file get` | document links only |
| `share_id` vs `share_token` | opaque strings | `share create`, `share blob-create`, `share list` | `share_id` → `share revoke`; the token is embedded in `share_url` |
| `invite_id` vs `invite_token` | opaque strings | `invite create`, `invite list` | `invite_id` → `invite revoke`; `invite_token` → `invite accept` |

**File ids are uint64 and are emitted as JSON strings on purpose.** Values above 2^53 would be silently rounded by a JavaScript-style parser, addressing a *different file*. So: pass them through verbatim (`-q '.data.id' -r`), never do arithmetic on them, never reformat them. The CLI rejects a non-decimal or out-of-range id locally.

Two traps worth naming:

- `space_id` (the drive space) and `doc_space_id` (the document's own Octo Space) are **different scopes**. Building a document link from `space_id` produces a link to the wrong place. The CLI fails closed rather than substituting.
- `doc unmount` takes the **mount node id** (`.data.id`), not the `doc_id`.

## 3. The five workflows

### Space → folder → upload → share → download

```bash
SPACE=$(octo-cli drive space create --name "Project files" -q '.data.id' -r)
FOLDER=$(octo-cli drive folder create --space-id "$SPACE" --parent-id 0 --name Contracts -q '.data.id' -r)
FILE=$(octo-cli drive upload file ./contract.pdf --space-id "$SPACE" --parent-id "$FOLDER" -q '.data.id' -r)

SHARE_URL=$(octo-cli drive share create "$FILE" -q '.data.share_url' -r)
# Hand SHARE_URL to the receiver verbatim. They never extract a token:
octo-cli drive share access   "$SHARE_URL"
octo-cli drive share download "$SHARE_URL" -o ./contract.pdf

# A space member can also download by internal id:
octo-cli drive download file "$FILE" -o ./copy.pdf
```

### Personal space → mount a document → share its link

```bash
SPACE=$(octo-cli drive space ensure-personal -q '.data.id' -r)
DOC=$(octo-cli drive doc candidates --space-id "$SPACE" -q '.data.items[0].doc_id' -r)
MOUNT=$(octo-cli drive doc mount --space-id "$SPACE" --doc-id "$DOC" -q '.data.id' -r)
DOC_URL=$(octo-cli drive share create "$MOUNT" -q '.data.share_url' -r)

octo-cli drive share access "$DOC_URL"      # resolves the target; grants nothing
# `share download` on a document link fails with NOT_DOWNLOADABLE — by design.
```

`doc mount` takes no `--doc-title`: the title and the document's real Octo Space are read server-side from the document metadata, so they cannot drift.

### Invite a member

```bash
SPACE=$(octo-cli drive space create --name "Collab" -q '.data.id' -r)
INVITE_ID=$(octo-cli drive invite create "$SPACE" --role editor -q '.data.invite_id' -r)
TOKEN=$(octo-cli drive invite list "$SPACE" -q '.data.invites[0].invite_token' -r)

octo-cli drive invite accept "$TOKEN"          # as the invitee's credential
octo-cli drive invite revoke "$SPACE" "$INVITE_ID"
```

`invite_id` / `invite_token` are base64url and may start with `-`; prefer
`--invite-id` / `--invite-token` in scripts so a leading dash is never parsed as a flag.

Roles accepted by `invite create`: `preview_only`, `downloader`, `uploader_downloader`, `editor`, `admin` (admin only if you are the space's `super_admin`). `custom` and `super_admin` are rejected on invites. Or add a known uid directly:

```bash
octo-cli drive member add "$SPACE" --uid "$UID" --role editor
```

`member add` / `member set-role` accept one more role than invites do — `custom`, the lowest rank (below `preview_only`). `super_admin` is never grantable: it is bound to the space creator at space creation.

| role | `member add` / `set-role` | `invite create` |
|---|---|---|
| `preview_only` / `downloader` / `uploader_downloader` / `editor` | ✅ | ✅ |
| `admin` | ✅ super_admin only | ✅ super_admin only |
| `custom` | ✅ | ❌ |
| `super_admin` | ❌ | ❌ |

Drive has no user search — get a uid from the message/group commands or your own context.

### IM attachment → drive

```bash
MSG=$(octo-cli message search files --chat-id "$GROUP" -q '.data.items[0].message_id' -r)
FILE=$(octo-cli drive im-transfer create \
  --im-group-no "$GROUP" --im-channel-type 2 --im-msg-id "$MSG" \
  --target-space-id "$SPACE" -q '.data.id' -r)
```

`--im-channel-type` is required: `1`=DM, `2`=group, `5`=thread, and it must be the kind the message actually came from. It picks the upstream message-read route (`1` uses the DM route; `2` and `5` share the group route, where group vs sub-thread comes from the composite `group_no`), and it is stored as the first segment of the row's `source_key` (`channelType#channelID#msgID`), which the chat file-card's already-transferred lookup matches on — a wrong value makes that lookup miss. Anything outside `1|2|5` is rejected locally (`ENUM_NOT_ALLOWED`, exit 2).

Transfer idempotency is keyed on (target space, type=blob, object path), not on the channel type: a replay of the same message returns the existing row with `idempotent: true`, and a wrong channel type cannot produce a duplicate file. Keep the message id a string.

### Browse and act

```bash
octo-cli drive browse --space-id "$SPACE" --parent-id 0
octo-cli drive browse --space-id "$SPACE" --type blob --source user-upload

FILE=$(octo-cli drive browse --space-id "$SPACE" -q '.data.entries[0].id' -r)
octo-cli drive file get    "$FILE"                       # type → blob | doc | folder
octo-cli drive file move   "$FILE" --parent-id "$FOLDER"
octo-cli drive file rename "$FILE" --name new-name.pdf
octo-cli drive file copy   "$FILE" --parent-id "$FOLDER"
```

`file get` is how you branch: `type` tells you whether an id is a blob, a mounted document, or a folder.

`browse` returns the complete listing; its `page` object is an envelope, not a database page, so `--page-index` / `--page-size` do not actually narrow the result yet.

## 4. Upload and download in detail

`drive upload file` runs prepare → PUT to object storage → confirm. The PUT goes out on a separate HTTP client that carries **no** Octo credential — the presigned URL is its own authorisation. If anything fails after the pending row exists, the CLI cancels it and the error reports the `file_id` plus the cancel outcome:

```json
{"ok":false,"error":{"code":"UPLOAD_FAILED","detail":{"file_id":"42","pending_file":"cancelled"}}}
```

If `pending_file` is not `cancelled`, run `octo-cli drive upload cancel <file-id>` yourself.

`drive download file` and `drive share download` write to `<output>.part`, fsync, then rename — an interrupted transfer never leaves a truncated file. **An existing destination is refused** unless you pass `--overwrite`. The result carries a `sha256` you can verify.

The low-level steps stay available (`upload prepare|confirm|cancel`, `download url`) if you need to drive the transfer yourself.

`drive blob create` is a different thing and is rarely what you want: it registers an object **already** in storage rather than uploading one. The backend verifies it — an `--object-path` storage does not hold is `invalid_argument`, and a `--size` that conflicts with the stored object is rejected (`--size 0` for a non-empty object included) — so it can no longer produce a row that lists fine and 404s on download. If storage is unreachable the probe is inconclusive and you get a 500 to retry, not `invalid_argument`. It still persists no download URL, so `share download` on such a row answers `not_found`. Use `drive upload file` unless the bytes are already in the bucket.

## 5. Share links

There is exactly one thing the two sides exchange: `data.share_url`.

```bash
octo-cli drive share create "$FILE" --permission download --expires-in-seconds 86400 --password "$PW"
```

- `--password` is passed out of band — it is never in the URL, and it is masked in `--verbose` / `--dry-run` output.
- **Both sides need a credential.** There is no anonymous share. The token (and password) authorise the *share*; your credential authenticates *you*. The receiver does not have to be a member of the file's space.
- `share access` / `share download` accept only links on your configured Octo origin, in exactly the `/drive/s/<token>` or `/d/<docId>?sp=<docSpaceId>` shape. Anything else fails with `INVALID_SHARE_URL` — the CLI parses the link, it never fetches the host in it.
- `downloadable` tells you which command to use next. Documents are always `false`, and so is a blob shared with `--permission view` — `share download` on either answers `permission_denied`. Only `--permission download` yields bytes.
- Revoke with the `share_id`, not the token: `octo-cli drive share revoke "$SHARE_ID"`.
- **base64url ids can start with `-`** (about one in 64), which cobra reads as a flag. Pass those as a flag instead — `octo-cli drive share revoke --share-id "$SHARE_ID"` — or put the value after a `--` separator. The same applies to `--invite-id` (`invite revoke`) and `--invite-token` (`invite accept`). Using the flag form unconditionally is the safe habit for a script.

## 6. Errors

| code | type / exit | what to do |
|---|---|---|
| `TOKEN_KIND_NOT_ALLOWED` | validation / 2 | switch credentials; do not retry |
| `ENUM_NOT_ALLOWED` | validation / 2 | the value is outside the spec's enum; the hint lists the accepted set |
| `unauthorized` | auth / 3 | token invalid, revoked, or the user/bot is inactive |
| `permission_denied` | permission / 1 | the identity lacks the space role; `drive member add` it |
| `password_required` / `wrong_password` | permission / 1 | pass or fix `--password` |
| `share_expired` | permission / 1 | ask for a new link |
| `not_found` | api_error / 1 | check the id and that the space is reachable |
| `conflict` | validation / 2 | re-read state, then retry |
| `invalid_argument` | validation / 2 | check the schema: `octo-cli schema drive.<op>` |
| `FILE_EXISTS` | validation / 2 | pass `--overwrite` or pick another path |
| `NOT_DOWNLOADABLE` | validation / 2 | a document link; use `share access` or a browser |
| `INVALID_SHARE_URL` | validation / 2 | pass the `share_url` exactly as produced |
| `MISSING_DOC_SPACE_ID` | validation / 2 | re-mount the document; never substitute the drive space id |
| `UNSAFE_PRESIGNED_URL` | api_error / 1 | the backend returned an unusable URL; report it |

## 7. Destructive commands

`space delete`, `member remove`, `folder delete`, `blob delete`, `doc unmount`, `share revoke`, `invite revoke` are all high-risk writes with no confirmation prompt (agent runtimes cannot prompt). Deletes are soft on the backend, but `folder delete` takes the whole subtree with it.

Preview any write first:

```bash
octo-cli drive folder delete "$FOLDER" --dry-run
```

`--dry-run` on `upload file`, `download file`, `share create` and `share download` describes the plan and stops: no pending row is created, no URL is fetched, nothing is written to disk.

## 8. Full command list

```
drive browse
drive space       create | list | ensure-personal | get | rename | delete
drive member      list | add | set-role | remove
drive folder      create | list | rename | move | delete
drive file        get | move | copy | rename
drive blob        create | get | list | delete
drive upload      file | prepare | confirm | cancel
drive download    file | url
drive doc         mount | unmount | list | candidates
drive share       create | blob-create | list | revoke | access | download
drive invite      create | list | revoke | accept
drive im-transfer create
```

Per-command flags: `octo-cli drive <group> <verb> --help`, or `octo-cli schema drive.<group>.<verb>` for the wire contract.
