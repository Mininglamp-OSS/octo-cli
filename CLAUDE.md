# CLAUDE.md — octo-cli project instructions

## What is octo-cli

`octo-cli` is the command-line interface for the Octo ecosystem, built for **AI Agent Bots** to call via `exec` from agent runtimes (OpenClaw, Claude Code, etc.). Output is a JSON envelope; agent-runtime commands take no interactive I/O. The sole exception is `octo-cli auth login`, an operator-only setup step that reads the token from a hidden terminal prompt (or stdin).

## Architecture

- Go single binary, cobra CLI framework.
- **Metadata-driven**: the entire service command tree is auto-registered at startup from OpenAPI 3.x specs embedded into the binary via `internal/registry`. To add or change an endpoint, update a spec — not code. Spec extensions cover identity routing (`x-octo-allowed-token-kinds`, `x-octo-mount-by-token-kind`), output shaping (`x-octo-response-fields`, `x-octo-lossless-id-fields`), and log hygiene (`x-octo-secret`); each is opt-in, and omitting it keeps the historical behaviour byte-for-byte. `x-octo-flag` renames the flag for a query/header param or a promoted body field, and on a **path** param it adds an optional flag alternative to the positional slot (the escape hatch for base64url ids starting with `-`, which cobra would parse as a flag) — an operation with no such declaration keeps `cobra.ExactArgs`.
- **Thin client**: all business logic lives in backend services (matters, dmworkim). CLI is transport + validation + formatting.
- **Unified gateway**: every domain uses `OCTO_API_BASE_URL`; embedded OpenAPI
  paths carry module namespaces such as `/market/api`, `/docs-html`, and
  `/fleet/api`.
- **Factory DI**: `internal/cmdutil.Factory` is the DI container; no package-level globals. Tests inject stubs through `ConfigFunc` / `CredentialFunc` / `ClientFunc` / `RegistryFunc`. `Factory.ErrorEmitted` tracks whether an error envelope was already written to stderr, preventing double-emit between RunE and the top-level main error handler.
- **JSON envelope I/O**: `{ok, identity, data, _pagination, _rate_limit}` on stdout for success; `{ok:false, error:{type,code,message,hint,detail}}` on stderr for failure. Exit codes: auth=3, validation/config=2, rest=1.
- **Pre-flight validation** (`cmd/service/run.go`, `cmd/service/enum.go`): the metadata-driven path checks the resolved request against the spec before any HTTP — required fields and `minItems` (`VALIDATION_ERROR`), spec `enum` vocabularies (`ENUM_NOT_ALLOWED`), `uint64` id range, and a refusal of empty or `.` / `..` path values (an empty value addresses the collection and a gateway that normalises dot segments retargets the request — and the engine emits `DELETE`). Loop Public API operations additionally enforce composition, closed-object and minimum-property constraints, Unicode string lengths, and maximum array sizes; those extended checks remain backend-enforced for legacy domains. Body checks walk the *merged* body, so `--data` is validated too, at every nesting depth and including `format: uint64` — `--data` is not a raw passthrough on this path, and it decodes with `UseNumber` so an id above 2^53 is never rounded before it is checked. A property present with an explicit `null` is checked, not skipped: it is refused wherever the schema constrains the value (an enum or a `uint64` format), because `null` matches no vocabulary member and decodes into a scalar field as the zero value — for a `parent_id` that is folder `0`, the documented root, i.e. a *valid* id addressing a place nobody named. A required field set to `null` is reported as missing instead, so both paths refuse it; a `null` on an unconstrained property is still forwarded, since a backend may accept it to clear the field. Enum and constant comparison is by canonical form, not `==`, because the same wire value arrives as `int`, `float64` or `json.Number` depending on how it was supplied; numbers compare by exact decimal text, so an integer vocabulary admits `1` but not `1.0` — the body keeps what the caller wrote, and a non-integer would fail at the backend on a value the local gate had called valid. Hand-written composites reuse the same walker via `service.ValidateRequestBody`, so a composite that replaces a generated leaf cannot enforce a weaker contract than the leaf did.

