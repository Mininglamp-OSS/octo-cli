# MCP context admission

MCP authenticates each tool call as a `bf_` User Bot using one read-only
`POST /v1/auth/verify-bot` request. The verification deadline is three seconds,
with no retries or identity cache. Production sends `include_mcp_policy:true`
and validates the complete v1 policy object. A missing or invalid policy fails
closed, including when an older server ignores the opt-in field. Bot overrides
take precedence over the stage default; a failed read is never an absent row.
There is no automatic switch date, registration, or fallback to debug mode.

HTTP uses exactly one request bearer and the operator's configured API origin.
It never inherits the host's Bot or task credential. Stdio resolves the normal
credential and origin providers again for every RPC. The authenticated UID,
token and origin are pinned to that RPC's private factory, including direct
secret access and business dry-run. Stored Bot IDs are claims that must match.
`app_`, `uk_`, human and Loop task credentials are outside this MCP contract.

Every `tools/call` passes the same admission gate, including `search_ops`,
`describe_op`, `get_skill`, both execution facades and
`get_secret_download_url`. Strict policy requires the following metadata;
compatible policy permits its complete absence. Present but malformed context
is refused in either mode.

```json
{"jsonrpc":"2.0","id":9007199254740993,"method":"tools/call","params":{"name":"describe_op","arguments":{"operation_id":"message.send"},"_meta":{"octo":{"version":1,"im":{"sender":"synthetic_user","channel":"synthetic_bot","channel_type":1,"space_id":"B","message_id":"2112815112610910209"}}}}}
```

`octo` and `im` are closed objects. Duplicate decoded keys, null, unknown keys,
invalid UTF-8 or unpaired surrogates are rejected. `version` must be the number
token `1`; `channel_type` must be `1`, `2` or `5`. Sender, raw channel and Space
are opaque strings with maximum UTF-8 byte lengths 128, 256 and 40. They must
be nonempty, without edge whitespace or control characters. `message_id` is a
positive decimal string within signed int64, without leading zeros. The raw
`octo` value is limited to 4096 bytes. RPC IDs preserve their original number
lexeme; string IDs are limited to 256 bytes and number lexemes to 128 bytes.

The source is a structurally checked client declaration. It does not prove
message existence or membership. Raw DM channels remain unchanged; source
Space and sender never replace a business target, actor or `X-Space-Id`.
Existing operator destination restrictions and backend authorization still
apply. MCP forbids OBO startup configuration, delegation controls at any
object depth, and OBO grant operations. Duplicate decoded business object keys
are refused before logging so overwritten secret fields cannot evade
projection. Ordinary CLI capabilities are unchanged.

`initialize`, `ping`, `tools/list`, `resources/list` and `resources/read` permit
missing context. Any context supplied on those methods is checked. HTTP still
authenticates these methods; stdio's local protocol methods do not use the
network. Notifications never execute tools and have no RPC response. HTTP
acknowledges them with an empty 202 response. Batch execution is unsupported.

## Operator debug policy

Select debug explicitly at startup with both `--policy-debug` and
`--policy-debug-file /absolute/private/policy.json`. There is no environment,
request, header or initialization override, and no default file path.

```json
{"version":1,"api_base_url":"https://octo.example.invalid","stage_default":"compatible","bot_policies":{"synthetic_bot":{"context_mode":"strict"}}}
```

All four root fields are required. The schema is closed at every level, with
at most 256 Bot entries and a 64 KiB file limit. Each entry contains only
`context_mode`, exactly `strict` or `compatible`. The origin must be an HTTPS
origin without credentials, path, query or fragment and must match the RPC's
resolved origin. The file must be a regular file owned by the service EUID,
mode 0600, with one link. Directory traversal rejects symlinks, `..`,
uncontrolled owners and group/other writable parents. This implementation
requires POSIX filesystem semantics.

The immutable startup snapshot is indexed by the UID returned by real Bot
verification. Debug omits `include_mcp_policy` and therefore supports an older
identity endpoint without policy tables. It still requires working identity
verification and durable request storage. Replacing or removing the file does
not change a running process. Drain and restart to load a new snapshot.

## Request-only storage

Set `OCTO_MCP_REQUEST_LOG_DIR` to an operator-created absolute directory owned
by the service EUID with mode 0700. Use a stable, separate directory per
instance. Parents must have controlled ownership and no group/other write
permission. A directory lock prevents shared writers; files use mode 0600.
Missing, unsafe, locked or corrupt storage prevents service startup.

One writer owns a queue of at most 128 encoded records and 1 MiB. It appends
one closed-schema JSONL request record and fsyncs before business execution.
Admission waits at most 500 ms, or until cancellation. Failure, queue pressure
or a late acknowledgement never starts business execution. A record can exist
for a request whose business action did not run; it is not outcome evidence.

Records contain server receive time and UUID, authenticated Bot UID, typed RPC
ID, known tool/operation, optional valid source declaration and a closed
argument projection. The projection follows XIN-2677 v2 section 8.2. Message
bodies, search terms, file data and paths, secret aliases, credentials, owner
profiles, verification replies, responses, status and outcomes are excluded.
Declared secrets hidden in permitted IDs or string RPC IDs are replaced with
the fixed `***REDACTED***` marker. Source fields containing those secrets are
rejected. Each projected argument object is bounded to 2048 bytes and a record
to 8192 bytes. No result/update writer API exists.

Segments rotate at 64 MiB or a UTC date change. Closed segments and quarantine
backups expire seven days after close; maintenance runs every 60 seconds. A
2 GiB instance quota refuses new tools without deleting unexpired records.
Recovery validates complete records, rejects intermediate corruption and
copies a damaged active segment to private quarantine before truncating only
its incomplete tail. File creation and rename also sync the directory.
Shutdown stops admission, drains and closes the writer with a five-second
wait bound. A blocked filesystem syscall can outlast that bound; the command
reports a fixed shutdown error and process exit ends remaining goroutines.

Ingress is bounded to 32 tool calls, 16 concurrent verifications and an
8-per-second token bucket with burst 16. Operators must account for the shared
outbound IP's server verification budget across all replicas. The request
deadline is at most 120 seconds, including verification and persistence.

## Compatibility and validation limits

Serve-time `mcp serve --dry-run` now fails locally with
`MCP_OFFLINE_POLICY_UNAVAILABLE` and opens no listener. On a running server,
`execute.dry_run=true` previews only the business request: identity/policy
network calls and request persistence still happen. Ordinary offline CLI
schema and preview commands remain available.

Synthetic caller and fake-server tests verify the approved wire and local
admission behavior. They do not establish compatibility with an independently
reviewed server implementation, real IM injection, group/thread membership or
production filesystem/proxy collection settings. Those remain separate gates.
