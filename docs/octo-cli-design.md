# Octo CLI — Complete Command Reference

> Source: dmworkim develop + matters main, handler-level verification
> Reviewer: 齐静春（架构师）
> Date: 2026-05-10

---

## Bot Type Reference

| Token Prefix | Type | Capabilities |
|-------------|------|-------------|
| `app_*` | App Bot | DM only, no group/thread write, no voice |
| `bf_*` | User Bot | Full access (DM + group + thread + voice) |

> The CLI recognizes the prefix for display only (`octo config show` → `bot_kind`); it does **not** gate commands by bot kind. An App Bot calling a User-Bot-only command sends the request and gets a server-side `FORBIDDEN`.

---

## Domain 1: matter (matters service, 17 commands)

| # | Command | Method | Path | App Bot | User Bot |
|---|---------|--------|------|---------|----------|
| 1 | `octo matter create` | POST | /api/v1/matters | Y | Y |
| 2 | `octo matter list` | GET | /api/v1/matters | Y | Y |
| 3 | `octo matter get <id>` | GET | /api/v1/matters/:id | Y | Y |
| 4 | `octo matter update <id>` | PUT | /api/v1/matters/:id | Y | Y |
| 5 | `octo matter delete <id>` | DELETE | /api/v1/matters/:id | Y | Y |
| 6 | `octo matter transition <id>` | PUT | /api/v1/matters/:id/status | Y | Y |
| 7 | `octo matter close <id>` | alias | → transition {status:done} | Y | Y |
| 8 | `octo matter reopen <id>` | alias | → transition {status:open} | Y | Y |
| 9 | `octo matter archive <id>` | alias | → transition {status:archived} | Y | Y |
| 10 | `octo matter extract` | POST | /api/v1/matters/extract | Y | Y |
| 11 | `octo matter assignee add <id>` | POST | /api/v1/matters/:id/assignees | Y | Y |
| 12 | `octo matter assignee remove <id> <uid>` | DELETE | /api/v1/matters/:id/assignees/:uid | Y | Y |
| 13 | `octo matter channel link <id>` | POST | /api/v1/matters/:id/channels | Y | Y |
| 14 | `octo matter channel unlink <id> <ch_id>` | DELETE | /api/v1/matters/:id/channels/:channel_id | Y | Y |
| 15 | `octo matter timeline add <id>` | POST | /api/v1/matters/:id/timeline | Y | Y |
| 16 | `octo matter timeline list <id>` | GET | /api/v1/matters/:id/timeline | Y | Y |
| 17 | `octo matter timeline delete <id> <entry>` | DELETE | /api/v1/matters/:id/timeline/:entry_id | Y | Y |

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

## Domain 2: message (dmworkim bot_api, 4 commands)

| # | Command | Method | Path | App Bot | User Bot |
|---|---------|--------|------|---------|----------|
| 18 | `octo message send` | POST | /v1/bot/sendMessage | Y (DM only) | Y (DM+group+thread) |
| 19 | `octo message edit` | POST | /v1/bot/message/edit | Y | Y |
| 20 | `octo message sync` | POST | /v1/bot/messages/sync | Y (DM only) | Y (DM+group) |
| 21 | `octo message read-receipt` | POST | /v1/bot/readReceipt | Y | Y |

Flags:
- send: --channel-id*, --channel-type*(uint8), --stream-no, --on-behalf-of; payload (object) via --data
- edit: --message-id*, --message-seq, --channel-id*, --channel-type*, --content-edit*
- sync: --data (JSON body with channel_id, channel_type, etc.)
- read-receipt: --channel-id*, --channel-type(default:1), --message-ids*(str[])

Notes:
- App Bot sendMessage: checkSendPermission enforces channelType=1 (DM only), requires friend relationship.
- User Bot sendMessage: DM + group (membership check) + thread (parent group membership check).
- payload is map[string]interface{} — object fields aren't promoted to flags, so it goes inside --data JSON. payload.type is an integer code (1=Text…; see common.ContentType / octo-messaging skill).
- sync: App Bot explicitly blocked for group channel type.

---

## Domain 3: group (dmworkim bot_api, 9 commands)

