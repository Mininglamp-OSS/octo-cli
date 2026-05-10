---
name: octo-matter
description: Matter (todo/task) domain — CRUD, status transitions, assignees, channels, timeline, and AI extract from chat messages. Load after octo-shared.
---

# octo-matter — the matters domain

A **matter** is the Octo equivalent of a task or todo. The domain has 17 operations across five groups: core CRUD, status transitions, assignees, channels, and timeline. Plus one LLM helper: `matter extract`.

Backend: matters service at `$OCTO_MATTERS_URL/api/v1/matters`.
Both App Bot and User Bot can call every operation in this domain.

## 1. Core CRUD

```bash
octo matter create --title "Fix login bug"                     # required: --title (≤500 chars)
octo matter list   --status open --assignee-id me --limit 50   # cursor pagination
octo matter get    <id>
octo matter update <id> --title "..." --description "..."
octo matter delete <id>                                        # soft delete
```

`create` also accepts `--description` (≤10 000), `--assignee-ids` (repeatable — supports `me` alias), `--deadline` (RFC3339), `--remind-at` (RFC3339), `--source-channel-id`, `--source-channel-type` (1=user, 2=group, 5=thread), `--source-name`.

`list` filters: `--status`, `--assignee-id` (`me` supported), `--creator-id`, `--q <query>`, `--source-channel-id`, `--source-channel-type`, `--channel-id`, `--limit` (default 20, max 100), `--cursor`.

## 2. Status transitions

There is **no state machine** — any status can move to any status.

```bash
octo matter transition <id> --status done
octo matter close      <id>          # alias → --status done
octo matter reopen     <id>          # alias → --status open
octo matter archive    <id>          # alias → --status archived
```

Valid values: `open`, `done`, `archived`.

## 3. Assignees

```bash
octo matter assignee add    <id> --user-id <uid>
octo matter assignee remove <id> <uid>
```

`--user-id` accepts `me` to self-assign. Adding a user who is already assigned returns `DUPLICATE_ASSIGNEE` (validation error — recover by listing current assignees first).

## 4. Channels

Link a matter to a chat channel so conversations show up in context.

```bash
octo matter channel link   <id> --channel-id <cid> --channel-type 1   # 1=user 2=group 5=thread
octo matter channel link   <id> --channel-id <cid> --channel-type 2 --channel-name "#eng-ops"
octo matter channel unlink <id> <channel_id>
```

## 5. Timeline

Timeline entries are the successor to comments. Simple text goes through `--content`; attachments, quoted messages, and channel context go through `--data`:

```bash
octo matter timeline add    <id> --content "Ping from oncall"
octo matter timeline add    <id> --data '{
  "content":"see attached log",
  "attachments":[{"url":"https://…/log.txt","name":"log.txt","type":"text/plain"}]
}'
octo matter timeline list   <id>                           # paginated
octo matter timeline delete <id> <entry_id>
```

`--content` caps at 10 000 characters.

## 6. AI extract — create a matter from chat messages

`matter extract` hands a chat transcript to an LLM and returns a structured matter. Typical bot use:

```bash
octo matter extract --data '{
  "channel_type": 2,
  "channel_id":   "ch_abc",
  "creator_uid":  "<bot-owner-uid>",
  "msgs": [
    {"uid":"u_alice","text":"we need to fix login"},
    {"uid":"u_bob","text":"+1 by friday"}
  ]
}'
```

**Critical**: a bot must set `creator_uid` to its **owner_uid**, not its own bot_uid — the backend rejects the request otherwise. Retrieve the owner from `octo bot user-info`.

## 7. Common patterns

### Self-scope with `me`

Anywhere an assignee UID is accepted, `me` resolves server-side to the caller's UID.

```bash
octo matter list --assignee-id me --status open
```

### Cursor pagination

All list endpoints follow `{data:[], pagination:{has_more, next_cursor}}`. Let the CLI walk them:

```bash
octo matter list --status open --page-all
octo matter timeline list <id> --page-all --page-limit 5
```

### Pipe chains

```bash
octo matter list --status open --assignee-id me --jq '.data[].id' \
  | xargs -I{} octo matter close {}
```

## 8. Error recovery

| `error.code`          | What to do                                                                 |
|-----------------------|----------------------------------------------------------------------------|
| `MATTER_NOT_FOUND`    | Confirm the id with `octo matter list` before retrying.                    |
| `ASSIGNEE_NOT_FOUND`  | The UID is wrong or not in the space. `octo bot space-members` to verify. |
| `DUPLICATE_ASSIGNEE`  | Already assigned — list current assignees and skip.                        |
| `FORBIDDEN`           | Bot lacks space membership or owner-equivalent permission.                 |
| `SPACE_FORBIDDEN`     | `OCTO_SPACE_ID` / `--space` points at a space the bot isn't in.            |
| `VALIDATION_ERROR`    | `error.detail.details` names the offending field; fix and retry.           |
| `PAYLOAD_TOO_LARGE`   | Body over 1 MB — trim description/timeline content.                        |
| `RATE_LIMITED`        | Honour the cooldown window; the retry wrapper already waits once.          |

## 9. Schema lookup

When unsure about a flag or body shape:

```bash
octo schema matter.create
octo schema matter.list
octo schema matter.timeline.add
```

Everything in this skill is derived from those specs — if the schema says otherwise, trust the schema.
