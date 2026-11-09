# V42 catalog package delivery

This implements the exact Owner-approved corrected v2 amendment for XIN-2794. The normative attachment is `01a1bfb4-aeef-7497-8f7f-268335e0b0fa`, 70,457 bytes, SHA256 `f8bcf1af183fcb69b5c40575d26e21b8a37e7e8fb6720b53e4013924ad078725`. Approval resolves the earlier packaging/selector disposition. It does not select docs provenance, business response bounds, native authority producers or a supported release.

The complete reproducible candidate is [catalog-package-v2/manifest.json](catalog-package-v2/manifest.json), format `permission-catalog-package-v2`, contract revision 3, semantics version `1`, candidate version `octo-capabilities-v1-xin2748-v4`. Its raw canonical manifest SHA256 is `ecc56c2ab0a2c520ab7e5203d2abab06671daf90c28d69f88fe1de8d154109fc`. This is a package commitment, **not an allowlisted catalog**. Production supported pins and directed compatibility pairs remain empty. Historical v1/v2/v3/v4 reports and schemas remain unchanged.

## Complete package and independent accounting

Run `go run ./scripts/permission-catalog --package-output <new-private-directory>`. This offline command enumerates the source configurations, writes an exclusive read-only package only after independent whole-package verification, and emits [catalog-package-report-v5.json](catalog-package-report-v5.json). Git checkout files are source fixtures; private bootstrap staging requires the isolated read-only output, not a writable checkout. The command does not change support tables. The retained package and a second final production are byte-identical.

| Measurement | Actual | Approved maximum |
| --- | ---: | ---: |
| All stored bytes, including manifest | 7,823,327 | 8,388,608 |
| Pre-slot expanded bytes | 10,202,896 | 16,777,216 |
| Post-slot expanded bytes | 11,603,961 | 16,777,216 |
| Metadata JSON nodes | 158,934 | 1,048,576 |
| Pre-slot nodes, including metadata | 399,778 | 1,048,576 |
| Post-slot nodes, including metadata | 398,617 | 1,048,576 |
| Maximum JSON depth | 15 | 32 |
| Files | 30 | 64 |
| Manifest bytes | 4,934 | 65,536 |
| Largest metadata file | 262,092 | 262,144 |
| Largest raw blob | 1,048,576 | 1,048,576 |
| Raw roots | 4,128 | 8,192 |
| Shared rows | 1,179 | 4,096 |
| Content records | 5,481 | 16,384 |
| Parts | 15,649 | 65,536 |
| Maximum parts per root | 25 | 256 |
| Largest reconstructed root | 304,934 | 8,388,608 |

The source producer measures actual source-rendered outputs with its own JSON traversal and occurrence ledger. The independent permission consumer reconstructs roots, validates typed edges, renders source-valid joint slot assignments and computes its own ledger. Every field above agrees. Each distinct bytes/template descriptor and each discovery row position contributes one payload occurrence; references and selector cells do not multiply descriptor uses. Unused raw roots receive the required minimum-one charge. Metadata nodes and pre/post budgets are separate. None of these constants is a business response, input or relay cap.

Whole-resource storage measures 11,088,524 bytes. Deterministic exact raw byte-slice reuse brings the complete package within 8 MiB. The producer tries fixed matching windows and chooses window 384 for this source; that heuristic is not a consumer limit. Blobs contain uncompressed raw bytes, and every root has only ordered, one-level `(path, offset, length)` slices. There is no recursive recipe, runtime codec, macro, patch interpreter, hidden padding or JSON reserialization of raw roots.

## Coverage and verification

All 827 exact baseline identities, aliases, targets and ten fixed profile programs are present. The source heads remain CLI `f8d434b6318215948ebbf67feb11f6265dee11e5`, server `79701d4912eba79a8bcb02c58ed86609c38335e2`, endpoint `5dfde6cfd56621b0fbcf9ec7f310c7caa5135f34`. The 97 exact source-file tuples have their raw bytes in the typed closure; the embedded source carrier checks each against that baseline. No docs-backend fourth pin is introduced.

The independent renderer compares all **56,592 schema cells**, **1,248 static cells** (all 26 entry IDs across 48 configurations) and **1,248 discovery source observations**. Discovery has 156 selector cells; managed-mask invariance is checked against all eight source masks. Comparisons include exact bytes, static media, matching text, query bounds and truncation-note contracts. Finite selectors have complete disjoint partitions, with no priority or wildcard inference.