| # | Command | Method | Path | App Bot | User Bot |
|---|---------|--------|------|---------|----------|
| 22 | `octo group list` | GET | /v1/bot/groups | Y | Y |
| 23 | `octo group get <group_no>` | GET | /v1/bot/groups/:group_no | Y | Y |
| 24 | `octo group members <group_no>` | GET | /v1/bot/groups/:group_no/members | Y | Y |
| 25 | `octo group md-get <group_no>` | GET | /v1/bot/groups/:group_no/md | Y | Y |
| 26 | `octo group md-update <group_no>` | PUT | /v1/bot/groups/:group_no/md | Y | Y |
| 27 | `octo group create` | POST | /v1/bot/createGroup | **N** | Y |
| 28 | `octo group update <group_no>` | PUT | /v1/bot/groups/:group_no/info | **N** | Y |
| 29 | `octo group member-add <group_no>` | POST | /v1/bot/groups/:group_no/members/add | **N** | Y |
| 30 | `octo group member-remove <group_no>` | POST | /v1/bot/groups/:group_no/members/remove | **N** | Y |

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

## Domain 4: thread (dmworkim bot_api, 9 commands)

| # | Command | Method | Path | App Bot | User Bot |
|---|---------|--------|------|---------|----------|
| 31 | `octo thread create <group_no>` | POST | /v1/bot/groups/:group_no/threads | **N** | Y |
| 32 | `octo thread list <group_no>` | GET | /v1/bot/groups/:group_no/threads | **N** | Y |
| 33 | `octo thread get <group_no> <short_id>` | GET | .../:short_id | **N** | Y |
| 34 | `octo thread delete <group_no> <short_id>` | DELETE | .../:short_id | **N** | Y |
| 35 | `octo thread members <group_no> <short_id>` | GET | .../:short_id/members | **N** | Y |
| 36 | `octo thread join <group_no> <short_id>` | POST | .../:short_id/join | **N** | Y |
| 37 | `octo thread leave <group_no> <short_id>` | POST | .../:short_id/leave | **N** | Y |
| 38 | `octo thread md-get <group_no> <short_id>` | GET | .../:short_id/md | **N** | Y |
| 39 | `octo thread md-update <group_no> <short_id>` | PUT | .../:short_id/md | **N** | Y |

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
| 40 | `octo file upload` | POST | /v1/bot/file/upload | Y | Y |
| 41 | `octo file download <path>` | GET | /v1/bot/file/download/*path | Y | Y |
| 42 | `octo file credentials` | GET | /v1/bot/upload/credentials | Y | Y |
| 43 | `octo file presigned` | GET | /v1/bot/upload/presigned | Y | Y |

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
| 44 | `octo bot register` | POST | /v1/bot/register | Y | Y |
| 45 | `octo bot set-commands` | POST | /v1/bot/setCommands | Y | Y |
| 46 | `octo bot user-info` | GET | /v1/bot/user/info | Y | Y |
| 47 | `octo bot space-members` | GET | /v1/bot/space/members | Y | Y |
| 48 | `octo bot typing` | POST | /v1/bot/typing | Y | Y |
| 49 | `octo bot heartbeat` | POST | /v1/bot/heartbeat | Y | Y |

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
| 50 | `octo event list` | POST | /v1/bot/events | Y | Y |
| 51 | `octo event ack <event_id>` | POST | /v1/bot/events/:event_id/ack | Y | Y |

Flags:
- list: --event-id (int64, start cursor), --limit (int64, default:20, max:100)

Notes:
- event list uses POST (body carries event_id cursor), not GET.

---

## Summary

| Domain | Commands | App Bot (Y) | App Bot (N) | User Bot (Y) | User Bot (N) |
|--------|----------|-------------|-------------|---------------|--------------|
| matter | 17 | 17 | 0 | 17 | 0 |
| message | 4 | 4 (DM only) | 0 | 4 | 0 |
| group | 9 | 5 | 4 | 9 | 0 |
| thread | 9 | 0 | 9 | 9 | 0 |
| file | 4 | 4 | 0 | 4 | 0 |
| bot | 6 | 6 | 0 | 6 | 0 |
| event | 2 | 2 | 0 | 2 | 0 |
| **Total** | **51** | **38** | **13** | **51** | **0** |

- **App Bot**: 38/51 commands available (75%)
- **User Bot**: 51/51 commands available (100%)
- **App Bot blocked**: group write (4) + thread all (9) = 13 commands
