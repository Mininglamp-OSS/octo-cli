---
name: octo-messaging
version: 0.4.1
description: Messaging domain — send/edit/sync messages, read receipts, message search (search/all/files/media/around/groups, in-channel or cross-channel), groups and threads (User Bot), and event polling. Covers App Bot DM-only constraints. Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-messaging — messages, groups, threads, events

Three related domains live here. They all call `$OCTO_API_BASE_URL/v1/bot/*` — except the `message search` family under a `uk_` (user API key) token, which the CLI routes to `/v1/user/*` (real-person mount; see the `message search` section).

| Domain    | App Bot           | User Bot |
|-----------|-------------------|----------|
| `message` | yes (DM only)     | yes (DM + group + thread) |
| `group`   | read-only         | full     |
| `thread`  | **blocked**       | full     |
| `event`   | yes               | yes      |

Search capability (the `message search` family) is a separate axis by token kind:

| Token   | Can search? | Notes                                                  |
|---------|-------------|--------------------------------------------------------|
| `app_*` | **no**      | CLI rejects locally with a `validation` error (not a server `FORBIDDEN`). |
| `bf_*`  | yes         | Searches as the bot; add `--on-behalf-of <uid>` for a real-person (OBO) view. |
| `uk_*`  | yes         | Real-person identity; routed to `/v1/user/*`.          |

Before attempting a write, confirm the token type with `octo-cli config show` — App Bot writes outside DM get `FORBIDDEN` from the server ("app bot does not support group operations").

## 1. `message` — 4 core commands (+ 6 search subcommands, see below)

```bash
# Send a message. payload is a JSON object (not a flag) — pass it inside --data.
# payload.type is an INTEGER code (1=text); see the payload table below.
octo-cli message send --channel-id <cid> --channel-type 1 \
  --data '{"payload":{"type":1,"content":"hello"}}' \
  [--stream-no <n>] [--on-behalf-of <uid>]

# Edit by (message-id, channel-id, channel-type). content-edit is the new
# content, stored opaquely server-side — carry the new payload, same shape as send.
octo-cli message edit --message-id <mid> --message-seq <seq> \
  --channel-id <cid> --channel-type <t> \
  --content-edit '{"type":1,"content":"updated"}'

# Pull history for a channel. Use --data for non-trivial filters.
octo-cli message sync --data '{"channel_id":"ch_abc","channel_type":1,"limit":50}'

# Mark messages read.
octo-cli message read-receipt --channel-id <cid> --channel-type 1 \
  --message-ids m1 --message-ids m2
```

### `channel-type` values

`1` = direct (DM), `2` = group, `5` = thread.

### App Bot restrictions (enforced server-side)

- `send` accepts **only** `channel-type=1`; group/thread are rejected, and even for DM the bot must have a friend relationship with the recipient.
- `sync` explicitly blocks `channel-type=2`.
- `edit` and `read-receipt` work in DM; other scopes follow bot-kind rules.

### `payload` shape

`payload` is an object (never auto-promoted to a flag). Wrap it under the top-level `payload` key and pass the whole body via `--data '{"channel_id":"…","channel_type":1,"payload":{…}}'` (or `--data @file.json`, `--data @-`).

**`payload.type` is an integer code (NOT a string like `"text"`).** Authoritative source: `common.ContentType` in `octo-lib/common/msg.go` (the server reads it as an int).

Chat content types:

| type | meaning           | common payload fields          |
|------|-------------------|--------------------------------|
| 1    | Text              | `content`                      |
| 2    | Image             | `url, width, height`           |
| 3    | GIF               | `url, width, height`           |
| 4    | Voice             | `url, duration`                |
| 5    | Video             | `url, width, height, duration` |
| 6    | Location          | `latitude, longitude`          |
| 7    | Card              | `uid, name`                    |
| 8    | File              | `url, name, size`              |
| 11   | MultipleForward   | (merged-forward)               |
| 12   | VectorSticker     | (sticker)                      |
| 13   | EmojiSticker      | (emoji sticker)                |
| 14   | RichText          | (rich text)                    |
| 16   | InviteJoinOrg     | (org invite)                   |

(System/notification types — 1000+ for group events, 2000 Tip — are server-generated, not sent by bots.) Text example: `{"type":1,"content":"hello"}`.

`--on-behalf-of <uid>` makes `send`/`typing` act as a user persona (OBO) — requires an active OBO grant + channel scope, else the server returns `OBO not authorized`.

## message search — 6 subcommands

Full-text search over messages and files. Registered as `message search`, `message search all|files|media|around|groups`.

```bash
# In-channel message search (payload.type in [1,11,14]).
octo-cli message search --chat-id <cid> --keyword "quarterly report" [--sort time_desc]

# Cross-channel message search (omit --chat-id). NOTE: without --chat-id the CLI
# routes to the cross-channel _search_global_messages endpoint, which is a MIXED
# feed (messages + files), so results may include file hits.
octo-cli message search --keyword "quarterly report"

# Messages + files (mixed feed) — in-channel or (no --chat-id) cross-channel.
octo-cli message search all --chat-id <cid> --keyword invoice

# Files only (payload.type=8), by filename / caption.
octo-cli message search files --chat-id <cid> --keyword "*.pdf"

# Images + videos (payload.type in [2,5]) — IN-CHANNEL ONLY, keyword NOT allowed.
octo-cli message search media --chat-id <cid> [--sort time_desc]

# Context window around an anchor message — IN-CHANNEL ONLY.
octo-cli message search around --chat-id <cid> --anchor-message-id <mid>

# Cross-channel aggregated overview (which channels matched) — CROSS-CHANNEL ONLY.
# Use it as level 1 of a two-step search: find matching channels, then pull
# messages per channel with `message search` / `message search all`.
octo-cli message search groups --keyword "quarterly report"
```