The loader rejects missing/unlisted files, tampered hashes, duplicate decoded metadata keys, wrong kinds, noncontiguous shards, changed identities, invalid/cyclic profile edges, owner mismatches, unreachable records/roots/rows, range overflow, raw-hash mismatch, blob padding, selector gaps/overlap, omitted discovery operations, malformed or undeclared slot markers and budget violations. Metadata uses its closed canonical domain; inherited `eq.literal:null` remains valid. Raw declared JSON preserves original whitespace, number spelling, repeated members and the declared Go renderer's string byte domain. Its parser counts every repeated value without importing metadata canonical/Unicode/number filters. Opaque bytes are not parsed as JSON or forced to UTF-8. Template slots replace whole values at exact RFC6901 pointers, reject ambiguous repeated marker/pointer ancestors and accept only their declared source domains. Post-slot JSON uses the separate expanded ceiling, not the smaller per-root limit.

Filesystem staging uses a pinned directory, two complete inventories, regular read-only single-link files, descriptor identity checks and detached byte copies. Symlinks, hardlinks, writable files and extras refuse. The private HTTP client retrieves only a manifest and its listed files from the fixed self authority, with the same original deadline, exact pin/Space/Bot, no redirects/compression and fresh server eligibility on every GET. Eligibility loss during download yields no snapshot or partial authority.

`CatalogRegistry` freezes private supported pins and explicitly directed pairs, verifies complete semantic equality before atomic publication and preserves the previous snapshot on any failure. Representation-neutral comparison retains permission programs, entry identity, rendered content, selectors, source contracts and inherited limits while ignoring only packaging layout/recipes/shards and packaging constants. A fixture changes blob placement and version without changing semantics; a changed profile fails. Reverse compatibility is never inferred. This is the CLI compatibility seam; real server policy migration remains the existing atomic CAS operation, not a local policy rewrite.

## Private endpoint and runtime ABI

The consumed endpoint is Draft #102 at `afb732b20e63baf8acf06c4726a66e290b98e1b6`. Its `docs/permission-endpoint-handoff-v4.json` is 6,435 bytes / SHA256 `cc193256548ecb3b5c09b0a739f1cd5a7ca5503b1fb66e2a7bd5684ddcbd4671`. Its control client is 11,710 bytes / SHA256 `4e40ee144b5eec7ef645be92948dd6b1f36a838d1fb82e6b471a59e820c99bef`.

The private endpoint descriptor v2 names `{catalog_format,catalog_version,catalog_digest}`. Existing HostConfig, flags and Hello stay unchanged. For an approved v2 pin, `release_path` is the absolute path to **manifest.json**, and `release_sha256` is the raw canonical manifest digest. The private compiled pin map selects the format. The host independently verifies the entire isolated package and installs the immutable snapshot before listeners/readiness. The endpoint's identity preflight is not full package verification. An operator-supplied digest or identity-only fixture cannot promote this candidate. The separately versioned `permission-wire-v1-package-v2.schema.json` adds the approved closed v2 catalog discriminator; the original wire schema is retained.

Managed schema, discovery, static resources, resource inventory and module-map reads use the coordinator snapshot inside a fresh facts/policy boundary before consuming cached bytes. Every final output still has its own existing check. Actual execute/call_op actions continue through the owned physical transport. No public MCP argument selects a package, path, pin or compatibility pair.

The private catalog GET is `/v1/bot/permission-bundle/v1/catalog?format=permission-catalog-package-v2&catalog_version=...&catalog_digest=...&space_id=...`; `/catalog/file` additionally accepts the exact listed `path`. These are private eligible reads, not business capabilities or policy grants. CLI implements a client; server owns eligible inventory-only serving/storage and real migration CAS composition.

The latest server handoff was read at Draft #951 head `ff4d43c403083fd059ec2e6ef6e0d6d505228d48`: `pkg/permissionbundle/contracts/handoff-v4.json`, 8,285 bytes / SHA256 `c226b46007904c37ef5b46f78b587cd734f4ac4ccf8c739f786e87cfe4573791`; contract manifest v4, 5,067 bytes / SHA256 `39b138a4f689f3695c087ceab0598b4da74a632ec6276ee159b6c5c586c0f230`. It delivers independent v2 verifier/registry/read components with synthetic fixtures. Its new schema definitions match this delivery after resolving inherited references; wire definitions are identical apart from document ID/comment. This supersedes the earlier missing-server-verifier statement, without claiming that server has consumed this real package.

