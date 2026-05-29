---
name: octo-files
version: 0.4.0
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
octo-cli file download    <path> --format json > saved.bin      # <path> = storage key (no bucket prefix), see note below
octo-cli file credentials --filename report.pdf
octo-cli file presigned   --filename report.pdf --fileSize 1048576   # --fileSize (bytes) is REQUIRED
```

### `upload` — multipart form

Unlike every other command, `file upload` sends a multipart `multipart/form-data` body, not JSON. The `--file` flag is **required** and names a local path; `--type` (default `chat`) and `--path` become form text fields. Promoted body flags declared in the spec go in as text fields alongside the binary.

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

For files too large for multipart, ask the backend for a presigned target and upload directly:

```bash
size=$(stat -f%z big.zip 2>/dev/null || stat -c%s big.zip)    # fileSize in bytes (REQUIRED; no padding)
cred=$(octo-cli file presigned --filename big.zip --fileSize "$size")
url=$(jq   -r '.data.uploadUrl'                   <<<"$cred")
ctype=$(jq -r '.data.contentType'                 <<<"$cred")
cdisp=$(jq -r '.data.contentDisposition // empty' <<<"$cred")
curl -X PUT -T big.zip -H "Content-Type: $ctype" ${cdisp:+-H "Content-Disposition: $cdisp"} "$url"
```

`file presigned` **requires `--fileSize` (bytes)** — the size is signed into the PUT Content-Length, so a missing/oversized value is rejected (`HTTP 400` — fileSize is required). It returns `{method, uploadUrl, downloadUrl, contentType, key, expiresIn, expiredTime, maxFileSize, [contentDisposition]}` — a one-shot signed URL (echo `contentDisposition` verbatim on the PUT if present). `file credentials` returns STS-style temporary credentials (`{bucket, region, key, credentials:{tmpSecretId, tmpSecretKey, sessionToken}, startTime, expiredTime, cdnBaseUrl}`) for SDK-driven uploads. Pick the shape the backend gives you.

## 2. Bot housekeeping

```bash
octo-cli bot register       [--data '{"agent_platform":"…","agent_version":"…","plugin_version":"…"}']
octo-cli bot set-commands   --data '{"commands":[{"command":"fix","description":"create a matter"}]}'
octo-cli bot user-info      --uid <uid>         # --uid is REQUIRED; returns {uid,name,avatar} for that user
octo-cli bot space-members  [--keyword <q>]     # members of the bot's space (limit cap 200)
octo-cli bot typing         --channel-id <cid> --channel-type 1 [--on-behalf-of <uid>]
octo-cli bot heartbeat
```

### `bot register` — special auth

`register` is the **only** operation not behind the `authBot` middleware. The backend routes it by token prefix: `app_*` → `registerAppBot`, `bf_*` → `registerUserBot`. You still supply `OCTO_BOT_TOKEN`; the CLI treats the call like any other.

Use `bot register` exactly once per bot lifecycle (publish), then use `bot set-commands` to advertise slash-command metadata.

### Get the owner_uid from `bot register`

`bot user-info` needs `--uid` and returns only `{uid, name, avatar}` — it does **not** carry `owner_uid`. The bot's `owner_uid` comes from the one-time `bot register` at publish; capture it then and cache it (env/config) rather than re-registering:

```bash
owner=$(octo-cli bot register --jq '.data.owner_uid')   # capture once at publish, then cache
```

This is the value LLM-backed operations require as `creator_uid` (e.g. the withheld `matter extract`, once the matter domain is re-enabled).

### `typing` and `heartbeat`

Both are fire-and-forget. `typing` signals an "…is typing" UI state in DM channels. `heartbeat` refreshes bot liveness — call it on a long-running poll loop so the platform knows the bot is alive.

## 3. Common pattern: message attachment end-to-end

```bash
# 1. Upload the binary.
up=$(octo-cli file upload --file ./log.txt --type chat)
url=$(jq -r '.data.url'  <<<"$up")
name=$(jq -r '.data.name' <<<"$up")

# 2. Attach it to a message.
octo-cli message send --data "$(jq -n \
  --arg c "chat-1" --arg u "$url" --arg n "$name" \
  '{chat_id:$c, text:"see log", attachments:[{url:$u, name:$n, type:"text/plain"}]}')"
```

## 4. Error recovery

| Symptom                                          | Fix                                                                |
|--------------------------------------------------|--------------------------------------------------------------------|
| `upload` → `PAYLOAD_TOO_LARGE`                   | Use `file presigned` / `file credentials` and upload direct to S3. |
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