### `--chat-id` decides in-channel vs cross-channel

- **With `--chat-id`** → scoped to that channel.
- **Without `--chat-id`** → `search` / `search all` / `search files` route to their cross-channel `_search_global_*` endpoints (CLI-side, in `internal/client/search_route.go` — not backend dispatch). Plain `search` cross-channel degrades to the **mixed messages+files** feed.
- `media` and `around` **require `--chat-id`** (no cross-channel endpoint; the backend rejects a request without a channel_id). `around` also requires `--anchor-message-id`; `media` does **not** accept `--keyword`.
- `groups` is **cross-channel only** and does **not** accept `--chat-id`.

### Token subjects

- **`bf_` (User Bot)** — searches as the bot.
- **`bf_` + `--on-behalf-of <uid>`** — OBO: searches with that real person's visibility; requires an active OBO grant.
- **`uk_` (user API key)** — real-person identity; the CLI rewrites the path to `/v1/user/*`.
- **`app_` (App Bot)** — **cannot search.** The CLI rejects this locally (`validation`), before any request — distinct from a server-side `FORBIDDEN`.

### Filters and pagination

Rich filters (`sender_ids`, `member_uids`, `content_types`, `sent_at_from`/`sent_at_to`, `file_exts`, …) go through `--data '{"filters":{…}}'`. All families except `around` and `groups` paginate; the cursor travels in the request **body** (`--page-all` handles this).

## 2. `group` — 9 commands (both 5 / User Bot only 4)

Available to both bot types (5):

```bash
octo-cli group list      [--space-id <sid>]               # groups the bot is a member of
octo-cli group get       <group_no>
octo-cli group members   <group_no>                        # paginated
octo-cli group md-get    <group_no>                        # group markdown description
octo-cli group md-update <group_no> --content "# Updated description"  # write, but bot_admin gate — not App-Bot-blocked
```

User Bot only (4):

```bash
octo-cli group create        --members u1 --members u2 --name "eng" --creator u0
octo-cli group update        <group_no> --data '{"name":"new name"}'
octo-cli group member-add    <group_no> --members u3 --members u4
octo-cli group member-remove <group_no> --members u3
```

App Bot attempting any of the four write operations gets `FORBIDDEN` with message `"app bot does not support group operations"`.

## 3. `thread` — 8 commands, **User Bot only**

Every thread operation calls `validateBotGroupAccess()` on the backend and rejects App Bot unconditionally. Don't attempt these with an `app_*` token.

```bash
octo-cli thread create    <group_no> --name "Incident #42"
octo-cli thread list      <group_no>                          # paginated
octo-cli thread get       <group_no> <short_id>
octo-cli thread members   <group_no> <short_id>
octo-cli thread join      <group_no> <short_id>
octo-cli thread leave     <group_no> <short_id>
octo-cli thread md-get    <group_no> <short_id>
octo-cli thread md-update <group_no> <short_id> --content "# …"
```

The User Bot must be a member of the parent group.

## 4. `event` — polling for inbound events

```bash
octo-cli event list                          # newest page
octo-cli event list --event-id 1234 --limit 50   # cursor = highest event-id seen
octo-cli event ack  <event_id>
```

### Important: `event list` is a **POST**

Even though the semantics are "list", the handler is `POST /v1/bot/events` — the cursor travels in the body, not the query string. The CLI handles this; agents calling `octo-cli api` must use `POST`.

### Standard poll loop

```bash
cursor=0
while :; do
  batch=$(octo-cli event list --event-id "$cursor" --limit 100)
  # response shape: {ok, data:{status, results:[{event_id, event_type, message{...}}]}}
  echo "$batch" | jq -c '.data.results[]' | while read -r ev; do
    id=$(jq -r '.event_id' <<<"$ev")
    process "$ev"
    octo-cli event ack "$id"
    cursor=$id
  done
  sleep 2
done
```

`--limit` defaults to 20, caps at 100.

## 5. Error recovery cheatsheet

| Symptom                                                | Likely cause / fix                                             |
|--------------------------------------------------------|----------------------------------------------------------------|
| `FORBIDDEN` + `app bot does not support group…`        | Switch to a User Bot (`bf_*`) or fall back to DM.              |
| `send` → `permission` on DM                            | No friend relationship; add the bot as a friend first.         |
| `sync` rejected with `channel-type=2`                  | App Bot cannot sync groups — use User Bot.                     |
| `VALIDATION_ERROR` on `send`                           | `payload` must be an object, not a string.                     |
| `event list` returns `data.results: []`                | No events since the cursor; keep the cursor and poll again.    |
| `validation` on any `message search` with an `app_*` token | App Bots cannot search — use `bf_*` or `uk_*`. (CLI-side reject, before the request.) |
| `media` search rejected with a non-empty keyword       | `search media` does not accept `--keyword`; drop it.          |
| `sort=relevance` rejected                              | `relevance` requires a non-empty `--keyword`.                 |
| `OBO not authorized` on `search --on-behalf-of`        | No active OBO grant for that uid; grant it or drop `--on-behalf-of`. |

## 6. Schema lookup

```bash
octo-cli schema message.send
octo-cli schema message.search
octo-cli schema message.search.all
octo-cli schema group.members
octo-cli schema thread.create
octo-cli schema event.list
```
