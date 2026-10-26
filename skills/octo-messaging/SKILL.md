---
name: octo-messaging
version: 0.4.1
services: ["message", "group", "thread", "event"]
description: Messaging domain — send/edit/sync messages, read receipts, message search (search/all/files/media/around/groups, in-channel or cross-channel), groups and threads (User Bot), and event polling. Covers App Bot DM-only constraints. Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-messaging — messages, groups, threads, events

Three related domains live here. They all call `$OCTO_API_BASE_URL/v1/bot/*` — except the `message search` family under a `uk_` (user API key) token, which the CLI routes to `/v1/user/*` (real-person mount; see the `message search` section).

| Domain    | App Bot | User Bot |
|-----------|---------|----------|
| `message` | yes (DM only) | yes (DM + group + thread) |
| `group`   | read-only except `md-update` with `bot_admin` | full; operation-specific `bot_admin` gates apply |
| `thread`  | **blocked** | full; `md-update` additionally requires `bot_admin` |
| `event`   | yes | yes |

Search capability (the `message search` family) is a separate axis by token kind:

| Token   | Can search? | Notes                                                  |
|---------|-------------|--------------------------------------------------------|
| `app_*` | **no**      | CLI rejects locally with a `validation` error (not a server `FORBIDDEN`). |
| `bf_*`  | yes         | Searches as the bot; add `--on-behalf-of <uid>` for a real-person (OBO) view. |
| `uk_*`  | yes         | Real-person identity; routed to `/v1/user/*`.          |

Before a write, confirm the token type with `octo-cli config show`. App Bot group mutations are rejected except `group md-update`, which still requires the bot to be a group member and have `bot_admin`.

## 1. `message` — 4 core commands (+ 6 search subcommands, see below)

```bash
# Send a message. payload is a JSON object (not a flag) — pass it inside --data.
# payload.type is an INTEGER code (1=text); see the payload table below.
octo-cli message send --channel-id <cid> --channel-type 1 \
  --data '{"payload":{"type":1,"content":"hello"}}' \
  [--stream-no <n>] [--on-behalf-of <uid>]

# Raw edit mode: content_edit is a non-empty JSON-encoded string; omit template_ref.
octo-cli message edit --message-id <mid> [--message-seq <seq>] \
  --channel-id <cid> --channel-type <t> \
  --content-edit '{"type":1,"content":"updated"}'

# Registry edit mode uses a real template reference in the JSON body; omit content_edit.
octo-cli message edit --data @registry-edit.json

# registry-edit.json supplies message_id, channel_id, channel_type, template_ref,
# state, data, and a positive card_seq; transient is optional.

# Pull history for a channel. Use --data for non-trivial filters.
octo-cli message sync --channel-id <cid> --channel-type 1 \
  --start-message-seq <start> --end-message-seq <end> --pull-mode <mode> --limit 50

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
| 17   | InteractiveCard  | Raw card payload or Registry template reference |

(System/notification types — 1000+ for group events, 2000 Tip — are server-generated, not sent by bots.) Text example: `{"type":1,"content":"hello"}`.

### InteractiveCard (type 17)

InteractiveCard sends are deployment- and bot-policy-gated. Raw sends use a validated card payload; Registry sends use `template_ref` with `state` and `data`. Choose one mode and do not combine the raw card with a Registry reference. Type-17 sends reject `--on-behalf-of` even when the bot otherwise has an active OBO grant.

For eligible ordinary `send` and `typing` requests, `--on-behalf-of <uid>` acts as that user persona and requires an active OBO grant plus channel scope.

## message search — 6 subcommands

Full-text search over messages and files. Registered as `message search`, `message search all|files|media|around|groups`.

```bash
# In-channel message search (payload.type in [1,11,14]); this example uses a group channel.
octo-cli message search --chat-id <group-cid> --channel-type 2 --keyword "quarterly report" [--sort time_desc]