Its private `SlotValues(ctx, SlotCell)` callback keys on exact Pin, EntryID, facade, Resources, ManagedMask and ServerFacade. [source-slot-witness-v5.json.gz](source-slot-witness-v5.json.gz) supplies source-derived joint values for that seam, outside package metadata: compressed 12,562 bytes / SHA256 `0c5da173bd0f53c64562a70a5c5feebc955ad111bbe84ed11f5588c08b4b9c0b`; decoded 843,132 bytes / SHA256 `0dac894681aa7f4f8810f5fa5574d68ce071f2a401df4a99da4a92e7a43e7c5b`. Reproduce with `go run ./scripts/permission-catalog --slot-witness <manifest-sha256>`. The artifact observes every 56,592 schema source cell and groups 29,070 slot-bearing cells into 2,700 disjoint exact selector records. Values are read directly from actual source output and keyed by **domain**, as the server callback requires; the CLI's internal renderer keys by slot ID. Both domains in a record belong to the same cell. There are no slot-bearing static templates in this package.

To bind it, trusted server composition first checks the external witness commitment and exact pin, expands each finite selector against the callback's cell (server facade order `three`, `two`, `both`), and returns a detached domain-value map. Missing/duplicate assignments refuse. Every package slot-bearing cell is independently checked against this witness and the CLI consumer's source-derived values. This artifact is source evidence for independent consumer conformance, not a new protocol, self-approved support pin or oracle supplied by package metadata. Its gzip is only an evidence container; package blobs remain raw and uncompressed. Server owns that trusted adapter and the actual-package verifier run; no sibling source is edited here.

## Tests and residual dependencies

Synthetic end-to-end tests use the complete package through real pinned mTLS control, HTTP turn relay, worker stdio, both action facades, schema/discovery/resources/inventory, and the actual built Go relay executable driven by the exact endpoint client above. Cached resource reads refuse fresh native removal even with an unchanged policy revision. Private read tests cover complete download and mid-download eligibility loss. Registry tests cover atomic publication, explicit direction, representation changes and changed permission programs. Fault tests cover commitments, selectors, typed roots and filesystem aliases. Earlier v1 managed/revision/epoch/retry/recovery tests remain in place.

Two independently actionable source-equivalence defects are fixed: managed preparation no longer imposes an extra 16 MiB serialized request cap, and the stdio relay no longer borrows the incoming request's 8 MiB limit for responses. A source-valid 3 MiB string whose escaped request exceeds 16 MiB passes both preparation paths; a 9 MiB relay response passes. Source local-input, incoming request, deadline, header and command-specific limits remain. These positive fixtures are not a universal per-entry response acquisition proof.

The remaining support dependencies are specific:

- **Docs provenance and counts:** the four docs page contracts remain unselected. The existing [page disposition](page-disposition-v4.json) records observed markers/replies/recent maxima 100/25/100 in read-only, unselected docs-backend `64be99c8`; docs search has a configurable default with no fixed compatible maximum and lacks independent returned-count validation. PjM must obtain an explicit compatible provenance representation/source disposition and the docs producer's maximum/count validation. Only the three approved original heads are representable today. CLI then completes the corresponding source-bound profile support and exact release/pair approval evidence.
- **Per-entry source bounds:** ordinary business assembly does not provide a universal finite `MaxResponseBytes` acquisition/aggregation contract. The old artificial spool/relay limits are removed, but this does not prove all per-entry finite profiles or output acquisition bounds. PjM/source owners must supply compatible actual contracts or an authorized disposition; CLI retains their integration and positive boundary proof. Packaging approval cannot substitute a new response cap.
- **Independent server composition:** server has delivered its v4 synthetic verifier/read/registry components. It owns trusted binding of the supplied source witness, independent verification of this exact real package, actual storage/startup/support-pair composition and atomic migration CAS. No real cross-repository migration or deployment is claimed here.
- **Native and endpoint integration:** actual delivered-Header/current-access/history disclosure/replay checkpoint and elected-current SQL/recovery producers remain separate. Endpoint retains dependent history reference bridge, safe prior-generation reuse, physical model/native publication and actual OS containment. Synthetic fixtures are not serving producers.

The compiled support tables therefore remain empty. The final task evidence records exact tests, source-copy verification and hashes. Full source/native/browser/OS/cross-repository acceptance is **NOT_RUN**. There is no CLI frontend UI. This continuation is not a partial-delivery review handoff: independently actionable v2 producer/consumer/runtime work is delivered, while the precise remaining source dependencies keep the task blocked. Rollback reverts the Draft changes, leaves managed mode disabled and preserves native journals, policy epochs and fencing state. No deployment, ready conversion or merge is authorized.
