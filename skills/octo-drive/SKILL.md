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

49 commands over one backend. Everything is scoped to a **drive space**: either your personal space or a shared one you are a member of.

## 1. Credentials — nothing drive-specific

Drive uses the same credential as every other domain. Set one of:

```bash
export OCTO_TOKEN=<uk_… | bf_… | app_…>      # preferred slot, any token kind
export OCTO_BOT_TOKEN=<uk_… | bf_… | app_…>    # fully compatible alias of OCTO_TOKEN
```

`OCTO_TOKEN` wins when both are set, so you can run one command as a real person without disturbing a bot setup:

```bash
OCTO_TOKEN=$UK_KEY octo-cli drive space list
```

An explicit `--bot-id` / `--profile` selector wins. Without one, precedence is `OCTO_TOKEN` > `OCTO_BOT_TOKEN` > an implicit stored profile (two or more profiles require a selector).

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

**File ids are uint64 and are emitted as JSON strings on purpose.** Values above 2^53 would be silently rounded by a JavaScript-style parser, addressing a *different file*. So: pass them through verbatim (`-q '.data.id' | tr -d '"'`), never do arithmetic on them, never reformat them. The CLI rejects a non-decimal or out-of-range id locally.

**Why `| tr -d '"'` on every capture below.** `--jq`/`-q` prints a JSON *value*, and ids are JSON strings — so `-q '.data.id'` emits `"123"` including the quotes, and a quoted id is rejected by the next command's id validation. There is no raw-output flag; `-q` is the only output filter the CLI defines. Strip the quotes in the shell. (Numbers already come out bare, so this matters for string-valued fields: every id, `share_url`, and every opaque token.)

Two traps worth naming:

