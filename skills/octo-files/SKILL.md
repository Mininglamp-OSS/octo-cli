---
name: octo-files
version: 0.4.0
services: ["file", "bot"]
description: File operations (upload/download, presigned S3 credentials) plus bot housekeeping (register, set-commands, user-info, space-members, typing, heartbeat). Load after octo-shared.
metadata:
  requires:
    bins: ["octo-cli"]
    skills: ["octo-shared"]
---

# octo-files — file I/O and bot housekeeping

Two small domains are covered here because they share a base URL (`$OCTO_API_BASE_URL/v1/bot/*`) and are usually needed together:

- `file` — 4 ops, no bot-kind restrictions
- `bot` — 6 ops, no bot-kind restrictions

## 1. File operations

```bash
octo-cli file upload      --file ./report.pdf [--type chat] [--path subdir/]
octo-cli file download    <path> [--filename <name>] --format json  # JSON envelope with presigned URL, not file bytes
octo-cli file credentials --filename report.pdf
octo-cli file presigned   --filename report.pdf --fileSize 1048576   # --fileSize (bytes) is REQUIRED
```

### `upload` — multipart form

Unlike most commands, `file upload` sends a multipart `multipart/form-data` body, not JSON. The `--file` flag is **required** and names a local path. `--type` (default `chat`) and `--path` are query parameters; they are not multipart form fields. The multipart body carries the file and any promoted body fields declared in the spec as text fields.

The maximum size and allowed/blocked extensions follow the current backend file policy. Multipart uploads and presigned URL issuance check that policy, including the configured file-size cap. The STS `file credentials` endpoint checks the filename/extension but has no size input or size check in the inspected backend; external COS enforcement for those credentials is unverified, so do not assume STS bypasses the file-size policy. There is no fixed 100 MB ceiling guaranteed by the CLI.

The response envelope carries the returned file descriptor under `data` — use it to build attachment references in `message send`.

### `download` — returns a presigned URL

`file download <path>` issues a GET. The backend responds with a 302 redirect to the storage tier. The CLI does **not** stream raw bytes — instead it returns a JSON envelope containing the presigned URL:

```bash
octo-cli file download /chat/2026/05/abc123.png
# → {"ok":true,"data":{"url":"https://s3.../abc123.png?...","status":302,"content_type":"image/png"}}
```

To actually fetch the file, use the URL from the response:

```bash
octo-cli file download /chat/2026/05/abc123.png --jq '.data.url' | xargs curl -o out.png
```

### Direct-to-S3 uploads

For the direct-to-S3 workflow, request a presigned target from the backend:

```bash
size=$(stat -f%z big.zip 2>/dev/null || stat -c%s big.zip)    # fileSize in bytes (REQUIRED; no padding)
cred=$(octo-cli file presigned --filename big.zip --fileSize "$size")
url=$(jq   -r '.data.uploadUrl'                   <<<"$cred")
ctype=$(jq -r '.data.contentType'                 <<<"$cred")
cdisp=$(jq -r '.data.contentDisposition // empty' <<<"$cred")
curl -X PUT -T big.zip -H "Content-Type: $ctype" ${cdisp:+-H "Content-Disposition: $cdisp"} "$url"
```

`file presigned` **requires `--fileSize` (bytes)** — the size is signed into the PUT Content-Length, so the upload must carry exactly that many bytes. A missing or non-positive value is rejected; a value above the current file-size cap is rejected as `file_too_large` (HTTP 400). It returns `{method, uploadUrl, downloadUrl, contentType, [contentDisposition], key, expiresIn, expiredTime, maxFileSize}` — a one-shot signed URL (echo `contentDisposition` verbatim on the PUT if present). `file credentials` returns STS-style temporary credentials (`{bucket, region, key, credentials:{tmpSecretId, tmpSecretKey, sessionToken}, startTime, expiredTime, cdnBaseUrl}`) for SDK-driven uploads; their external COS size enforcement is not established.

## 2. Bot housekeeping

```bash
octo-cli bot register       [--agent-platform <name>] [--agent-version <version>] [--plugin-version <version>] [--agent-hosting <slug>]
octo-cli bot set-commands   --data '{"commands":[{"command":"fix","description":"create a matter"}]}'
octo-cli bot user-info      --uid <uid>         # --uid is REQUIRED; returns {uid,name,avatar} for that user
octo-cli bot space-members  [--keyword <q>] [--space-id <sid>] [--page <n>] [--limit <n>]
octo-cli bot typing         --channel-id <cid> --channel-type 1 [--on-behalf-of <uid>]
octo-cli bot heartbeat
```

