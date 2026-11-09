# Catalog reconciliation for XIN-2794

Draft [#234](https://github.com/Mininglamp-OSS/octo-cli/pull/234), issue [#233](https://github.com/Mininglamp-OSS/octo-cli/issues/233), continues the existing branch. Required CLI base remains `f8d434b6318215948ebbf67feb11f6265dee11e5`. This document supersedes the catalog findings in the v3 handoff; unchanged native/endpoint contracts remain in `contract-manifest-v3.json`. No supported catalog digest is emitted. No source pin, grammar, limit, capability or acceptance gate is changed by this report.

## In-grammar corrections delivered

Template SHA256 now binds the complete canonical descriptor excluding `sha256`, including renderer, media type, slots and payload. The prior payload-only hash did not protect slot metadata. Truth and descriptor hashes now use permission canonical JSON, preserving literal UTF-8/HTML characters instead of Go's default HTML escaping. The trusted offline encoder is separate from the bounded untrusted policy decoder. Retained v1/v2/v3 artifacts are historical; new v4 artifacts and hashes are explicit. No old artifact is silently replaced or promoted.

The earlier claim that approved server source lacked `/_search_media` was incorrect. At approved server `79701d4912eba79a8bcb02c58ed86609c38335e2`, `modules/bot_api/search_route.go` mounts the shared tree, `modules/messages_search/route_subtree.go` invokes its registered mounters, and `search_media.go` registers the route. Validation caps page size at 100; visibility pagination stops at that count and media projection creates at most one item per hit. `page-bounds-v2.json` corrects this witness. Four docs operations, rather than five operations, remain without selected page contracts.

## Exact uncompressed byte accounting

The regenerated canonical engineering shape is **14,127,786 bytes**, SHA256 `54f78644b0e09145c6883795124392b8eaca7480ba0a4bbae057b42bb1eca648` (**not a catalog digest**), against the approved **8,388,608-byte** artifact limit. This is the complete enumerated shape, not an impossibility proof for all equivalent in-grammar representations.

| Component | Bytes |
| --- | ---: |
| Content array, including framing | 13,571,077 |
| Entries | 534,514 |
| Profiles | 11,674 |
| Source-file records | 9,594 |
| Other top-level values | 723 |
| Top-level keys, separators and framing | 204 |
| Total | 14,127,786 |

The 5,407 content records comprise 2,458 templates (6,041,584B), 2,454 schema variants (637,134B), 417 byte records (1,194,337B), and 78 discovery indices (5,692,614B). Record sums exclude array commas. Dynamic operation templates account for 2,454; inventory/module-map shapes add 4. Every top-level byte is attributed in the machine-readable report and tested against the serialized total.

Template `value_base64` literals occupy 5,405,476B and discovery `row_base64` literals 4,869,168B: **10,274,644B before descriptor overhead**. Exact whole-record/payload deduplication already exists. Discovery repeats 1,179 distinct rows across 4,716 occurrences: 3,651,876 repeated base64 bytes. Removing those rows would discard named/global/facade surfaces under the current literal-row grammar. Likewise all 2,458 template payloads are distinct. There are 1,179 operation/facade selector keys with multiple templates and 26 scope/facade keys with three discovery alternatives. The closed schema provides no resource-negotiation, managed-field or server-facade selector to choose them. Dropping alternatives is not an equivalent correction.

## Concrete encoding option for disposition

`experiments/static-fragment-v4.py` is an offline measurement, outside release generation/serving. It factors repeated immutable JSON values into hashed literal skeletons and static references. It reconstructs **all 3,637 distinct template/discovery payloads byte for byte**, including member order and Go HTML escaping (7,174 occurrences), and measures **7,476,486B** with 1,441 fragments. Its 160B factoring threshold is an experimental parameter, not a new product cap. The result demonstrates a plausible bounded direction; it does not validate the full release, slots, selectors, graph budgets, runtime or migration compatibility. It emits no candidate release or digest.

Recommended minimal disposition: retain the 8MiB artifact limit; authorize design of immutable static-fragment/root references and explicit finite configuration selectors. Require canonical descriptor/reference hashes, cycle rejection, bounded fully expanded bytes/nodes, exact payload order/bytes, approved-slot pointer validity after expansion, closed selector domains and source-backed configuration correspondence. Then CLI and server must independently validate the same exact artifact and positive renderer/migration conformance before allowlisting. Existing content kinds, capabilities and independent U/E/native requirements remain intact.

Alternatives: continue searching for an equivalent representation inside the current closed literal grammar (none established here); or explicitly approve a larger decoded artifact budget after measurement. Gzip is only an evidence carrier and does not satisfy the decoded artifact limit. A larger budget alone does not resolve selector ambiguity. None of these departures is selected by this implementation.

Affected approved clauses: release schema-template `$defs.Content` closed union's template/schema_variant/discovery_index arms and `$defs.DiscoveryRow` fields and selectors; `$defs.Release.properties.source_heads`/`source_files`; canonical descriptor hashing and 8MiB decoded artifact limit; action-semantics-v6.md sections 1.1/5 (finite bounds), architecture O4 measurement and conditional O5 compatibility disposition. In particular, `source_heads` has `additionalProperties:false` and exactly fixed cli/endpoint/server pins, so approving docs provenance requires an explicit representation amendment, not adding an undeclared fourth head. The two approved whole-value slot domains stay unchanged. Any new representation requires architect/Owner disposition and corresponding exact schema/corpus migration, not a unilateral codec addition.

## Five endpoint traces and minimal producer requirements

`page-disposition-v4.json` gives exact source hashes, paths and line ranges. The approved server pin proves media search. Docs code was inspected read-only at **unselected** docs-backend `64be99c86e5a9924a5998dbc71343101a9a3672d`; it is not added to normative source heads. Its `src/api/app.ts:343-390` mounts the Bot docs/comments routers behind Bot authentication and documents the existing gateway split. A Go server route change is not needed to manufacture the three observed count bounds.

| Operation | Observed returned items | Concrete remaining requirement |
| --- | ---: | --- |
| message.search.media | <=100 | Approved server witness corrected; no count change required. |
| docs.comments.markers | <=100 | Approve exact docs source and gateway provenance representation; SQL LIMIT and one-row projection already bound count. |
| docs.comments.replies | <=25 | Same source selection; both PPT and non-PPT return 25. Non-PPT acquisition still reads the whole thread before slicing. |
| docs.recent.list | <=100 | Same source selection; repository uses LIMIT size+1 then truncates. |
| docs.search | No fixed source maximum | Selected producer needs an approved maximum<=1000 plus returned-hit validation, or an explicitly pinned configuration/OpenSearch contract and source/config representation. |

Docs search defaults `SEARCH_PAGE_SIZE_MAX` to 50 but `numMin` actually uses `Math.max(min,n)` without an upper bound or finite/integer validation. Its nearby comment claims stronger validation than the implementation. The route clamps only to this configured value; `osClient.ts` sends that size and maps returned hits without its own maximum-count check. A default is not a universal bound. A new hard maximum changes previously accepted larger configurations; that compatibility decision cannot be silently selected. No docs-backend code was edited.

Returned-item counts do not prove byte bounds. Variable-size payloads and non-PPT whole-thread acquisition remain distinct. Ordinary business `Request.MaxResponseBytes` is unset; a blanket8MiB/16MiB response cap is not source-equivalent by assertion. Managed 16MiB complete serialized request spools include framing overhead beyond the approved 16MiB local-input domain, and the stdio relay buffers responses under 8MiB while ordinary HTTP responses have no matching universal limit. These are explicit boundary obligations, not newly approved output caps. Source-declared binary describe/inline behavior must also be preserved. CLI owns profile/renderer/spool/relay reconciliation after selected compatible source bounds or a bounded compatibility amendment; producer count evidence alone cannot close it.

## Remaining ownership and verification limits

CLI retains complete release validation/emission/allowlisting, per-entry selected profiles, selector reconciliation and old/target semantic migration-equivalence proof. This work does not wait for endpoint completion to investigate catalog defects. The endpoint's delivered control/relay/chunk ABI and its factory/import/publication/reuse/renewal/OS work remain separate. Server/native SQL/startup and actual serving IM/HA/recovery ownership remain as recorded in `remaining-producers-v3.json`. No endpoint dispatch, sibling write, new resource, fourth-repository edit, shared configuration, runtime/provider or credential change occurred.

Descriptor metadata tampering and byte-ledger regressions were demonstrated red before implementation, then green. Complete source-content comparisons, negative and positive managed transport/coordinator tests run separately; fixture results are not native acceptance. Native/OS/cross-repository/browser acceptance remains NOT_RUN. This repository has no frontend UI. Rollback reverts these Draft changes; existing journals/fences/epochs must not be erased. Formal review, ready conversion, deployment and merge remain outside this continuation.