- `space_id` (the drive space) and `doc_space_id` (the document's own Octo Space) are **different scopes**. Building a document link from `space_id` produces a link to the wrong place. The CLI fails closed rather than substituting.
- `doc unmount` takes the **mount node id** (`.data.id`), not the `doc_id`.

## 3. The six workflows

### Space → folder → upload → share → download

```bash
SPACE=$(octo-cli drive space create --name "Project files" -q '.data.id' | tr -d '"')
FOLDER=$(octo-cli drive folder create --space-id "$SPACE" --parent-id 0 --name Contracts -q '.data.id' | tr -d '"')
FILE=$(octo-cli drive upload file ./contract.pdf --space-id "$SPACE" --parent-id "$FOLDER" --name contract.pdf --content-type application/pdf -q '.data.id' | tr -d '"')

SHARE_URL=$(octo-cli drive share create "$FILE" -q '.data.share_url' | tr -d '"')
# Hand SHARE_URL to the receiver verbatim. They never extract a token:
octo-cli drive share access   "$SHARE_URL"
octo-cli drive share download "$SHARE_URL" -o ./contract.pdf

# A space member can also download by internal id:
octo-cli drive download file "$FILE" -o ./copy.pdf
```
For `drive upload file`, optional `--name` and `--content-type` override the stored filename and MIME type; when omitted, the filename comes from the local path and content type is detected.

### Personal space → mount a document → share its link

```bash
SPACE=$(octo-cli drive space ensure-personal -q '.data.id' | tr -d '"')
# Candidate listing supports real pagination:
# `--page` is 1-based; `--page-size` selects the page size.
DOC=$(octo-cli drive doc candidates --space-id "$SPACE" --page 1 --page-size 50 -q '.data.items[0].doc_id' | tr -d '"')
MOUNT=$(octo-cli drive doc mount --space-id "$SPACE" --doc-id "$DOC" -q '.data.id' | tr -d '"')
DOC_URL=$(octo-cli drive share create "$MOUNT" -q '.data.share_url' | tr -d '"')

octo-cli drive share access "$DOC_URL"      # resolves the target; grants nothing
# `share download` on a document link fails with NOT_DOWNLOADABLE — by design.
```

`doc mount` takes no `--doc-title`: the title and the document's real Octo Space are read server-side from the document metadata, so they cannot drift.

### Invite a member

```bash
SPACE=$(octo-cli drive space create --name "Collab" -q '.data.id' | tr -d '"')
INVITE_ID=$(octo-cli drive invite create "$SPACE" --role editor --expires-in-seconds 86400 -q '.data.invite_id' | tr -d '"')
TOKEN=$(octo-cli drive invite list "$SPACE" -q '.data.invites[0].invite_token' | tr -d '"')

octo-cli drive invite accept --invite-token "$TOKEN"          # as the invitee's credential
octo-cli drive invite revoke "$SPACE" --invite-id "$INVITE_ID"
```
`--expires-in-seconds` controls invite lifetime; omitted or `0` uses the backend's seven-day default.


`invite_id` / `invite_token` are base64url and may start with `-`; prefer
`--invite-id` / `--invite-token` in scripts so a leading dash is never parsed as a flag.

Roles accepted by `invite create`: `preview_only`, `downloader`, `uploader_downloader`, `editor`, `admin` (admin only if you are the space's `super_admin`). `custom` and `super_admin` are rejected on invites. Or add a known uid directly:

```bash
# Set OCTO_UID to the target's Octo user or bot identity first.
octo-cli drive member add "$SPACE" --uid "$OCTO_UID" --role editor
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
MSG=$(octo-cli message search files --chat-id "$GROUP" -q '.data.items[0].message_id' | tr -d '"')
FILE=$(octo-cli drive im-transfer create \
  --im-group-no "$GROUP" --im-channel-type 2 --im-msg-id "$MSG" \
  --target-space-id "$SPACE" --target-parent-id "$FOLDER" --name-override "attachment.pdf" \
  -q '.data.id' | tr -d '"')
```

`--im-channel-type` is required: `1`=DM, `2`=group, `5`=thread, and it must be the kind the message actually came from. It picks the upstream message-read route (`1` uses the DM route; `2` and `5` share the group route, where group vs sub-thread comes from the composite `group_no`), and it is stored as the first segment of the row's `source_key` (`channelType#channelID#msgID`), which the chat file-card's already-transferred lookup matches on — a wrong value makes that lookup miss. Anything outside `1|2|5` is rejected locally (`ENUM_NOT_ALLOWED`, exit 2).

Transfer idempotency is keyed on (target space, type=blob, object path), not on the channel type: a replay of the same message returns the existing row with `idempotent: true`, and a wrong channel type cannot produce a duplicate file. Keep the message id a string.

### Browse and act

```bash
octo-cli drive browse --space-id "$SPACE" --parent-id 0
octo-cli drive browse --space-id "$SPACE" --type blob --source user-upload

FILE=$(octo-cli drive browse --space-id "$SPACE" -q '.data.entries[0].id' | tr -d '"')
octo-cli drive file get    "$FILE"                       # type → blob | doc | folder
octo-cli drive file move   "$FILE" --parent-id "$FOLDER"
octo-cli drive file copy   "$FILE" --parent-id "$FOLDER" --name "copy-of-file.pdf"
octo-cli drive folder move "$FOLDER_NODE" --parent-id 0

# Cross-space destinations are optional; on-conflict is forwarded as a string.
octo-cli drive file move "$FILE" --target-space-id "$TARGET_SPACE" --parent-id 0 --on-conflict rename
octo-cli drive file copy "$FILE" --target-space-id "$TARGET_SPACE" --parent-id 0 --on-conflict rename
octo-cli drive folder move "$FOLDER_NODE" --target-space-id "$TARGET_SPACE" --parent-id 0 --on-conflict rename
```


`file get` is how you branch: `type` tells you whether an id is a blob, a mounted document, or a folder.

`--target-space-id` is optional; omitting it keeps move/copy in the source space. The three move/copy operations accept `--on-conflict` as a string forwarded to Drive. The handler documents `rename`, but the backend controls conflict handling and outcome; the CLI does not promise that a name will be automatically changed.


`browse` returns the complete listing; its `page` object is an envelope, not a database page, so `--page-index` / `--page-size` do not actually narrow the result yet.

### Full-text search

`drive search` is full-text search across every space the caller can see (name + blob/doc body via OpenSearch), scoped by the caller's own membership — a bot only sees spaces it was added to. `--page-index` here IS a real page (0-based).

```bash
octo-cli drive search --q "报销单"                              # all visible spaces
octo-cli drive search --q "预算" --scope space --space-id "$SPACE"
octo-cli drive search --q "合同" --data '{"filters":{"type":"blob","owner_scope":"me"}}'
```

Advanced `filters` (a JSON object passed via `--data`): `type` (folder|doc|blob), `owner_scope` (me|others), `updated_after` (RFC3339), `size_min`/`size_max` (bytes). Each hit carries `file_id`, `path` (breadcrumb), `type`, `highlights.{name,body}` with `<mark>` fragments, and `owner_uid`/`updater_uid` (resolve display names via the message/group commands — search returns uids, not names). Requires the server's search backend to be configured; otherwise the call returns `unavailable` (503).

## 4. Upload and download in detail

`drive upload file` runs prepare → upload → complete/confirm. The upload goes out on a separate HTTP client that carries **no** Octo credential or space header — the presigned URL is its own authorisation.

The server owns mode selection. `drive upload file` sends exactly one prepare request and always declares `X-Drive-Upload-Capabilities: multipart-v1`; the server ignores the header below its multipart threshold and returns a multipart snapshot when scheduling is required. There is no probe request or 409 retry. A multipart response enters the scheduler directly; `"single"`, a missing value, or an unknown value uses the legacy single-part path. An unknown mode without `upload_url` is `UPLOAD_MODE_CONFLICT`, with the pending row cancelled. Low-level callers can also send the header with `drive upload prepare --upload-capabilities multipart-v1`.

The multipart path honors the inherited global `--no-retry` flag:

- treats `part_size` as bytes, matching the backend response;
- opens the local file once, then streams every part with exact offsets and `Content-Length` instead of reading the file into memory;
- signs all parts in batches of at most 20, covering `1..part_count` without gaps;
- uploads with at most the server's `max_concurrent_parts` workers;
- by default, retries a failed part up to three times (four PUT attempts total); a 403 re-signs only that part. With `--no-retry`, the initial PUT still runs, but a failed attempt does not cause another PUT or signature request. If the URL has expired before the first PUT, the CLI still obtains a valid signature for that initial attempt.
- by default, completion retries an explicit HTTP 5xx within a four-attempt bound; `--no-retry` still sends the first complete request but does not repeat it.

Progress goes to stderr only. `--dry-run` describes the prepare request and local file, but fetches no URL, sends no bytes, and creates no pending row.

Both identities use the same command surface; only the credential and backend mount differ:

```bash
OCTO_TOKEN="$BOT_TOKEN" octo-cli drive upload file ./large.bin --space-id "$SPACE"
OCTO_TOKEN="$USER_API_KEY" octo-cli drive upload file ./large.bin --space-id "$SPACE"
```

If the attempt fails before completion, the CLI best-effort aborts the incomplete multipart session using a detached five-second timeout. It never aborts after a successful complete. If cleanup also fails, the message includes a recovery command:

```bash
octo-cli drive upload abort <file-id> --data '{"upload_id":"<upload-id>"}'
```

For single-part uploads, a failure after the pending row exists cancels it. The error reports the `file_id` plus the cancel outcome:

```json
{"ok":false,"error":{"code":"UPLOAD_FAILED","detail":{"file_id":"42","pending_file":"cancelled"}}}
```

If `pending_file` is not `cancelled`, run `octo-cli drive upload cancel <file-id>` yourself.

By default, explicit HTTP 5xx completion responses get a bounded retry; `--no-retry` keeps only the first complete attempt. Retry behavior alone does not establish the remote upload's final state. A connection interruption or timeout may mean the request already reached the server: that path always sends exactly one request and returns `RESULT_UNKNOWN`; the CLI does not retry or abort the session. Inspect the file's actual state before starting another upload.

A `413 file_too_large` from prepare reports the declared size and the server's effective limit; no upload bytes are sent and no pending multipart session is created.

`drive download file` and `drive share download` write to a randomly-named partial file next to the destination, fsync, then rename — an interrupted transfer never leaves a truncated file, and a pre-created symlink at a guessable path cannot redirect the write. **An existing destination is refused** unless you pass `--overwrite`. The result carries a `sha256` you can verify.

The low-level steps stay available (`upload prepare|parts-presign|complete|abort|confirm|cancel`, `download url`) if you need to drive the transfer yourself.

`drive blob create` is a different thing and is rarely what you want: it registers an object **already** in storage rather than uploading one. The backend verifies it — an `--object-path` storage does not hold is `invalid_argument`, and a `--size` that conflicts with the stored object is rejected (`--size 0` for a non-empty object included) — so it can no longer produce a row that lists fine and 404s on download. If storage is unreachable the probe is inconclusive and you get a 500 to retry, not `invalid_argument`. It still persists no download URL, so `share download` on such a row answers `not_found`. Use `drive upload file` unless the bytes are already in the bucket.

## 5. Share links

There is exactly one thing the two sides exchange: `data.share_url`.

```bash
octo-cli drive share create "$FILE" --permission download --expires-in-seconds 86400 --password-file ./pw
```

- The password is passed out of band — it is never in the URL, and it is masked in `--verbose` / `--dry-run` output.
- `--password` and `--password-file` are mutually exclusive. Prefer `--password-file <path>` (or `--password-file -` for stdin) over `--password`. A value on argv is readable from `ps` and `/proc` and lands in shell history for the process lifetime, the same reason `auth login` takes its token from `--token-file` / stdin and never from the command line. `--password` still works for interactive use. One trailing newline is stripped from the file; nothing else is, so a password may begin or end with a space.
- `--permission` is held to the spec's `view | download` enum locally: anything else is `ENUM_NOT_ALLOWED` / exit 2 with no request sent.
- **Both sides need a credential.** There is no anonymous share. The token (and password) authorise the *share*; your credential authenticates *you*. The receiver does not have to be a member of the file's space.
- `share access` / `share download` accept only links on your configured Octo origin — same scheme, host and port — in exactly the `/drive/s/<token>` or `/d/<docId>?sp=<docSpaceId>` shape. Anything else fails with `INVALID_SHARE_URL` — the CLI parses the link, it never fetches the host in it. Pass the link exactly as `share create` produced it; a scheme-downgraded copy of an otherwise correct link is refused.
- `downloadable` tells you which command to use next. Documents are always `false`; `share download` on a document link fails locally with `validation / NOT_DOWNLOADABLE` before any request. A blob share marked `false` should not be assumed to have the same local error; rely on its actual response.
- Revoke with the `share_id`, not the token: `octo-cli drive share revoke --share-id "$SHARE_ID"`. The `share_id` is itself treated as a secret — the backend returns one opaque id that is both the management handle and the access token — so `--verbose`, `--dry-run` and error output show it as `***REDACTED***`. That is the mask, not a wrong id: the real value still goes on the wire. Read it from `share create` / `share list` output, which is not masked.
- **base64url ids can start with `-`** (about one in 64), which cobra reads as a flag. Pass those as a flag instead — `octo-cli drive share revoke --share-id "$SHARE_ID"` — or put the value after a `--` separator. The same applies to `--invite-id` (`invite revoke`) and `--invite-token` (`invite accept`). Using the flag form unconditionally is the safe habit for a script.

## 6. Errors

| code | type / exit | what to do |
|---|---|---|
| `TOKEN_KIND_NOT_ALLOWED` | validation / 2 | switch credentials; do not retry |
| `ENUM_NOT_ALLOWED` | validation / 2 | the value is outside the spec's enum; the hint lists the accepted set |
| `unauthorized` | auth / 3 | token invalid, revoked, or the user/bot is inactive |
| `permission_denied` | permission / 1 | the identity lacks the space role; `drive member add` it |
| `password_required` / `wrong_password` | permission / 1 | pass or fix the password (`--password-file`) |
| `share_expired` | permission / 1 | ask for a new link |
| `not_found` | api_error / 1 | check the id and that the space is reachable |
| `conflict` | validation / 2 | re-read state, then retry |
| `invalid_argument` | validation / 2 | check the schema: `octo-cli schema drive.<op>` |
| `FILE_EXISTS` | validation / 2 | pass `--overwrite` or pick another path |
| `NOT_DOWNLOADABLE` | validation / 2 | a document link; use `share access` or a browser |
| `INVALID_SHARE_URL` | validation / 2 | pass the `share_url` exactly as produced |
| `MISSING_DOC_SPACE_ID` | validation / 2 | re-mount the document; never substitute the drive space id |
| `UNSAFE_PRESIGNED_URL` | api_error / 1 | the backend returned an unusable URL, or one whose host resolves to this machine; report it |
| `TRANSFER_REDIRECTED_LOCALLY` | validation / 2 | the transfer was rerouted to this machine but the URL does not name it; check `http_proxy` / `https_proxy` / `all_proxy` / `no_proxy` |
| `INVALID_PROXY` | validation / 2 | the proxy value in this environment is not a usable URL. The value is never echoed, because a proxy URL often carries credentials |
| `PARTIAL_FILE_REPLACED` | validation / 2 | something replaced the part file or the destination mid-download; nothing was published, re-run in a directory only you can write |
| `UPLOAD_FAILED` | api_error / 1 | the presigned PUT was rejected or the confirm failed; the detail carries `file_id` and whether the pending row was cancelled |
| `file_too_large` | validation / 2 | the declared size exceeds the server's effective limit; the message includes that limit. Do not retry unchanged |
| `client_capability_required` | validation / 2 | the server requires a declared upload capability; `upload file` always declares it on its single prepare request. Low-level prepare can pass `--upload-capabilities multipart-v1` |
| `upload_mode_conflict` | validation / 2 | follow the server's `upload_mode` and use the matching single-part or multipart flow |
| `object_size_mismatch` | validation / 2 | compare `actual_size` with `declared_size`; restart the upload with the complete file |
| `upload_session_not_active` | validation / 2 | read `status`: `completing` is not active; treat `confirmed` as success, start a new session if aborted or expired, and never replay the same `upload_id` |
| `upload_policy_unavailable` | api_error / 1 | wait for `retry_after_seconds`, then retry with bounded backoff |
| `too_many_upload_sessions` | rate_limited / 1 | finish or abort existing sessions to free a slot, then retry |
| `RESULT_UNKNOWN` | network / 1 | completion may have succeeded. Inspect the file before retrying; the CLI deliberately does not abort or replay this request |
| `INVALID_FLAG` | validation / 2 | a positional id starting with `-` was parsed as a flag; pass it as the named flag or after `--` |

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
drive search
drive space       create | list | ensure-personal | get | rename | delete
drive member      list | add | set-role | remove
drive folder      create | list | rename | move | delete
drive file        get | move | copy | rename
drive blob        create | get | list | delete
drive upload      file | prepare | parts-presign | complete | abort | confirm | cancel
drive download    file | url
drive doc         mount | unmount | list | candidates
drive share       create | blob-create | list | revoke | access | download
drive invite      create | list | revoke | accept
drive im-transfer create
```

Per-command flags: `octo-cli drive <group> <verb> --help`, or `octo-cli schema drive.<group>.<verb>` for the wire contract.