# Cross-channel message search (omit --chat-id). NOTE: without --chat-id the CLI
# routes to the cross-channel _search_global_messages endpoint, which is a MIXED
# feed (messages + files), so results may include file hits.
octo-cli message search --keyword "quarterly report"

# Messages + files (mixed feed) — in-channel or (no --chat-id) cross-channel.
octo-cli message search all --chat-id <group-cid> --channel-type 2 --keyword invoice

# Files only (payload.type=8), by filename / caption.
octo-cli message search files --chat-id <group-cid> --channel-type 2 --keyword "*.pdf"

# Images + videos (payload.type in [2,5]) — IN-CHANNEL ONLY, keyword NOT allowed.
octo-cli message search media --chat-id <group-cid> --channel-type 2 [--sort time_desc]

# Context window around an anchor message — IN-CHANNEL ONLY, not cursor pagination.
octo-cli message search around --chat-id <group-cid> --channel-type 2 --anchor-message-id <mid> [--page-size 10]

# Cross-channel aggregated overview (which channels matched) — CROSS-CHANNEL ONLY.
# Use it as level 1 of a two-step search: find matching channels, then pull
# messages per channel with `message search` / `message search all`.
octo-cli message search groups --keyword "quarterly report" [--sequence 42]
```

### `--chat-id` decides in-channel vs cross-channel

- **With `--chat-id`** → scoped to that channel.
- **Without `--chat-id`** → `search` / `search all` / `search files` route to their cross-channel `_search_global_*` endpoints (CLI-side, in `internal/client/search_route.go` — not backend dispatch). Plain `search` cross-channel degrades to the **mixed messages+files** feed.
- When `--chat-id` is supplied, `--channel-type` is also required; the CLI does not infer it. Use `1` for DM, `2` for group, or `5` for thread. A thread channel ID must contain the backend's `____` delimiter. Cross-channel search omits `--chat-id` and does not inherit this single-channel requirement.
- `groups` is **cross-channel only** and does **not** accept `--chat-id`.
- `groups` accepts optional `--sequence <int64>`; the backend echoes the supplied value for correlation. In CLI JSON output, read it at `.data.data.sequence` (the backend's `{data,pagination}` object is inside the CLI envelope). It is not a pagination cursor.

### Token subjects

- **`bf_` (User Bot)** — searches as the bot.
- **`bf_` + `--on-behalf-of <uid>`** — OBO: searches with that real person's visibility; requires an active OBO grant.
- **`uk_` (user API key)** — real-person identity; the CLI rewrites the path to `/v1/user/*`.
- **`app_` (App Bot)** — **cannot search.** The CLI rejects this locally (`validation`), before any request — distinct from a server-side `FORBIDDEN`.

### Filters and pagination

- For `search`, `all`, `files`, and `media`, omitted/zero `--page-size` uses the backend default of 20; nonzero values must be 1–100. Non-empty keywords are limited by the backend to 64 Unicode code points. `around --page-size` controls the context window (same default/range), not cursor pagination.
- Rich filters (`sender_ids`, `member_uids`, `content_types`, `sent_at_from`/`sent_at_to`, `file_exts`, …) go through `--data '{"filters":{…}}'`. For manual paging, pass `--cursor <cursor>` and `--page-size <n>`; the cursor is carried in the request body. `groups` has no channel-type input. All families except `around` and `groups` paginate, and `--page-all` handles their cursor loop.

## 2. `group` — 9 commands (both 5 / User Bot only 4)

Available to both bot types (5):

```bash
octo-cli group list      [--space-id <sid>]               # groups the bot is a member of
octo-cli group get       <group_no>
octo-cli group members   <group_no>
octo-cli group md-get    <group_no>                        # group markdown description
octo-cli group md-update <group_no> --content "# Updated description"  # member + bot_admin required; App Bot supported
```

User Bot only (4):

```bash
octo-cli group create        --members u1 --members u2 [--name "eng"] [--creator u0] [--space-id <sid>]
octo-cli group update        <group_no> [--name <name>] [--notice <notice>]  # bot_admin; provide name or notice
octo-cli group member-add    <group_no> --members u3 --members u4
octo-cli group member-remove <group_no> --members u3  # bot_admin
```

The four User-Bot-only group operations above are rejected for App Bots. This does not include `group md-update`, which is available to either bot kind when the bot is a member and has `bot_admin`. Create, member-add, and member-remove require at least one `--members` value. Group updates require a `name` or `notice` property; an explicit empty string is accepted by the backend, but an unknown property alone is not an update.

Group names longer than 50 Unicode code points are truncated to 50 by the backend on create/update; they are not rejected for length.

## 3. `thread` — 8 commands, **User Bot only**

Every thread operation calls `validateBotGroupAccess()` on the backend and rejects App Bot unconditionally. Don't attempt these with an `app_*` token.

```bash
octo-cli thread create    <group_no> --name "Incident #42" [--source-message-id <mid>] [--on-behalf-of <uid>]
octo-cli thread list      <group_no>  # bare array, first page (up to 100)
octo-cli thread list      <group_no> --page-index <n> --page-size <n>  # page-number pagination
octo-cli thread get       <group_no> <short_id>
octo-cli thread members   <group_no> <short_id>
octo-cli thread join      <group_no> <short_id>
octo-cli thread leave     <group_no> <short_id>
octo-cli thread md-get    <group_no> <short_id>
octo-cli thread md-update <group_no> <short_id> --content "# …"  # requires bot_admin
```

The User Bot must be a member of the parent group. `thread md-update` additionally requires `bot_admin` in that group. A non-empty thread-create `--on-behalf-of <uid>` creates the thread as that user only when the bot has an active OBO grant and that user can read the parent group; the backend checks both and fails closed. An empty/blank value leaves the bot as creator.

Without either thread-list page parameter, the backend returns a bare array for page 1 with up to 100 threads. Supplying either `--page-index` or `--page-size` changes the response to `{count,list}`. In paged mode, page index defaults/clamps to 1; page size defaults to 15 when nonpositive and caps at 100.

Group and thread markdown updates share a configurable byte-size ceiling (10,240 bytes in the inspected default configuration; deployment configuration can change it). Thread markdown additionally rejects whitespace-only content; the group markdown handler does not apply that blank-content check.

## 4. `event` — polling for inbound events

```bash
octo-cli event list                          # next batch, earliest first
octo-cli event list --event-id 1234 --limit 50 --wait 20 --timeout 25s  # wait in seconds; set timeout above wait
octo-cli event ack  <event_id>
```

### Important: `event list` is a **POST**

Even though the semantics are "list", the handler is `POST /v1/bot/events` — the cursor travels in the body, not the query string. The CLI handles this; agents calling `octo-cli api` must use `POST`.

The backend reads after the exclusive `event_id` cursor in ascending event-ID order. Cursor `0` starts at the earliest queued event; `event list` is not a newest-first page.

### Optional server-side wait

`--wait <seconds>` opts into a server-side hold only when no visible events are pending. Absent or zero wait preserves immediate polling; the server clamps the hold to 30 seconds. Set the CLI `--timeout` above the requested wait (the CLI default is 30 seconds); callers with a shorter outer deadline must use a smaller wait. The OpenClaw channel plugin has a 10-second request timeout, so keep its wait below that limit.

### Standard poll loop

```bash
cursor=0
while :; do
  batch=$(octo-cli event list --event-id "$cursor" --limit 100 --format json)
  # response shape: {ok, data:{status, results:[{event_id, event_type, message{...}}]}}
  # Use Bash process substitution so this loop runs in the parent shell.
  while IFS= read -r ev; do
    id=$(jq -r '.event_id' <<<"$ev")
    process "$ev"
    octo-cli event ack "$id"
    cursor=$id
  done < <(jq -c '.data.results[]' <<<"$batch")
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
