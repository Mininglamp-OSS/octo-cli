"""Unselected, offline size experiment; no release digest or serving codec.

Usage: python3 static-fragment-experiment.py release-assembly-report-v4.json
The 160-byte threshold is an experiment parameter, never a production cap.
"""
import base64
import collections
import hashlib
import json
import pathlib
import sys


def compact(value, html=False):
    raw = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode()
    if html:
        for char, escape in [(b"&", b"\\u0026"), (b"<", b"\\u003c"),
                             (b">", b"\\u003e"), ("\u2028".encode(), b"\\u2028"),
                             ("\u2029".encode(), b"\\u2029")]:
            raw = raw.replace(char, escape)
    return raw


report = json.loads(pathlib.Path(sys.argv[1]).read_bytes())
release = report["release_shape"]
payloads = []
for record in release["content"]:
    if record["kind"] == "template":
        payloads.append(record["value_base64"])
    if record["kind"] == "discovery_index":
        payloads.extend(row["row_base64"] for row in record["rows"])
frequencies = collections.Counter()


def walk(value):
    raw = compact(value, True)
    if isinstance(value, (dict, list, str)) and len(raw) >= 160:
        frequencies[raw] += 1
    if isinstance(value, dict):
        assert "$static_ref" not in value
        for item in value.values():
            walk(item)
    if isinstance(value, list):
        for item in value:
            walk(item)


for payload in set(payloads):
    original = base64.b64decode(payload)
    value = json.loads(original)
    assert compact(value, True) == original
    walk(value)
fragments = {}


def factor(value, root=False):
    raw = compact(value, True)
    if not root and frequencies[raw] > 1 and len(raw) >= 160:
        digest = hashlib.sha256(raw).hexdigest()
        if digest not in fragments:
            fragments[digest] = compact(factor(value, True), True).decode()
        return {"$static_ref": digest}
    if isinstance(value, dict):
        return {key: factor(item) for key, item in value.items()}
    if isinstance(value, list):
        return [factor(item) for item in value]
    return value


roots = {payload: compact(factor(json.loads(base64.b64decode(payload))), True).decode()
         for payload in set(payloads)}


def expand(value):
    if isinstance(value, dict):
        if set(value) == {"$static_ref"}:
            raw = compact(expand(json.loads(fragments[value["$static_ref"]])), True)
            assert hashlib.sha256(raw).hexdigest() == value["$static_ref"]
            return json.loads(raw)
        return {key: expand(item) for key, item in value.items()}
    if isinstance(value, list):
        return [expand(item) for item in value]
    return value


for payload, skeleton in roots.items():
    assert compact(expand(json.loads(skeleton)), True) == base64.b64decode(payload)
candidate = json.loads(json.dumps(release))
for record in candidate["content"]:
    if record["kind"] == "template":
        original = record.pop("value_base64")
        record["value_ref"] = hashlib.sha256(base64.b64decode(original)).hexdigest()
    if record["kind"] == "discovery_index":
        for row in record["rows"]:
            original = row.pop("row_base64")
            row["row_ref"] = hashlib.sha256(base64.b64decode(original)).hexdigest()
# Preserve object-member order as literal skeleton text, rather than rely on
# a canonical outer JSON encoder retaining nested payload member order.
candidate["EXPERIMENTAL_STATIC_FRAGMENTS"] = fragments
candidate["EXPERIMENTAL_ROOTS"] = {
    hashlib.sha256(base64.b64decode(payload)).hexdigest(): skeleton
    for payload, skeleton in roots.items()
}
print(json.dumps({
    "status": "UNSELECTED_GRAMMAR_EXPERIMENT_NOT_A_RELEASE",
    "input_shape_bytes": report["assembled_bytes"],
    "experimental_shape_bytes": len(compact(candidate)),
    "all_payload_occurrences": len(payloads),
    "distinct_payloads": len(roots),
    "static_fragments": len(fragments),
    "exact_payload_byte_roundtrips": len(roots),
    "threshold_bytes": 160,
    "catalog_digest": None,
    "limitations": "Not the approved grammar; no supported digest, selector reconciliation, full release validation, decoded-budget or runtime acceptance. Only exact static payload roundtrips and total JSON size measured."
}, indent=2))
