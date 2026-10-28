# Stored-secret downloads over MCP

Use the dedicated `get_secret_download_url` tool with `{"query":"<alias-or-id>"}`.
It is present once in every facade: `three` has 4 tools, `two` has 3, and
`both` has 6. Existing meta-tools and their schemas retain their contracts.
The raw resolver is not a registry operation and cannot be called through
`execute` or `call_op`.

The server resolves under the actual caller's User Bot (`bf_`) credential.
The bot creator owns the secrets; the current chat actor and selected space
do not grant additional access. HTTP uses only its request bearer, with no
ambient credential fallback. Stdio uses ordinary credential/profile selection;
task mode remains prohibited for `mcp serve`. Unsupported credentials fail
with a fixed `auth_error` outcome. The existing Octo resolver enforces ownership.

`query` must be nonblank, at most 128 Unicode characters and 512 UTF-8 bytes.
The object is closed; unknown, mis-cased, duplicate or null fields are refused.
Optional `dry_run: true` and global `--dry-run` return a plan without credential
resolution, file access, resolver calls or Redis connections/writes.

Success contains only `status`, `download_url`, `expires_at` (UTC), and
`expires_in_seconds`. An ambiguous alias returns only candidate `secret_id`
and `display_name`: ask the user to choose explicitly, then retry the ID.
Never guess. Other outcomes are fixed `validation_error`, `auth_error`,
`not_found`, `unavailable` or `busy`; upstream error bodies are discarded.

Download directly to a private **CLIENT file** using an ordinary downloader.
Never use text-extraction fetch or `resources/read` to display the key. Never
read/open/cat the file into model context. Scripts/code may read it internally,
but must never echo secrets or expose them through tracing, stdout, stderr,
exceptions or error output. Pass a file path to the consumer; do not paste
the file contents into chat, source, configuration examples or command arguments.

The URL itself is a sensitive temporary credential. Do not display or persist
it publicly, attach it to issues, or enable link previews/unfurling. Host tool
history can retain it: these instructions and file permissions are not hard
isolation from the same host identity or a model able to run arbitrary code.
Downloaded files and history are not erased when the URL expires.

This generic Bash/curl example contains placeholders only. Supply the returned
URL through a protected input channel, with shell tracing disabled. It keeps
the URL out of curl's argv, creates a private directory/file, never overwrites
an existing destination, suppresses body/error diagnostics, rejects redirects,
and removes partial files. It does not install a new client or SDK.

```bash
set +x
set -eu
umask 077
SECRET_DOWNLOAD_URL='<temporary-https-url-from-tool>'
# The tool emits only this URL shape. Refuse config-file metacharacters.
case "$SECRET_DOWNLOAD_URL" in
  *'"'*|*'\'*|*$'\n'*|*$'\r'*) exit 1 ;;
  https://*/mcp/secret-download/*) ;;
  *) exit 1 ;;
esac
private_dir=$(mktemp -d "${TMPDIR:-/tmp}/octo-secret.XXXXXXXX")
partial="$private_dir/.partial"
destination="$private_dir/secret.bin"
trap 'rm -f "$partial"; rmdir "$private_dir" 2>/dev/null || true' EXIT
if ! printf 'url = "%s"\n' "$SECRET_DOWNLOAD_URL" |
  curl --config - --proto '=https' --silent --fail --max-time 10 \
    --output "$partial" >/dev/null 2>/dev/null; then
  exit 1
fi
# Hard-link creation is exclusive: an existing destination is never replaced.
ln "$partial" "$destination"
unset SECRET_DOWNLOAD_URL
printf '%s\n' "$destination"   # path only; no automatic open/read
```

Run the intended consumer with this private path and remove the file after use.
Keep tracing/debug and secret-bearing error output disabled in the consumer.
An aborted GET may be retried with the same link until expiry; request a new
link after expiry. Never print a response body to diagnose a failed download.

The link holds a **snapshot selected at issuance**. Rotation, deletion or
revocation of the original stored secret does not update or delete existing
snapshots. A new issuance resolves the current value and authorization. The
link is reusable, including concurrent GETs, until admission expiry. HEAD is
405 without a body; Range and marked Purpose/Sec-Purpose/X-Purpose requests
are refused. Unmarked preview GETs cannot be distinguished from downloads.
No resume, redirect, extra Octo bearer or client SDK is required.

## Operator configuration and deployment prerequisites

These are deployment requirements, not a claim that Redis or HTTPS is deployed.
No octo-server changes or object storage are involved.

