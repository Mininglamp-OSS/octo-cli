# Permission coordinator components (XIN-2794)

This is a partial implementation of [issue #233](https://github.com/Mininglamp-OSS/octo-cli/issues/233). Production MCP enforcement is **not wired**, the catalog is not a supported release, and native/cross-repository acceptance is **NOT_RUN**. These components must not be used to claim completion of the two permission stages or to enable an unprotected managed endpoint.

The required base and target are `chore/main-snapshot-20261029-mcp` at `f8d434b6318215948ebbf67feb11f6265dee11e5`. The original prepared checkout at `fa07e9150ec75d542546006921611144b7268f3b` is preserved by `backup/xin-2794-pre-permission`. That merged Drive work is unrelated. Implementation branch: `ken-anderson2006:feat/xin-2794-permission-coordinator`. The exact delivered head is recorded in the Draft PR and task outcome.

## Delivered components

`internal/permission` implements the complete ten-field P reader, duplicate decoded-key/UTF-8/surrogate/depth/size checks, canonical JSON and digest verification, uint64 revisions/epochs, closed selectors and independent U/E stage evaluation. A rule must match the complete caller/source/capability tuple; applicable denies win. Parent rules apply to threads only with explicit descendants. Native existence, canonical record spelling, original visibility and current authority remain provider responsibilities. The immutable shared schemas and corpus are copied without regeneration; their source references and hashes are in `contract-manifest-v1.json`.

The coordinator accepts newly read revisions even when they deny. One shared instance owns the watermark for each exact origin/Bot/Space/incarnation. Equal-revision equivocation, revision/epoch regression and obsolete generation epochs refuse. Facts precede each newly initiated complete policy read; newly activated requirements force another F/B round, with three rounds total. Contention behind another first write discards the old proof. Private host handles are copied, opaque, revocable and unusable with a restarted coordinator. `Binding`/`Facts` are in-process composition seams, **not** an alternate CapsuleV2/native-facts wire protocol or an authentication implementation.

The fixed private self-policy reader performs only exact-Bot, explicit-Space GETs of `/v1/bot/permission-bundle/v1`. It requires a deadline, never caches P, follows no redirects and rejects 304, oversized/compressed/partial responses, semantic errors and alternate keys. Managed-v2 declaration and minimal identity parsers preserve full native scalar/byte domains and reject legacy projections, extra owner/display fields, decoded duplicates and coerced numeric versions. These parsers do not themselves negotiate or authenticate a managed connection.

The owned HTTP component seals one request and completes a fresh direct TLS handshake before F/B. It performs one synchronous application write under the watermark lock, with no `http.Transport`, pool, proxy, redirect, authentication resend or retry loop. Actions are consumed on refusal as well as execution. Short writes and response loss remain `result_unknown`; a later refusal makes no claim about earlier effects. This component is not connected to the business factory. Effective credential/Space/actor forcing, complete PreparedActionV1/capsule identity, all OS/TLS first-write acceptance, response bounds and checked result publication remain integration work. Replacing the existing transport with an outer `Do`/`NoRetry` wrapper would not satisfy that work.

## Actual catalog content

`go run ./scripts/permission-catalog` uses the real embedded registry, exposure table, mapping, facade renderers and resource filter. Its checked-in gzip output contains 827 candidate entries, **393 OperationDetail truths, 1,179 base facade responses and 24 filtered resource files**, plus inventory/discovery baselines. Every byte blob is SHA-256 addressed, and every entry has source/reachability references and explicit unresolved work. The source entry/profile projection comes from the exact approved candidate (`1ef7021469a3bab55c48361c47318d6852daf1f401909a9e46b61f71d7b293fb`); it does not replace that candidate.

The output is a finite engineering artifact with `catalog_digest: null`, not an allowlist. Resource-negotiation/forced-context whole-value templates, dynamic discovery templates, source-compatible response acquisition/count/aggregate bounds and all fixed-program sink integrations remain incomplete. Counts and a package/index hash cannot close these gaps. `source-materialization-v1.json.gz` is deterministic, contains no business content, and is verified against fresh rendering and every referenced blob/source path.

To inspect or reproduce it without changing the installed CLI:

```sh
go run ./scripts/permission-catalog > /tmp/xin-2794-materialized.json
gzip -dc docs/permission/source-materialization-v1.json.gz > /tmp/xin-2794-retained.json
cmp /tmp/xin-2794-materialized.json /tmp/xin-2794-retained.json
```

## Dependencies and remaining integration

The exact sibling contracts were read and verified:

| Dependency | Exact head | Contract |
| --- | --- | --- |
| Server Draft #951 | `36ab3dedf9354a7a0cad1a712831c132edd2e08f` | `pkg/permissionbundle/contracts/handoff-v1.json`, SHA-256 `31cf10807ef0fac36c3d214354fbb69bcea0c79ee8a217d38644ad289893cfae` |
| Endpoint Draft #102 | `c152cb54df8e3fb822288933e39cf6dcad4b9335` | `docs/permission-component-contract.json`, SHA-256 `3235d448421f4bfd34977d474a9a0568d720dfe8d12c825085eef463b6d13e83` |

The server explicitly lacks the native fact route, real authority/read-write/recovery fencing, association-commit integrations and a supported release catalog. The endpoint explicitly lacks native Header/current-access/disclosure/body/checkpoint/fence adapters, authenticated worker/control/relay containment and production admission/import/publication/refusal gates. Neither sibling contract supplies an authenticated private capsule or native facts provider that the CLI can safely invent.

Remaining CLI work includes private authenticated Hello/CapsuleV2 control and one-use turn relays; complete context/contributor/native predicates; dispatch/declaration/refusal integration across both facades, HTTP/stdio and resources; and every root/child/page/hop/retry/local read/secret Redis/portable/publication sink. The catalog's unresolved compatible bounds/templates must be resolved before producing the single supported release shared by server and endpoint. The component seams here are not substitutes for those integrations. Native ACL, no-OBO and strict/compatible legacy behavior must remain independent.

PjM/Dennis-mini owns dependency coordination and selection of an independent backend/security reviewer for the exact Draft head. CLI continuation stays on this task and fork. No independent review, deployment, ready conversion, merge, release or human acceptance is performed here. Full acceptance remains blocked; this is not completion by permanent refusal.

## Validation and rollback

Focused race tests exercise synthetic shared-server bytes, strict negative vectors, Unicode domains, all four stage-state pairs, E/E positive rules over actual TLS, cross-rule denial, native revocation without revision change, revision concurrency/equivocation, epoch reset, restart/closed handles, unknown authority/capability, response loss, refused retry and zero refused-attempt receiver count. The initial group-default test failed until the seal helper filled input defaults on a detached map; the final reader still requires explicit canonical output fields. Catalog checks validate actual content, every blob hash/reference and existing source paths; the first source-path check caught an incorrect secret source reference.

Passed: `go test -race ./internal/permission -count=1`; the focused permission/catalog race suite; `go test -race -count=1 -timeout=20m ./...`; `go build ./...`; `go vet ./...`; formatting, diff whitespace, contract/artifact byte checks; and the repository-pinned `golangci-lint v2.12.2` (zero issues). No new module dependency is added.

The full race run used an identical source copy under a private `/Users/mlamp/.xin-2794-selftest-*` directory. The daemon workspace's `/Volumes/MacDataXY` ancestor has mode 0775, so the pre-existing request-log ancestor check rejects it. An untouched-base test binary reproduces `REQUEST_LOG_STARTUP_FAILED` in the workspace and passes the same admission test in the private directory. The initial workspace-wide run consequently failed and timed out after ten minutes; shared volume permissions and production guards were not changed. The successful private-directory race run included the existing HTTP/stdio built-binary regression tests. There is no UI for this CLI-only component change. Browser/native frontend-to-backend and cross-repository acceptance are NOT_RUN because the named production interfaces are undelivered; TLS synthetic receiver tests are component evidence only.

There is no database migration, persisted permission state, command flag, global installation or daemon/configuration change. Removing these additive files and the engineering artifact rolls back this partial delivery without altering existing settings or CLI/MCP envelopes. Future complete integration must preserve server incarnations/epochs/fences and endpoint replay journals during rollback; deleting those records is not a safe recovery strategy.
