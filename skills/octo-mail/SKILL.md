---
name: octo-mail
version: 0.1.0
description: Mail domain — send/list/get/reply/forward/flag/delete messages, threads, drafts, mailboxes, and the suppression list, over octo-mail's REST API. Account-scoped by API key. Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-mail — programmatic email for agents

The `mail` domain is a thin CLI over [octo-mail](https://github.com/Mininglamp-OSS/octo-mail)'s
REST surface at `$OCTO_API_BASE_URL/webapi/v0/*`. Every command is **account-scoped**:
the API key you authenticate with reaches only its own mailbox — there is no
cross-account access and no admin/provisioning surface here (accounts, domains, and
keys are created out-of-band by an operator).

Message ids are opaque `E<n>` strings (shared with the JMAP surface); thread ids are
`T<n>`. Sends are asynchronous — a successful send returns `202` with
`submissionIds`, meaning the message was accepted into the outbound queue.

## Authentication

Mail uses an octo-mail account API key (an `omk_<prefix>_<secret>` token) as the bot
token. It travels as `Authorization: Bearer <token>`, exactly like any other octo-cli
credential:

```bash
export OCTO_API_BASE_URL=https://mail.example.com     # octo-mail origin (or gateway)
export OCTO_BOT_TOKEN=omk_ab12cd34ef_9x8y7z…          # account API key
octo-cli mail mailboxes                               # smoke test
```

(An operator mints the key with `octo-mail apikey create <login>` on the server; the
secret is shown once.)

## 1. Messages — 8 commands

```bash
# List (newest first, deduplicated by email). All filters optional.
octo-cli mail list [--mailbox Inbox] [--search "invoice"] [--limit 50] [--offset 0]

# Read one message (includes parsed bodyText/bodyHtml).
octo-cli mail get <id>

# Send. --to/--cc/--bcc are repeatable (or comma-separated). Returns 202 + submissionIds.
octo-cli mail send --to a@x.test --to b@y.test --subject "Hi" --text "hello"
octo-cli mail send --to a@x.test --subject "Report" --html "<h1>Q3</h1>" --text "Q3 (plain)"

# Reply / reply-all — recipients + In-Reply-To/References derived from the original.
octo-cli mail reply     <id> --text "thanks!"
octo-cli mail reply-all <id> --text "thanks all"

# Forward — you supply new recipients; the original is quoted below your note.
octo-cli mail forward <id> --to c@z.test --text "fyi"

# Flag: add/remove IMAP flags or free-form keywords (repeatable).
octo-cli mail flag <id> --addKeywords '\Seen'
octo-cli mail flag <id> --addKeywords '\Flagged' --removeKeywords '\Seen'

# Delete (expunge). Returns 204.
octo-cli mail delete <id>
```

### Raw RFC 822

`mail get` returns parsed `bodyText`/`bodyHtml` and headers, which covers almost
every need. There is no `mail raw` command — the CLI speaks JSON envelopes, not
raw byte streams. For the rare case where you need the exact `.eml`, GET the
endpoint directly with your key:

```bash
curl -H "Authorization: Bearer $OCTO_BOT_TOKEN" \
  "$OCTO_API_BASE_URL/webapi/v0/messages/<id>/raw" > message.eml
```


### Attachments

Attachments are objects, not flags — pass them inside `--data`. Each is
`{filename, contentType, content}` where `content` is **base64**:

```bash
octo-cli mail send --to a@x.test --subject "Invoice" --text "attached" \
  --data '{"attachments":[{"filename":"inv.pdf","contentType":"application/pdf","content":"'"$(base64 -w0 inv.pdf)"'"}]}'
```

Individual flags (`--to`, `--subject`, …) override same-named fields in `--data`, so you
can mix: put the attachment array in `--data` and keep recipients/subject as flags.

## 2. Threads — 1 command

```bash
octo-cli mail thread get <thread_id>     # e.g. T5 — returns the thread + its messages
```

Get a message first (`mail get <id>`) to read its `threadId`.

## 3. Drafts — 4 commands

```bash
octo-cli mail draft list
octo-cli mail draft create --to a@x.test --subject "WIP" --text "draft body"   # 201 + {id}
octo-cli mail draft send <id>                                                   # 202
octo-cli mail draft delete <id>                                                 # 204
```

`draft create` takes the same body shape as `send` (including `--data` attachments) but
stores the message in Drafts instead of submitting it.

## 4. Mailboxes — 1 command

```bash
octo-cli mail mailboxes     # [{id, name, total, unread}, …]
```

Use a mailbox `name` (e.g. `Inbox`, `Sent`, `Drafts`) with `mail list --mailbox`.

## 5. Suppressions — 4 commands

The suppression list holds recipient addresses that should not be mailed (bounces,
complaints). It is per-account.

```bash
octo-cli mail suppression list
octo-cli mail suppression check  <address>     # 200 if suppressed, 404 if not
octo-cli mail suppression add    <address> [--reason bounce]   # idempotent
octo-cli mail suppression remove <address>     # 204
```

## 6. Status codes & error recovery

| Code | Meaning                                          |
|------|--------------------------------------------------|
| 200  | OK (get/list/flag/thread/suppression check)      |
| 201  | Draft created                                    |
| 202  | Send/reply/forward/draft-send accepted (queued)  |
| 204  | Deleted / suppression removed                    |
| 400  | Bad message id or malformed body                 |
| 401  | Missing/invalid API key                          |
| 404  | No such message/thread/draft, or not suppressed  |
| 422  | Nothing to send to (e.g. reply with no recipient)|

Errors return `{"error":{"code","message"}}`.

| Symptom                                   | Likely cause / fix                                          |
|-------------------------------------------|-------------------------------------------------------------|
| `401 unauthorized`                        | `OCTO_BOT_TOKEN` is not a valid `omk_` key, or unset.       |
| `400 invalid_id` on `get`/`reply`         | Message id must be the `E<n>` form from `mail list`.        |
| `404 not_found` on another account's id   | Account isolation — you can only see your own mail.         |
| `422 no_recipients` on `reply`            | The original had no reply address; use `send` explicitly.   |
| send "succeeds" but never arrives         | `202` means queued, not delivered — check the recipient isn't suppressed (`mail suppression check`). |

## 7. Schema lookup

```bash
octo-cli schema --list mail
octo-cli schema mail.send
octo-cli schema mail.flag
octo-cli schema mail.draft.create
```