| Environment variable | Contract |
| --- | --- |
| `OCTO_MCP_SECRET_REDIS_URL` | Private replica-accessible `redis://` or `rediss://` URL; optional ACL username/password and nonnegative DB. Query/fragment options are refused. Never log the DSN. Use authenticated TLS (`rediss://`, certificate verification, TLS 1.2+) on nonlocal networks. |
| `OCTO_MCP_SECRET_REDIS_PREFIX` | Default `octo:mcp:secret-download:`; 1–128 ASCII letters/digits/colon/underscore/hyphen. Reserve an exclusive ACL key namespace. Every publisher/reader must share it. |
| `OCTO_MCP_SECRET_PUBLIC_ORIGIN` | Explicit client-reachable HTTPS origin only, with no credentials/path/query/fragment. HTTP numeric loopback is allowed only for isolated fixtures. Request Host/forwarded headers never construct links. |
| `OCTO_MCP_SECRET_TTL` | Positive Go duration, default `300s`, minimum `1ms`, maximum Go `time.Duration` (`9223372036854775807ns`, truncated to milliseconds). Recommend at most `600s`; `601s` is accepted. No caller TTL override. |

Run an HTTP MCP service (`octo-cli mcp serve --http 127.0.0.1:<port>`) behind
a TLS proxy routing `/mcp/secret-download/<capability>` without redirects.
Every HTTP replica shares Redis/prefix. For stdio, run that same HTTP service
separately and configure the stdio publisher with its public origin and the
same Redis/prefix. Stdio writes the snapshot into Redis; the HTTP service
serves it. Stdio needs no hidden local listener and must not advertise an
unreachable loopback URL. Provisioning that service/routing is explicit.

Unset, partial or invalid config disables this feature with `unavailable`;
existing MCP tools continue working. A Redis outage also fails closed. Startup
does not connect to Redis or resolve secrets. No real credential configuration
is read or changed by the isolated test fixtures.

Each capability uses 256 random bits; Redis keys store its SHA-256 hash, never
the capability or a long-lived Octo bearer. A single atomic Redis script writes
the base64-encoded snapshot and absolute deadline with `SET NX PX`; there is
no record-without-TTL window. Redis time governs issuance and admission across
replicas. Every GET checks the absolute deadline and positive physical TTL
before reading bytes, independently of asynchronous reclamation. Eviction,
restart without persistence, or expiry makes the link unavailable. A GET
admitted before expiry may finish within a 10-second write deadline. Aborting
a transfer does not consume the record; failed/aborted issuance may leave a
bounded orphan until its TTL expires. Transport retries are disabled.

Bounds per process: 16 concurrent issuance/download operations (overflow is
refused), Redis pool maximum 16 connections, resolver value 8 KiB, resolver
body 64 KiB, headers 16 KiB, stored record 16 KiB on retrieval, dedicated tool
argument object 2 KiB, and existing RPC envelope 8 MiB. Each operation has a
10-second deadline; Redis dial/read/write/pool waits have 2-second bounds;
resolver response headers have 5 seconds. Payloads are held in bounded memory,
never written to server disk. Fixed response headers include octet-stream,
attachment `secret.bin`, no-store, nosniff and no-referrer; bytes have no added
newline. Invalid/expired/missing downloads share a fixed 404 error. The endpoint
does not forward Authorization to Octo or contact Octo at download time.

Redis must be private, access controlled, memory bounded, and protected from
unbounded issuance through edge rate limits suited to the deployment. The
resolver's existing backend limits still apply. Provision a dedicated
ephemeral Redis with persistence/backups disabled when retained plaintext is
unacceptable. The stored base64 payload is **not encrypted**. Redis AOF/RDB,
replication, snapshots and backups can retain values beyond TTL; TTL is not
secure erasure. An optional operator-shared encryption key could reduce Redis
backup exposure but adds key distribution/rotation and does not protect a
compromised MCP process. This feature does not introduce that dependency.
If policy requires encrypted-at-rest payloads, resolve that operator decision
before deployment rather than treating TLS/TTL as at-rest encryption.

Disable access/recovery/body/command logging and APM payload capture for this
route and the dedicated issuance tool, including the TLS proxy/CDN. Never log
full URLs, tokens, resolver bodies, Redis command arguments or DSNs. The MCP
implementation emits no such diagnostics; third-party infrastructure and
client history require separate verification. Do not cache this route.
Independent real caller authorization, cross-host HTTPS, routing, logging,
preview/indexing and operator retention checks are required before acceptance.
