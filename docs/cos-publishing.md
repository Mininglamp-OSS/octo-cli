# Optional COS publishing

This repository builds and uploads **CLI packages only**. The daemon repository
owns the unified `install.js`, CLI/daemon version-pair promotion and the complete
installation runbook (`docs/cos-publishing.md` there). Existing npm and GitHub
release workflows are unchanged; these tools never run `npm publish` or push tags.

## Release contract

| Source branch | Version | COS prefix (example) |
| --- | --- | --- |
| `test` | `X.Y.Z-next.N` | `static/octo-loop-test` |
| `main` | `X.Y.Z` | `static/octo-loop` |

Versions follow the CLI's existing `next.N` convention. The committed source
branch selects the environment; it is not a separate upload switch. An optional
leading `v` is normalized away. Build numbers have no leading zeroes.

Both environments use the same scripts:

- `build.js`: test committed source, build six native archives, probe the host
  version with `--format json`, then pack and verify six self-contained packages.
- `publish.js`: preview or upload the verified packages to COS.
- `lib.js` / `cos.js`: shared validation and authenticated COS transport.
- `tests/`: build/package, validation and publication tests for both environments.

Supported targets are macOS, Linux and Windows, each with amd64 and arm64. A build
checks all six archive/package hashes and executes only the host binary; native
runtime testing on the other platforms requires matching hosts.

## Local setup

Required: Node 22+, npm, Git, Go as specified in `go.mod`, GoReleaser 2.16+ and
`tar`. Only the publisher needs the COS SDK. Use a macOS/Linux publishing host;
Windows is supported as an artifact target but the publishing workflow has not
been validated on a Windows host.

```sh
npm ci --prefix scripts/release --ignore-scripts --no-audit --no-fund
npm test --prefix scripts/release
cp scripts/release/config.example.json scripts/release/config.local.json
cp scripts/release/.env.example scripts/release/.env.local
chmod 600 scripts/release/config.local.json scripts/release/.env.local
```

Edit the local copies only. JSON contains the bucket, region, HTTPS CDN origin
and disjoint test/main prefixes. Credentials belong in `.env.local`:
`COS_SECRET_ID`, `COS_SECRET_KEY`, and optional `COS_SESSION_TOKEN`. Do not put
credentials in JSON, shell arguments, examples, logs or build artifacts.

Local config, `.env` files, release dependencies and `release-dist/` are ignored;
only placeholder examples are tracked. Confirm before committing:

```sh
git check-ignore scripts/release/config.local.json scripts/release/.env.local release-dist/check
git check-ignore scripts/release/config.example.json scripts/release/.env.example
```

The first command prints all three paths; the second prints nothing (exit 1).

## Build, preview and upload

Fetch the intended source branch first. The builder prefers `origin/<branch>`,
falling back to the local branch only when that remote ref is absent. It clones
that exact commit into a temporary checkout; uncommitted source and local
configuration are not copied. Existing GoReleaser build/archive settings are
reused without remote publishers or hooks.

```sh
git fetch origin test
node scripts/release/build.js --ref test --version X.Y.Z-next.N
node scripts/release/publish.js --dist release-dist/X.Y.Z-next.N/npm
node --env-file=scripts/release/.env.local scripts/release/publish.js --dist release-dist/X.Y.Z-next.N/npm --execute
```

Replace the example version with a new prerelease. For production, fetch `main`,
use `--ref main --version X.Y.Z`, and pass its `release-dist/X.Y.Z/npm` directory
to the same publisher. The output directory must not already exist.

Build runs source tests, the existing npm packaging tests, native host smoke and
COS package verification before exposing its output. It does not install or
change any existing CLI/daemon command. Both commands support `--help` without
local configuration or credentials. Build subprocesses receive an environment
with `COS_*` variables removed; the publishing process retains its credentials.

Without `--execute`, publication validates local files and prints object keys;
it makes no cloud requests. With `--execute`, it also validates source provenance,
uploads immutable objects and reads them back through the configured CDN. Each
package buffer is checked against the manifest immediately before upload, and
the same buffer is used for COS/CDN verification. The original validated manifest
bytes are uploaded last, preserving byte-for-byte retry compatibility.

```text
<environment-prefix>/cli/npm/
  releases/<version>/
    octo-cli-<version>-darwin-amd64.tgz
    octo-cli-<version>-darwin-arm64.tgz
    octo-cli-<version>-linux-amd64.tgz
    octo-cli-<version>-linux-arm64.tgz
    octo-cli-<version>-windows-amd64.tgz
    octo-cli-<version>-windows-arm64.tgz
    npm-release.json
  .publish-lock                 # transient, owned by one publisher
  .publish-probe-<uuid>          # transient conditional-write check
```

The packages retain `@mininglamp-oss/octo-cli` identity. Each contains package
metadata, a Node launcher and one native binary; no registry dependencies or
lifecycle scripts are required. `private: true` prevents accidental registry
publication. The outer `.tgz` SHA-256 verifies the complete archive; per-file
hashes in the manifest are also consumed by the daemon installer after installation.
Launchers forward SIGINT/SIGTERM and preserve native exit codes/signals. Changes
to launchers require a new package version; replacing the root installer cannot
change an existing package.

## Permissions and recovery

Grant object Get/Put for the selected environment's `cli/npm/` prefix, and Delete
only for `.publish-lock` and `.publish-probe-*`. Bucket listing is unnecessary.
Read-back through the CDN must be publicly accessible over HTTPS. The CLI
publisher needs no write access to root `install.js` or `installation.json`;
those mutable execution/activation objects belong to the daemon publisher.

Conditional creation (`x-cos-forbid-overwrite`) must actually work. The tool probes
that guarantee before release writes and refuses buckets that ignore it (for
example, incompatible versioning configurations). Releases cannot be replaced
with different bytes. Retry with the **same original artifact directory**; do not
rebuild an uploaded version. A partial upload is safe to retry, but does not
activate anything.

A per-component/environment lock prevents concurrent publishers. Cleanup checks
ownership before deletion, including after a lock/probe creation request times
out with an uncertain server-side result. A cleanup warning does not turn a
verified upload into a failure or hide the original upload/CDN error. Inspect the indicated lock key;
remove it only after confirming no publisher is active. A failed preflight probe
cleanup prevents publication. There is no automatic stale-lock timeout.

## Unified installation and CI

After both component versions pass CDN verification, use the daemon repository's
release runbook to promote their version-pair manifest and publish the root
`<environment-prefix>/install.js`. That installer owns test commands/state
isolation and production npm-compatible installation. CLI publication writes no
installer, `latest.json`, or installation pointer. The former component-native
installer is no longer generated or supported; use the daemon-owned root entry.
Already uploaded legacy objects are not deleted automatically.

CI validates these tools without cloud credentials or publishing. A future
manual `workflow_dispatch` can invoke the same commands, with test/main-only refs,
protected environment credentials and per-environment concurrency. It must
verify that the builder's resolved source SHA equals the pipeline commit. Keep
COS publication opt-in and separate from the existing npm release workflow.
