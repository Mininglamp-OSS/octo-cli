# Octo CLI — Complete Command Reference

> Source: dmworkim develop + matters main, handler-level verification
> Reviewer: 齐静春（架构师）
> Date: 2026-07-21

---

## Bot Type Reference

| Token Prefix | Type | Capabilities |
|-------------|------|-------------|
| `app_*` | App Bot | DM only, no group/thread write, no voice; **cannot search** (CLI rejects locally) |
| `bf_*` | User Bot | Full access (DM + group + thread + voice); can search (as bot, or as a person via `--on-behalf-of`) |
| `uk_*` | User API key | Real-person identity, used for `message search` and all of `drive`; routed to `/v1/user/*`. `bot_kind` shows `user_key` |

> The CLI recognizes the prefix for display only (`octo-cli config show` → `bot_kind`); it does **not** gate commands by bot kind, with two exceptions: an `app_*` token running `message search`, and a credential whose kind a domain's spec does not list in `x-octo-allowed-token-kinds` (`TOKEN_KIND_NOT_ALLOWED`). Both are local `validation` errors (exit 2) raised before the request. Otherwise an App Bot calling a User-Bot-only command sends the request and gets a server-side `FORBIDDEN`.

> **Token source.** The token comes from a stored profile, else `OCTO_TOKEN`, else `OCTO_BOT_TOKEN`. `OCTO_TOKEN` is the preferred env slot and accepts any of the three kinds; `identity.source` in the success envelope names the variable actually used.

---

## Domain 1: matter (matters service, 17 commands)

> Note: "17 commands" counts the CLI command surface, which includes convenience
> aliases (e.g. `close`/`reopen`/`archive` over the `transition` op). The backend
> operationId count is 14 (as in README / CLAUDE.md / the spec registry).

| # | Command | Method | Path | App Bot | User Bot |
|---|---------|--------|------|---------|----------|
| 1 | `octo-cli matter create` | POST | /api/v1/matters | Y | Y |
| 2 | `octo-cli matter list` | GET | /api/v1/matters | Y | Y |
| 3 | `octo-cli matter get <id>` | GET | /api/v1/matters/:id | Y | Y |
| 4 | `octo-cli matter update <id>` | PUT | /api/v1/matters/:id | Y | Y |
| 5 | `octo-cli matter delete <id>` | DELETE | /api/v1/matters/:id | Y | Y |
| 6 | `octo-cli matter transition <id>` | PUT | /api/v1/matters/:id/status | Y | Y |
| 7 | `octo-cli matter close <id>` | alias | → transition {status:done} | Y | Y |
| 8 | `octo-cli matter reopen <id>` | alias | → transition {status:open} | Y | Y |
| 9 | `octo-cli matter archive <id>` | alias | → transition {status:archived} | Y | Y |
| 10 | `octo-cli matter extract` | POST | /api/v1/matters/extract | Y | Y |
| 11 | `octo-cli matter assignee add <id>` | POST | /api/v1/matters/:id/assignees | Y | Y |
| 12 | `octo-cli matter assignee remove <id> <uid>` | DELETE | /api/v1/matters/:id/assignees/:uid | Y | Y |
| 13 | `octo-cli matter channel link <id>` | POST | /api/v1/matters/:id/channels | Y | Y |
| 14 | `octo-cli matter channel unlink <id> <ch_id>` | DELETE | /api/v1/matters/:id/channels/:channel_id | Y | Y |
| 15 | `octo-cli matter timeline add <id>` | POST | /api/v1/matters/:id/timeline | Y | Y |
| 16 | `octo-cli matter timeline list <id>` | GET | /api/v1/matters/:id/timeline | Y | Y |
| 17 | `octo-cli matter timeline delete <id> <entry>` | DELETE | /api/v1/matters/:id/timeline/:entry_id | Y | Y |

Flags (from handler req structs):
- create: --title*(max500), --description(max10000), --assignee(str[]), --deadline(RFC3339), --remind-at(RFC3339), --source-channel, --source-type(1|2|5), --source-name
- list: --status(open|done|archived), --assignee(str,supports "me"), --creator, -q(search), --source-channel, --source-type, --channel, --limit(default:20,max:100), --cursor
- update: --title, --description, --deadline, --remind-at
- extract: --data (complex nested body: channel_type, channel_id, creator_uid, msgs[])
- timeline add: --content(max10000), --data (for attachments/msgs/channel context)
- channel link: --channel-id*, --channel-type*(1|2|5), --channel-name
- assignee add: --user-id*

