---
name: octo-messaging
version: 0.4.0
description: Messaging domain — send/edit/sync messages, read receipts, groups and threads (User Bot), and event polling. Covers App Bot DM-only constraints. Load after octo-shared.
metadata:
  requires:
    bins: ["octo"]
    skills: ["octo-shared"]
---

# octo-messaging — messages, groups, threads, events

Three related domains live here. They all call `$OCTO_API_BASE_URL/v1/bot/*`.

| Domain    | App Bot           | User Bot |
|-----------|-------------------|----------|
| `message` | yes (DM only)     | yes (DM + group + thread) |
| `group`   | read-only         | full     |
| `thread`  | **blocked**       | full     |
| `event`   | yes               | yes      |

Before attempting a write, confirm the token type with `octo config show` — App Bot writes outside DM get `FORBIDDEN` from the server ("app bot does not support group operations").

## 1. `message` — 4 commands

```bash
# Send a message. payload is a JSON object (not a flag) — pass it inside --data.
# payload.type is an INTEGER code (1=text); see the payload table below.
octo message send --channel-id <cid> --channel-type 1 \
  --data '{"payload":{"type":1,"content":"hello"}}' \
  [--stream-no <n>] [--on-behalf-of <uid>]

# Edit by (message-id, channel-id, channel-type). content-edit is the new
# content, stored opaquely server-side — carry the new payload, same shape as send.
octo message edit --message-id <mid> --message-seq <seq> \
  --channel-id <cid> --channel-type <t> \
  --content-edit '{"type":1,"content":"updated"}'

# Pull history for a channel. Use --data for non-trivial filters.
octo message sync --data '{"channel_id":"ch_abc","channel_type":1,"limit":50}'

# Mark messages read.
octo message read-receipt --channel-id <cid> --channel-type 1 \
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

## 2. `group` — 9 commands (5 read + 4 write)

Read operations (both bot types):

```bash
octo group list    [--space-id <sid>]                 # groups the bot is a member of
octo group get     <group_no>
octo group members <group_no>                          # paginated
octo group md-get  <group_no>                          # group markdown description
```

Write operations (**User Bot only**):

```bash
octo group md-update     <group_no> --content "# Updated description"
octo group create        --members u1 --members u2 --name "eng" --creator u0
octo group update        <group_no> --data '{"name":"new name"}'
octo group member-add    <group_no> --members u3 --members u4
octo group member-remove <group_no> --members u3
```

App Bot attempting any of the four write operations gets `FORBIDDEN` with message `"app bot does not support group operations"`.

## 3. `thread` — 9 commands, **User Bot only**

Every thread operation calls `validateBotGroupAccess()` on the backend and rejects App Bot unconditionally. Don't attempt these with an `app_*` token.

```bash
octo thread create    <group_no> --name "Incident #42"
octo thread list      <group_no>                          # paginated
octo thread get       <group_no> <short_id>
octo thread delete    <group_no> <short_id>
octo thread members   <group_no> <short_id>
octo thread join      <group_no> <short_id>
octo thread leave     <group_no> <short_id>
octo thread md-get    <group_no> <short_id>
octo thread md-update <group_no> <short_id> --content "# …"
```

The User Bot must be a member of the parent group.

## 4. `event` — polling for inbound events

```bash
octo event list                          # newest page
octo event list --event-id 1234 --limit 50   # cursor = highest event-id seen
octo event ack  <event_id>
```

### Important: `event list` is a **POST**

Even though the semantics are "list", the handler is `POST /v1/bot/events` — the cursor travels in the body, not the query string. The CLI handles this; agents calling `octo api` must use `POST`.

### Standard poll loop

```bash
cursor=0
while :; do
  batch=$(octo event list --event-id "$cursor" --limit 100)
  # response shape: {ok, data:{status, results:[{event_id, event_type, message{...}}]}}
  echo "$batch" | jq -c '.data.results[]' | while read -r ev; do
    id=$(jq -r '.event_id' <<<"$ev")
    process "$ev"
    octo event ack "$id"
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

## 6. Schema lookup

```bash
octo schema message.send
octo schema group.members
octo schema thread.create
octo schema event.list
```