`bot space-members` uses a one-based `--page` (defaults to 1; capped at 1,000,000) and a `--limit` (defaults to 50; maximum 200). Request later pages by incrementing `--page` yourself; this operation does not provide `--page-all`.

### `bot register` — special auth

`register` is the **only** operation not behind the `authBot` middleware. The backend routes it by token prefix: `app_*` → `registerAppBot`, `bf_*` → `registerUserBot`. You still supply the normal environment credential (`OCTO_TOKEN`, or legacy alias `OCTO_BOT_TOKEN`); the CLI treats the call like any other.

For CLI housekeeping, run `bot register` during publish to capture `owner_uid` for caching; then use `bot set-commands` to advertise slash-command metadata. Registration is repeatable, not an exactly-once backend rule: a runtime may call it again to retrieve current connection information, and User Bots can re-register to recover after disconnection.

The optional agent-platform, agent-version, plugin-version, and agent-hosting fields report runtime metadata. Only User Bot (`bf_*`) registration persists agent metadata; App Bot (`app_*`) ignores it. `agent-hosting` is normalized by the backend to an open-vocabulary lowercase ASCII slug (starts with a letter, then letters/digits/underscores; at most 64 bytes). `none` retracts the marker, while empty or omitted means unchanged. Invalid metadata is ignored without failing registration. These fields are metadata, not a replacement for the bot token.

### Get the owner_uid from `bot register`

`bot user-info` needs `--uid` and returns only `{uid, name, avatar}` — it does **not** carry `owner_uid`. The bot's `owner_uid` comes from `bot register`; cache it as CLI housekeeping (for example, in env/config) when later commands need it. This recommendation does not prevent a runtime from registering again for current connection information or recovery:

```bash
registration=$(octo-cli bot register --format json) # publish/setup or runtime recovery
owner=$(jq -r '.data.owner_uid' <<<"$registration") # decode the JSON string to a raw UID
```

Use this raw owner UID as `creator_uid` only where the operation schema requires the bot owner. Inspect an active operation with `octo-cli schema <operation-id>`.

### `typing` and `heartbeat`

Both are fire-and-forget. `typing` signals an "…is typing" UI state in DM channels. `heartbeat` refreshes bot liveness — call it on a long-running poll loop so the platform knows the bot is alive.

## 3. Common pattern: message attachment end-to-end

```bash
# 1. Upload the binary.
up=$(octo-cli file upload --file ./log.txt --type chat --format json)
url=$(jq -r '.data.url'  <<<"$up")
name=$(jq -r '.data.name' <<<"$up")
size=$(jq -r '.data.size' <<<"$up")

# 2. Attach it to a message.
octo-cli message send --data "$(jq -n \
  --arg c "chat-1" --arg u "$url" --arg n "$name" --argjson s "$size" \
  '{channel_id:$c, channel_type:1, payload:{type:8, url:$u, name:$n, size:$s}}')" \
  --dry-run
```

## 4. Error recovery

| Symptom                                          | Fix                                                                |
|--------------------------------------------------|--------------------------------------------------------------------|
| `upload` → `file_too_large` (HTTP 400)            | The configured file-size policy was exceeded. Reduce the file or ask an administrator about the policy; presigned URL issuance checks the same cap. |
| `payload_too_large` / `PAYLOAD_TOO_LARGE` (HTTP 413) | A distinct request-payload-size response, not the file-policy `file_too_large` error. Follow the affected endpoint's limit; do not assume presigned or STS upload bypasses it. |
| `upload` → `validation` "--file is required"     | Path is empty or unreadable; check permissions and absolute path.  |
| `download` returns JSON instead of bytes          | Expected; extract `.data.url` and fetch with curl/wget.            |
| `bot typing` returns 200 but no UI update        | Channel type must be `1` and the DM must exist.                    |
| `bot register` rejected with `UNAUTHORIZED`      | Token prefix/env mismatch — confirm with `octo-cli config show`.       |

## 5. Schema lookup

```bash
octo-cli schema file.upload
octo-cli schema file.download
octo-cli schema bot.register
octo-cli schema bot.user-info
```

## More Bot API operations

Additional Bot-callable operations (with their roles, server-side switches and risk) are listed in `bot-api.md`. Load it before using an operation that is not described above.