Notes:
- matters service has its own auth (→ dmworkim verify-bot). Both bot types supported.
- Status transitions: no state machine, any → any valid.
- extract: bot can only act on behalf of owner (creator_uid = bot owner_uid).
- assignee_id=me alias: server resolves to caller UID.
- Pagination: {data:[], pagination:{has_more, next_cursor}}.

---

## Domain 2: message (dmworkim bot_api, 10 commands)

| # | Command | Method | Path | App Bot | User Bot |
|---|---------|--------|------|---------|----------|
| 18 | `octo-cli message send` | POST | /v1/bot/sendMessage | Y (DM only) | Y (DM+group+thread) |
| 19 | `octo-cli message edit` | POST | /v1/bot/message/edit | Y | Y |
| 20 | `octo-cli message sync` | POST | /v1/bot/messages/sync | Y (DM only) | Y (DM+group) |
| 21 | `octo-cli message read-receipt` | POST | /v1/bot/readReceipt | Y | Y |
| 21a | `octo-cli message search` | POST | /v1/bot/messages/_search | **N** | Y |
| 21b | `octo-cli message search all` | POST | /v1/bot/messages/_search_all | **N** | Y |
| 21c | `octo-cli message search files` | POST | /v1/bot/messages/_search_files | **N** | Y |
| 21d | `octo-cli message search media` | POST | /v1/bot/messages/_search_media | **N** | Y |
| 21e | `octo-cli message search around` | POST | /v1/bot/messages/_search_around | **N** | Y |
| 21f | `octo-cli message search groups` | POST | /v1/bot/messages/_search_global_groups | **N** | Y |

Flags:
- send: --channel-id*, --channel-type*(uint8), --stream-no, --on-behalf-of; payload (object) via --data
- edit: --message-id*, --message-seq, --channel-id*, --channel-type*, --content-edit*
- sync: --data (JSON body with channel_id, channel_type, etc.)
- read-receipt: --channel-id*, --channel-type(default:1), --message-ids*(str[])
- search / search all / search files: --chat-id (optional; omit → cross-channel `_search_global_*`), --channel-type, --keyword, --sort(time_desc|time_asc|relevance), --page-size, --on-behalf-of; extra `filters` via --data. Paginated (cursor in body; `--page-all`).
- search media: --chat-id* (required; in-channel only), --channel-type, --sort, --page-size, --on-behalf-of; **no** --keyword. Paginated.
- search around: --chat-id* (required), --channel-type, --anchor-message-id*, --on-behalf-of; extra `filters` via --data. Not paginated.
- search groups: --keyword, --on-behalf-of; extra `filters` via --data. **--chat-id not supported** (cross-channel only). Not paginated.

Notes:
- App Bot sendMessage: checkSendPermission enforces channelType=1 (DM only), requires friend relationship.
- User Bot sendMessage: DM + group (membership check) + thread (parent group membership check).
- payload is map[string]interface{} — object fields aren't promoted to flags, so it goes inside --data JSON. payload.type is an integer code (1=Text…; see common.ContentType / octo-messaging skill).
- sync: App Bot explicitly blocked for group channel type.
- search: `--chat-id` decides in-channel vs cross-channel. Without it, `search`/`all`/`files` route (CLI-side, `internal/client/search_route.go`) to `_search_global_messages` / `_search_global_files`; plain `search` cross-channel becomes a **mixed messages+files** feed. `media`/`around` require `--chat-id`; `groups` forbids it. `app_` tokens are rejected locally; `uk_` tokens are rewritten to `/v1/user/messages/*`.

---

## Domain 3: group (dmworkim bot_api, 9 commands)

| # | Command | Method | Path | App Bot | User Bot |
|---|---------|--------|------|---------|----------|
| 22 | `octo-cli group list` | GET | /v1/bot/groups | Y | Y |
| 23 | `octo-cli group get <group_no>` | GET | /v1/bot/groups/:group_no | Y | Y |
| 24 | `octo-cli group members <group_no>` | GET | /v1/bot/groups/:group_no/members | Y | Y |
| 25 | `octo-cli group md-get <group_no>` | GET | /v1/bot/groups/:group_no/md | Y | Y |
| 26 | `octo-cli group md-update <group_no>` | PUT | /v1/bot/groups/:group_no/md | Y | Y |
| 27 | `octo-cli group create` | POST | /v1/bot/createGroup | **N** | Y |
| 28 | `octo-cli group update <group_no>` | PUT | /v1/bot/groups/:group_no/info | **N** | Y |
| 29 | `octo-cli group member-add <group_no>` | POST | /v1/bot/groups/:group_no/members/add | **N** | Y |
| 30 | `octo-cli group member-remove <group_no>` | POST | /v1/bot/groups/:group_no/members/remove | **N** | Y |