## Identity Model

- The CLI is **bot-only** — no interactive user login. Credentials include `app_*` (App Bot), `bf_*` (User Bot), `uk_*` (**user API key**), and short-lived `octo_loop_*` Loop task credentials. A user key carries a real person's identity and is used for `message search` and the whole `drive` domain (both route to `/v1/user/*` rather than `/v1/bot/*`); `credential.TokenKind` reports the latter two as `user_key` and `loop_credential` respectively.
- **Credential resolution** (see `internal/credential`, `internal/authstore`): a token comes from a stored encrypted profile or, as a fallback, an env var — `OCTO_TOKEN` first, then `OCTO_BOT_TOKEN`. The credential's `Source` records which variable was used, so the envelope's `identity.source` cannot mislead. Stored profiles live in `~/.octo-cli` (override `OCTO_CONFIG_DIR`): metadata in plaintext `config.json`, tokens in AES-256-GCM `credentials.enc`. Manage them with `octo-cli auth`.
- **Per-domain token gate and mount routing**: a spec may declare which token kinds it accepts and which server mount each kind uses. `drive` accepts all three and routes `uk_*` to `/v1/user/drive/*`, bots to `/v1/bot/drive/*`. An incompatible kind fails locally with `TOKEN_KIND_NOT_ALLOWED` (`validation`, exit 2 — switch credentials, don't re-auth), implemented once in `cmd/service/identity.go` and reused by the hand-written drive composites via `service.MountForOperation`. Generated leaves and composites alike resolve identity **before** `--dry-run` or any local success return, so a refused credential can never have a request described for it or a document link resolved under it.
- **Lossless uint64 ids**: `drive` file ids are backend uint64s. Inputs are decimal-string flags validated in `[0, 2^64-1]` and sent as JSON integers; responses are emitted as decimal strings. Go's `int` cannot hold the upper half of the range and a `float64` would round above 2^53, so neither is used on this path. The rule holds for *every* output format, not just `json`: the row extraction behind `--format table|csv|ndjson` decodes with `UseNumber` too, so a large integer a spec did not declare as a lossless field still prints the digits the backend sent rather than a rounded float.
- **Selecting a credential at runtime**: `--bot-id <robot_id>` (env `OCTO_BOT_ID`) is the agent's primary selector — robot ids are self-known; `--profile <name>` selects by friendly name. With exactly one profile, selection is implicit; with **two or more, a selector is required** (ambiguity is a hard error, never a silent guess). Precedence: selector > sole/implicit profile > `OCTO_BOT_TOKEN`. The success envelope's `identity` echoes the active `{profile, robot_id, bot_kind, source}` so misuse is visible.
- **Isolation boundary = OS user**: the encryption key is machine-derived, so the store resists off-machine leakage (commit/backup/sync) but not a same-user process. Isolate mutually-distrusting bots with separate OS users or `OCTO_CONFIG_DIR` values.
- **Daemon task isolation**: `OCTO_CREDENTIAL_MODE=task` selects the restricted Loop-only policy but cannot protect against a process rewriting its own environment. Daemon task processes must receive an isolated `OCTO_CONFIG_DIR` with no host profiles and the short-lived `OCTO_BOT_TOKEN`.
- Each Bot has an **owner**; operations are attributed to the Bot identity. For LLM-backed paths (`matter extract`) the bot acts on behalf of its owner — pass `owner_uid` as `creator_uid`.
- **Search subjects** (`message search` family): a `bf_` token searches as the bot, or as a real person with `--on-behalf-of <uid>` (OBO — requires an active grant); a `uk_` token searches as the real person it belongs to. An `app_` token cannot search — the CLI rejects it locally (`validation`, in `internal/client/search_route.go`) before any request, distinct from a server-side `FORBIDDEN`.
- `OCTO_SPACE_ID` (or `--space`) supplies space context for platform-scoped bots. Space-scoped bots resolve their space server-side.

## Command Structure (11 active domains, 284 operations)

Service commands are auto-registered. The hand-written leaves are `schema`, `version`, `api` (generic passthrough), `config`, `auth`, and the cobra-generated `completion`.

> **`matter` is temporarily withheld** (backend API not yet stable). The spec
> stays embedded — `octo-cli schema matter.*` still introspects it — but the command
> subtree and the `octo-matter` skill are hidden via the `x-octo-disabled` spec
> flag (`internal/registry/specs/matter.json`) and the skill's `disabled: true`
> frontmatter. Flip both off to re-enable. The tree below shows the full surface.

```
octo-cli matter    create | list | get | update | delete        (withheld)
               transition | close | reopen | archive | extract
               assignee add|remove
               channel  link|unlink
               timeline add|list|delete
octo-cli message   send | edit | sync | read-receipt
               search  (default) | all | files | media | around | groups
octo-cli group     list | get | members | md-get | md-update
               create | update | member-add | member-remove       (User Bot only)
octo-cli thread    create | list | get | members
               join | leave | md-get | md-update                  (User Bot only)
octo-cli file      upload | download | credentials | presigned
octo-cli bot       register | set-commands | user-info | space-members | typing | heartbeat
octo-cli event     list | ack
octo-cli drive     browse
               space  create|list|ensure-personal|get|rename|delete
               member list|add|set-role|remove
               folder create|list|rename|move|delete
               file   get|move|copy|rename
               blob   create|get|list|delete
               upload file|prepare|confirm|cancel
               download file|url
               doc    mount|unmount|list|candidates
               share  create|blob-create|list|revoke|access|download
               invite create|list|revoke|accept
               im-transfer create
octo-cli docs      create | list | search | get | rename | delete | forward-grant
               content  get|edit
               sheet    get|edit
               scene    get|edit|export
               members  list|set|remove
               share    get|set
               comments list|add|edit|delete
               versions list|create|state|rename|delete|restore
               attachments presign|get|resolve
octo-cli html      list | get | publish | versions | rm     (octo-doc HTML docs; distinct backend from `docs`)
               draft    save|promote
               share | unshare
               grant    add|list|rm
               asset    add|ls|rm
               comment  list|add
               element  get|replace
               reply

octo-cli auth      login | status | logout | list
octo-cli schema [--list [domain] | <operation-id>]
octo-cli api <METHOD> <PATH> [--params ...] [--data ...] [--service ...]
octo-cli config show
octo-cli completion bash|zsh|fish|powershell
octo-cli version
```

`octo-cli auth login` stores a bot token (read from a hidden prompt, `--with-token` stdin, or `--token-file` — never argv) under a profile keyed by `--bot-id`/`--profile`. `status`/`list` show metadata only (tokens always masked); `logout` removes a profile.

Bot-type capability and per-command flags are in `docs/octo-cli-design.md`. Agent-facing usage lives under `skills/` (`octo-shared`, `octo-matter` (withheld — see above), `octo-summary` (withheld — see CHANGELOG "Currently withheld" note), `octo-messaging`, `octo-files`, `octo-drive`, `octo-docs`, `octo-html`, `octo-marketplace`) — keep those in sync when command shapes change.

The hand-written drive leaves (`cmd/drive*.go`) are the exception to "everything is generated": `upload file` / `download file` / `share download` are multi-request transfers, `share create` branches on node type, and `share blob-create` / `share access` / `share download` take an argument shape (positional body field, whole share URL) the engine cannot express. They replace the generated leaf of the same name where one exists, so the spec still documents the endpoint for `octo-cli schema`.

## Environment

| Var                 | Purpose                                                  |
|---------------------|----------------------------------------------------------|
| `OCTO_TOKEN`        | Token (`app_*`, `bf_*`, or `uk_*`). Preferred env slot; wins over `OCTO_BOT_TOKEN`. |
| `OCTO_BOT_TOKEN`    | Octo or Loop credential (`app_*`, `bf_*`, `uk_*`, or `octo_loop_*`); used when `OCTO_TOKEN` is unset. |
| `OCTO_CREDENTIAL_MODE` | Set to `task` only for an isolated daemon-launched Loop task process. |
| `OCTO_BOT_ID`       | Robot id selecting a stored profile (env form of `--bot-id`). Selector, not a secret. |
| `OCTO_CONFIG_DIR`   | Override the credential dir (default `~/.octo-cli`).     |
| `OCTO_API_BASE_URL`  | Optional API base URL override; defaults to `https://im.deepminer.com.cn`. |
| `OCTO_SPACE_ID`     | Space context for platform-scoped bots.                  |
| `OCTO_FORMAT`       | Default output format (`json` | `table` | `csv` | `ndjson`). |

Universal flags: `--format`, `--jq`/`-q`, `--dry-run`, `--verbose`, `--timeout`, `--no-retry`, `--space`, `--bot-id`, `--profile`. Paginated ops additionally support `--page-all` / `--page-limit`.

## Code Style

- `gofmt`, `go vet`, standard-library `testing` with table-driven tests.
- Errors wrap with `fmt.Errorf("context: %w", err)`; CLI errors use the `*output.ExitError` taxonomy so envelopes stay structured.
- The `internal/output` package is a leaf — it must not import other `internal/*` packages.
- No mutable package-level globals; state flows through the Factory. The only package-level `var` declarations allowed are (a) ldflags-injected build metadata (`cmd/build.go`), (b) `//go:embed` file systems (`internal/registry`), and (c) immutable lookup tables (e.g. `backendErrorMapping` in `internal/output/errors.go`, `httpMethods` in `internal/registry/loader.go`). These are const-equivalent — never mutated at runtime — and exist only because Go doesn't permit `const` for their types.
- External deps limited to cobra, gojq, `golang.org/x/term` (hidden token prompt in `octo-cli auth login`), and the standard library.
- All text in English.

## Test Discipline

A test that passes is not evidence until it has failed. Write the assertion first,
run it against the unfixed code, and require it to fail **for the reason you
predicted** before writing the fix. If it passes before the fix, or fails for a
different reason, the assertion is wrong — fix the assertion, not the schedule.

This is not a preference. Assertions written after the code is already correct pass
whether or not they check anything, and this repo has produced several: a
`strings.EqualFold` comparison that ignored the case difference it existed to catch;
a chmod-target check satisfied by a `SameFile` guard three lines earlier; a
post-publication identity check satisfied by the pre-publication one. Each was found
later by mutation, which only tells you a test is weak once the bug is gone.

- **Predict the failure text**, then check the run matches it. "It failed" is not
  the bar; "it failed because the resolved address was never inspected" is.
- **Isolate the primitive.** If the property is about function X, call X directly.
  A path that reaches X through three earlier guards tests the earliest one.
- **Never assert on a substring you did not copy from the code.** Two of the above
  were checks for text nothing emits.
- **Mutation is the audit, not the proof.** Break the fix, confirm the test goes red,
  restore from a per-file `cp` backup — not `git checkout`, which silently does
  nothing for untracked files and takes unrelated edits with it when it does work.

Security boundaries additionally need a test in each direction: the unsafe input is
refused *and* the legitimate one still works. A guard with only the first half gets
widened by the next person who trips over it.

## Build & Test

```bash
go build -o octo-cli ./cmd/octo-cli
go test ./... -count=1
go vet ./...

# Shell completion (cobra built-in)
octo-cli completion bash   > /etc/bash_completion.d/octo-cli
octo-cli completion zsh    > "${fpath[1]}/_octo-cli"
octo-cli completion fish   > ~/.config/fish/completions/octo-cli.fish
```

Version metadata is injected via `-ldflags` at release time (see `cmd/build.go`).