Flags:
- list: --space-id (query param, optional filter)
- create: --name, --members(str[]), --creator, --space-id (via --data JSON)
- update: --data (JSON body for group info fields)
- member-add: --data (JSON body with member UIDs)
- member-remove: --data (JSON body with member UIDs)
- md-update: --content* (markdown string)

Notes:
- #27-30: handler checks `getBotKindFromContext(c) == BotKindApp` → rejects with "app bot does not support group operations".
- #22-26: read operations have no bot kind check, only membership check.
- User Bot group operations require bot to be a group member.

---

## Domain 4: thread (dmworkim bot_api, 8 commands)

| # | Command | Method | Path | App Bot | User Bot |
|---|---------|--------|------|---------|----------|
| 31 | `octo-cli thread create <group_no>` | POST | /v1/bot/groups/:group_no/threads | **N** | Y |
| 32 | `octo-cli thread list <group_no>` | GET | /v1/bot/groups/:group_no/threads | **N** | Y |
| 33 | `octo-cli thread get <group_no> <short_id>` | GET | .../:short_id | **N** | Y |
| 34 | `octo-cli thread members <group_no> <short_id>` | GET | .../:short_id/members | **N** | Y |
| 35 | `octo-cli thread join <group_no> <short_id>` | POST | .../:short_id/join | **N** | Y |
| 36 | `octo-cli thread leave <group_no> <short_id>` | POST | .../:short_id/leave | **N** | Y |
| 37 | `octo-cli thread md-get <group_no> <short_id>` | GET | .../:short_id/md | **N** | Y |
| 38 | `octo-cli thread md-update <group_no> <short_id>` | PUT | .../:short_id/md | **N** | Y |

Flags:
- create: --name* (thread name), --data (additional fields)
- md-update: --content* (markdown string)

Notes:
- ALL thread handlers call validateBotGroupAccess() which rejects App Bot: "app bot does not support group operations".
- User Bot requires parent group membership.

---

## Domain 5: file (dmworkim bot_api, 4 commands)

| # | Command | Method | Path | App Bot | User Bot |
|---|---------|--------|------|---------|----------|
| 39 | `octo-cli file upload` | POST | /v1/bot/file/upload | Y | Y |
| 40 | `octo-cli file download <path>` | GET | /v1/bot/file/download/*path | Y | Y |
| 41 | `octo-cli file credentials` | GET | /v1/bot/upload/credentials | Y | Y |
| 42 | `octo-cli file presigned` | GET | /v1/bot/upload/presigned | Y | Y |

Flags:
- upload: --file* (multipart), --type(default:"chat"), --path
- credentials: --filename* (query)
- presigned: --filename*, --fileSize* (query; fileSize in bytes is REQUIRED)

Notes:
- Upload is multipart form, not JSON body — special handling in engine.
- /v1/bot/upload is duplicate path for same handler, CLI maps only /v1/bot/file/upload.
- No bot kind restrictions on any file operation.

---

## Domain 6: bot (dmworkim bot_api, 6 commands)

| # | Command | Method | Path | App Bot | User Bot |
|---|---------|--------|------|---------|----------|
| 43 | `octo-cli bot register` | POST | /v1/bot/register | Y | Y |
| 44 | `octo-cli bot set-commands` | POST | /v1/bot/setCommands | Y | Y |
| 45 | `octo-cli bot user-info` | GET | /v1/bot/user/info | Y | Y |
| 46 | `octo-cli bot space-members` | GET | /v1/bot/space/members | Y | Y |
| 47 | `octo-cli bot typing` | POST | /v1/bot/typing | Y | Y |
| 48 | `octo-cli bot heartbeat` | POST | /v1/bot/heartbeat | Y | Y |

Flags:
- register: --data (JSON, registration payload)
- set-commands: --data (JSON, body shape: {"commands":[{"command","description"}]})
- user-info: --uid* (query, required)
- typing: --channel-id*, --channel-type*, --on-behalf-of

Notes:
- register: NOT behind authBot middleware. Routes by token prefix (app_ → registerAppBot, bf_ → registerUserBot). Both supported.

---

## Domain 7: event (dmworkim bot_api, 2 commands)

| # | Command | Method | Path | App Bot | User Bot |
|---|---------|--------|------|---------|----------|
| 49 | `octo-cli event list` | POST | /v1/bot/events | Y | Y |
| 50 | `octo-cli event ack <event_id>` | POST | /v1/bot/events/:event_id/ack | Y | Y |

Flags:
- list: --event-id (int64, start cursor), --limit (int64, default:20, max:100)

Notes:
- event list uses POST (body carries event_id cursor), not GET.

---

## Domain 8: drive (octo-drive, 45 commands)

All three token kinds are accepted. The mount is chosen from the kind, so the
command surface is identical either way: `uk_*` → `/v1/user/drive/*` (acts as the
real person), `bf_*` / `app_*` → `/v1/bot/drive/*` (acts as the bot). Paths below
are written with `{mount}` standing for whichever applies.

A bot has no implicit access to a shared drive space — it must be added as a
member exactly like a person, and gets `permission_denied` until it is. Drive
never sends `X-Space-Id`; the tenant comes from the verified identity.

`<file-id>` and `--parent-id` are backend **uint64** values. They are decimal
strings on the CLI surface (validated in `[0, 2^64-1]`, sent as JSON integers,
returned as decimal strings) so a value above 2^53 cannot be rounded by a
JavaScript-style parser into a different file's id.

| # | Command | Method | Path | Risk |
|---|---------|--------|------|------|
| 51 | `octo-cli drive space create` | POST | {mount}/spaces | write |
| 52 | `octo-cli drive space list` | GET | {mount}/spaces | read |
| 53 | `octo-cli drive space ensure-personal` | POST | {mount}/spaces/personal | write (idempotent) |
| 54 | `octo-cli drive space get <space-id>` | GET | {mount}/spaces/:id | read |
| 55 | `octo-cli drive space rename <space-id>` | PUT | {mount}/spaces/:id | write |
| 56 | `octo-cli drive space delete <space-id>` | DELETE | {mount}/spaces/:id | high-risk-write |
| 57 | `octo-cli drive member list <space-id>` | GET | {mount}/spaces/:id/members | read |
| 58 | `octo-cli drive member add <space-id>` | POST | {mount}/spaces/:id/members | write |
| 59 | `octo-cli drive member set-role <space-id> <uid>` | PUT | {mount}/spaces/:id/members/:uid | write |
| 60 | `octo-cli drive member remove <space-id> <uid>` | DELETE | {mount}/spaces/:id/members/:uid | high-risk-write |
| 61 | `octo-cli drive browse` | GET | {mount}/browse | read |
| 62 | `octo-cli drive folder create` | POST | {mount}/folders | write |
| 63 | `octo-cli drive folder list <space-id> <parent-id>` | GET | {mount}/folders/:space_id/:parent_id | read |
| 64 | `octo-cli drive folder rename <folder-id>` | PATCH | {mount}/folders/:id/rename | write |
| 65 | `octo-cli drive folder move <folder-id>` | PATCH | {mount}/folders/:id/move | write |
| 66 | `octo-cli drive folder delete <folder-id>` | DELETE | {mount}/folders/:id | high-risk-write |
| 67 | `octo-cli drive file get <file-id>` | GET | {mount}/files/:id | read |
| 68 | `octo-cli drive file move <file-id>` | POST | {mount}/files/:id/move | write |
| 69 | `octo-cli drive file copy <file-id>` | POST | {mount}/files/:id/copy | write |
| 70 | `octo-cli drive file rename <file-id>` | POST | {mount}/files/:id/rename | write |
| 71 | `octo-cli drive blob create` | POST | {mount}/blobs | write |
| 72 | `octo-cli drive blob get <blob-id>` | GET | {mount}/blobs/:id | read |
| 73 | `octo-cli drive blob list` | GET | {mount}/blobs | read |
| 74 | `octo-cli drive blob delete <blob-id>` | DELETE | {mount}/blobs/:id | high-risk-write |
| 75 | `octo-cli drive upload prepare` | POST | {mount}/files/prepare-upload | write |
| 76 | `octo-cli drive upload confirm <file-id>` | POST | {mount}/files/:id/confirm-upload | write |
| 77 | `octo-cli drive upload cancel <file-id>` | POST | {mount}/files/:id/cancel-upload | write (idempotent) |
| 78 | `octo-cli drive upload file <local-path>` | — | composite: prepare → PUT → confirm | write |
| 79 | `octo-cli drive download url <file-id>` | GET | {mount}/files/:id/download | read |
| 80 | `octo-cli drive download file <file-id>` | — | composite: download-url → object GET | read + local write |
| 81 | `octo-cli drive doc mount` | POST | {mount}/docs | write |
| 82 | `octo-cli drive doc unmount <file-id>` | DELETE | {mount}/docs/:id | high-risk-write |
| 83 | `octo-cli drive doc list` | GET | {mount}/docs | read |
| 84 | `octo-cli drive doc candidates` | GET | {mount}/mountable-docs | read |
| 85 | `octo-cli drive share create <file-id>` | — | composite: file get → blob token or doc link | write |
| 86 | `octo-cli drive share blob-create <file-id>` | POST | {mount}/shares | write |
| 87 | `octo-cli drive share list` | GET | {mount}/shares | read |
| 88 | `octo-cli drive share revoke <share-id>` | DELETE | {mount}/shares/:id | high-risk-write |
| 89 | `octo-cli drive share access <share-url>` | POST | {mount}/shares/:token/access | read |
| 90 | `octo-cli drive share download <share-url>` | POST | {mount}/shares/:token/download | read + local write |
| 91 | `octo-cli drive invite create <space-id>` | POST | {mount}/spaces/:id/invites | write |
| 92 | `octo-cli drive invite list <space-id>` | GET | {mount}/spaces/:id/invites | read |
| 93 | `octo-cli drive invite revoke <space-id> <invite-id>` | DELETE | {mount}/spaces/:id/invites/:invite_id | high-risk-write |
| 94 | `octo-cli drive invite accept <invite-token>` | POST | {mount}/invites/:token/accept | write (idempotent) |
| 95 | `octo-cli drive im-transfer create` | POST | {mount}/blobs/transfer-from-im | write (idempotent) |

Flags:
- space create / rename: --name
- space list: --page-index, --page-size
- member add: --uid, --role; member set-role: --role
  (role ∈ preview_only | downloader | uploader_downloader | editor | admin | custom; `super_admin` is server-rejected — it is bound to the space creator at space creation. `custom` is accepted here but rejected by `invite create`; see the role matrix below)
- browse: --space-id (required), --parent-id, --type (all|doc|blob|folder), --source (all|user-upload|im-transfer|user-mount|docs-sync), --page-index, --page-size
- folder create: --space-id, --parent-id, --name; folder rename: --name; folder move: --parent-id
- file move: --parent-id; file copy: --parent-id, --name; file rename: --name
- blob create: --space-id, --parent-id, --name, --object-path, --size, --content-type, --source (user-mount is Type-1 only). The backend **verifies the object**: an `--object-path` storage does not hold is `invalid_argument`, and a `--size` that conflicts with the stored object is rejected (including `--size 0` for a non-empty object — 0 is a stated count, not an omission). An inconclusive probe (storage unreachable/timeout) surfaces as a 500, not `invalid_argument`. A row created this way carries no persisted download URL, so `share download` on it is `not_found` — use `upload file` for a shareable blob.
- blob list: --space-id (required), --parent-id
- upload prepare: --space-id, --parent-id, --name, --size, --content-type; upload confirm: --actual-size
- upload file: --space-id (required), --parent-id, --name, --content-type
- download file: --output/-o (required), --overwrite
- doc mount: --space-id, --parent-id, --doc-id, --source (user-mount|docs-sync; no --doc-title: the title and doc_space_id are read server-side from the document metadata)
- doc list: --space-id (required), --parent-id; doc candidates: --space-id (required), --page, --page-size
- share create / blob-create: --permission (view|download), --expires-in-seconds, --password
- share access: --password; share download: --output/-o (required), --password, --overwrite
- invite create: --role (required, role ∈ preview_only | downloader | uploader_downloader | editor | admin — `custom` and `super_admin` are rejected), --expires-in-seconds
- im-transfer create: --im-group-no (required), --im-channel-type (required, 1=DM|2=group|5=thread; picks the message-read route — 1 is the DM route, 2 and 5 share the group route — and is stored as the first segment of the row's source_key, which the already-transferred batch lookup matches on. Transfer idempotency is (target space, type=blob, object path), so a wrong value cannot duplicate a file), --im-msg-id (required), --target-space-id (required), --target-parent-id, --name-override

Role grantability matrix (the enum is one shared schema; the accepted subset is not):

| role | member add / set-role | invite create | notes |
|---|---|---|---|
| `preview_only` | ✅ | ✅ | rank 20 |
| `downloader` | ✅ | ✅ | rank 30 |
| `uploader_downloader` | ✅ | ✅ | rank 40 |
| `editor` | ✅ | ✅ | rank 60 |
| `admin` | ✅ super_admin only | ✅ super_admin only | rank 80; an admin cannot grant admin |
| `custom` | ✅ | ❌ | rank 10 — lowest; carries no invite-link semantics |
| `super_admin` | ❌ | ❌ | rank 100; bound to the creator at space creation |

Notes:
- **Share hand-over is the `share_url`, nothing else.** `share create` returns
  `/drive/s/<token>` for a blob or `/d/<docId>?sp=<docSpaceId>` for a mounted
  document; the receiver passes that link straight to `share access` /
  `share download`. The token is never a caller-facing parameter.
- `share access` / `share download` parse the link and refuse anything that is
  not one of those two shapes on the configured Octo origin. The CLI never
  fetches the link's host — it calls the configured API with the parsed token.
  Both sides need a credential; there is no anonymous share.
- `doc_space_id` (the document's own Octo Space) and `space_id` (the drive space)
  are different scopes. When a mount predates drive capturing `doc_space_id`,
  `share create` fails with `MISSING_DOC_SPACE_ID` rather than substituting.
- The presigned PUT/GET runs on a separate HTTP client with **no** Octo
  credential and no space header. A failed `upload file` cancels the pending row
  and reports `file_id` + the cancel outcome in the error detail.
- `download file` / `share download` write `<output>.part`, fsync, then rename;
  an existing destination is refused unless `--overwrite` is passed.
- `--password`, share tokens and invite tokens are marked `x-octo-secret`: they
  go on the wire unchanged but are masked in `--verbose` and `--dry-run` output.
- **base64url ids may start with `-`**, which cobra parses as a flag before the
  command runs. The three affected path params (`share_id`, `invite_id`,
  `invite_token`) declare `x-octo-flag`, so each also accepts a flag form —
  `drive share revoke --share-id <id>`, `drive invite revoke <space> --invite-id
  <id>`, `drive invite accept --invite-token <tok>`. The `--` separator still
  works. Positional parsing is unchanged for every other operation: only a path
  param that declares `x-octo-flag` relaxes its command's arity, and the flag is
  optional. Supplying both forms for the same slot is a validation error rather
  than a silent winner.
- `browse` returns the full listing; its `page` object is an envelope, not a
  database page, so `--page-all` is deliberately not offered yet.
- There are no `drive org` commands: the product's member picker is a frontend
  filter over the space roster, not a backend search.

---

## Summary

| Domain | Commands | App Bot (Y) | App Bot (N) | User Bot (Y) | User Bot (N) |
|--------|----------|-------------|-------------|---------------|--------------|
| matter | 17 | 17 | 0 | 17 | 0 |
| message | 10 | 4 (DM only) | 6 (search) | 10 | 0 |
| group | 9 | 5 | 4 | 9 | 0 |
| thread | 8 | 0 | 8 | 8 | 0 |
| file | 4 | 4 | 0 | 4 | 0 |
| bot | 6 | 6 | 0 | 6 | 0 |
| event | 2 | 2 | 0 | 2 | 0 |
| drive | 45 | 45 (needs a resolvable space) | 0 | 45 | 0 |
| **Total** | **101** | **83** | **18** | **101** | **0** |

- **App Bot**: 83/101 commands available (82%)
- **User Bot**: 101/101 commands available (100%)
- **App Bot blocked**: group write (4) + thread all (8) + message search (6) = 18 commands
- **drive** additionally accepts a `uk_*` user API key, which acts as the real
  person rather than a bot. An `app_*` token reaches drive only if the server can
  resolve a space for it; otherwise the mount returns 401 (exit 3) and a `bf_*`
  or `uk_*` credential is required.

> This reference predates the `docs`, `html`, `marketplace` and `summary`
> domains and does not cover them. `octo-cli schema --list` is the authoritative,
> always-current inventory.
