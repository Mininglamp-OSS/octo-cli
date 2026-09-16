# Local releases and optional COS publishing

This workflow releases **octo-cli only**. Daemon packages and the unified
installer are published from the daemon repository. Existing npm/GitHub release workflows are
unchanged. CI additionally validates the release tools without publishing to COS.
No command in these scripts runs `npm publish`, creates
GitHub/GitLab Releases, pushes tags, or starts/restarts the daemon.

For **one-command CLI + daemon installation** (isolated test or npm-compatible production), start with
[the daemon-owned runbook](https://codex.mlamp.cn/dmwork/octo-daemon-old/-/blob/test/docs/cos-publishing.md)
and the self-contained npm section below. The native component installer described
here is a separate maintenance tool: it does not isolate test state and is not the
recommended unified test installation. The runbook link becomes available when the
daemon MR is merged into `test`.

## Files and dependencies

- `scripts/release/build.js`: test committed source and create six GoReleaser archives.
- `scripts/release/verify.js`: validate archive hashes; optionally smoke-test the host installer.
- `scripts/release/publish-cos.js`: preview by default; authenticated upload only with `--execute`.
- `scripts/release/install.js`: source template for a standalone Node installer, bundled at publish time.
- `runtime.js`, `lib.js`, `project.json`: release validation, repository identity and helpers.
- `config.example.json`, `.env.example`: placeholders only. Never put real credentials in these files.
- `package.json` / `package-lock.json`: private release-tool dependencies, separate from product/npm dependencies.

The installer's maintained source lives in this repository at
`scripts/release/install.js`. Publication bundles it with `runtime.js` and the
selected public distribution settings into `release-dist/<version>/install.js`.
Keep the generated file and actual configuration ignored; do not add a second
installer source at the repository root or maintain a separate publishing repository.

Publisher requirements: Node 22+, npm, Git, the Go version required by `go.mod`,
GoReleaser 2.16+ and `tar`. `npm ci` installs pinned `yaml` and Tencent's official
`cos-nodejs-sdk-v5` plus their dependencies. Only the publishing machine/CI needs the SDK.
The downloaded installer uses Node built-ins and system `tar` (Windows: `tar.exe`),
with no npm registry dependency. Windows users need a tar implementation that reads
both tar.gz and zip (the Windows bsdtar implementation does).

## Configuration stays local

Run from the repository root:

```sh
npm ci --prefix scripts/release
cp scripts/release/config.example.json scripts/release/config.local.json
cp scripts/release/.env.example scripts/release/.env.local
```

Edit **only** the local copies. `config.local.json` needs the actual bucket (including
APPID suffix), region, HTTPS CDN origin and disjoint test/main object prefixes.
The URL path must match the bucket's CDN origin mapping. Credentials are rejected
as configuration keys; fill `COS_SECRET_ID`, `COS_SECRET_KEY`, and optionally the
STS `COS_SESSION_TOKEN` in `.env.local` or inject them through your process environment.
Node's `--env-file` loads the file when explicitly requested. The scripts never read
an account CSV, discover credentials from other projects, or log signed SDK errors.

The checked-in examples use `cdn.example.com` and placeholder credentials. Actual
uploads reject example configuration. Real local configuration must remain ignored and must never be committed.

These paths are ignored: `scripts/release/*.local.*`, `.env*` (except `.env.example`),
PEM/key files, `node_modules/`, and `/release-dist/`. Keep other generated/private
files in those locations. Check before staging; never force-add ignored files:

```sh
git check-ignore scripts/release/config.local.json scripts/release/.env.local release-dist/check
# Both examples must remain visible to git:
git check-ignore scripts/release/config.example.json scripts/release/.env.example
# The second command should print nothing and return status 1.
```

## Branches and versions

| Source branch | Version | Example prefix |
|---|---|---|
| `test` | `X.Y.Z-next.N` | `static/octo-loop-test` |
| `main` | `X.Y.Z` | `static/octo-loop` |

`--ref` accepts only `test` or `main`. It resolves `origin/<branch>` first, then a
local branch with that exact name. Fetch first to update remote-tracking refs.
If the branch is missing, the command fails; it does not create a branch or reinterpret
`dev/*`, feature branches, or tags as test/main. The CLI repository may need its test
branch established before test releases are possible.

Git cannot have both `test` and `test/*` branches in the same ref namespace.
If legacy branches occupy `test/*`, an initial local trial can use an isolated
clone with a local `test` branch at the reviewed integration commit. Its manifest
records `refs/heads/test` and the exact commit; this does not create a remote
test branch. Resolve the remote naming conflict before connecting a CI workflow
that requires `origin/test`.

The build runs in a temporary detached checkout of the resolved **committed SHA**.
Uncommitted work and local configuration are never copied, and the current worktree
is not reset or switched. For code under development, commit/merge it to the intended
source branch before expecting it in a release. The tooling can be invoked from a
separate implementation worktree while building the selected source branch.

Versions follow the CLI's `next.N` convention, without a leading v in directories,
packages or manifests. Input may contain v. N must be unique/increasing for that base
version; it is explicitly chosen, not silently allocated. Commit/build time remain
separate metadata. Only these prerelease formats are supported (not arbitrary dev/rc).
GoReleaser gets an explicit snapshot version so binary and archive versions agree.
Snapshot mode disables remote publication; the script runs source tests before invoking it.

## Build and validate (no cloud access)

Examples below are version placeholders, not instructions to claim an already published
version. Select the correct version for the component before running:

```sh
git fetch origin
node scripts/release/build.js --ref test --version 1.2.0-next.1
node scripts/release/verify.js --dist release-dist/1.2.0-next.1 --smoke
node scripts/release/publish-cos.js --dist release-dist/1.2.0-next.1 --config scripts/release/config.example.json
```

For a stable release, use `--ref main --version X.Y.Z`. Both commands use the same
rules. Existing output directories are refused; don't rebuild into an immutable
release directory. Six archives, `checksums.txt`, and `release.json` are generated.
GoReleaser archive names/formats are preserved: CLI uses tar.gz on all platforms;
daemon uses zip on Windows and tar.gz otherwise. Go/platform names are normalized
from Node's `win32`/`x64` to `windows`/`amd64` in the installer.

`--smoke` installs only the current host's archive into a disposable prefix, runs its
version command, and removes that prefix. It does not modify your normal installation.
This validates real host execution, not all six operating-system/CPU combinations.
Other platform smoke tests must be performed on matching hosts before claiming runtime support.

`build.js` always runs `go test ./...`, plus the existing CLI npm packaging tests when
present. It does not replace the project's full release quality gates (lint/race/required
CI evidence). Future CI must retain those checks before a production upload.

## Publish explicitly

Preview (no SDK initialization, credential access or HTTP calls):

```sh
node scripts/release/publish-cos.js --dist release-dist/1.2.0-next.1
```

After reviewing the target and completing installation checks, upload:

```sh
node --env-file=scripts/release/.env.local scripts/release/publish-cos.js --dist release-dist/1.2.0-next.1 --execute
```

Environment derives from the manifest's verified source branch; there is no free-form
`--env` or object-key option. At execution the source commit must still belong to the
expected repository branch. All artifact hashes are checked before contacting COS.

The publisher verifies **conditional object creation** using a unique temporary
`<environment-prefix>/cli/.publish-probe-<uuid>` object. It writes the probe,
attempts a second write with different bytes and `x-cos-forbid-overwrite: true`,
requires `409 FileAlreadyExists`, verifies the original bytes remain, and removes
its probe before acquiring the release lock. Permission errors, unexpected
conflicts, changed bytes, or cleanup failures stop publication. This requires no
ListBuckets or GetBucketVersioning permission. A read-only bucket-status 403 does
not imply that object uploads are denied.

COS ignores forbid-overwrite for version-enabled/suspended buckets, so those
buckets fail the behavioral check; the script never changes bucket settings.
See [Tencent COS PUT Object](https://cloud.tencent.com/document/product/436/7749).
Required permissions are GetObject and PutObject for the release prefix and
installer entry, plus DeleteObject for `.publish-lock` and `.publish-probe-*`
under the component directory. CDN configuration/refresh permissions are separate.
Do not change bucket versioning while a publisher is running.

Sequence:

1. Acquire `<environment-prefix>/cli/.publish-lock` with conditional creation.
2. Refuse accidental backwards promotion unless `--allow-rollback` was explicitly supplied.
3. Upload immutable version objects. Existing identical bytes are reused; different contents fail.
4. Read back COS objects and download **every immutable object through HTTPS CDN** to verify hashes.
5. Update the installer entry and verify the CDN serves its exact bytes.
6. Recheck the existing pointer, write this component's latest.json **last**, and verify it through CDN.
7. Remove only this invocation's own lock in normal success/failure cleanup.

A crash or ambiguous network failure may leave a lock or temporary publish probe.
Confirm there is no active publisher, then remove only the abandoned object with
an authorized operator. There is no automatic
lock-stealing timeout. All publishers must follow the same lock convention; external
console edits can bypass it and must not run concurrently.

Installer and latest use `Cache-Control: no-store`; version objects use immutable long
caching. Check CDN rules, including forced caching and cached 404s. The script does not
modify/refresh CDN configuration. If it detects stale bytes, refresh the exact URL and
retry. A failure **after** writing latest is reported as such; it is not presented as an
unpublished release. Test CDN cache behavior before the first real deployment.

## Layout and installation

For the example prefixes:

```text
static/octo-loop-test/
  install.js                          # unified CLI + daemon installer
  installation.json                   # tested version pair
  daemon/npm/releases/<version>/...   # independent daemon COS packages
  cli/install.js
  cli/latest.json
  cli/releases/<version>/...          # CLI only
static/octo-loop/                      # same layout for main/stable releases
```

Each remote version directory contains the original native archives, checksums.txt,
release.json and the generated installer snapshot. On first explicit publication, the
local release directory also saves install.js and installer.local.json (origin/channel/hash
only, no credentials). Preserve both with the original archives: retries and rollbacks
reuse that installer even after release-tool code changes. A changed origin or incomplete
snapshot fails instead of replacing an immutable version. URLs below intentionally use a
placeholder domain. Replace it with the configured CDN origin only in local commands:

```sh
# Download success must precede execution; the script also supports curl | node.
curl -fsSL https://cdn.example.com/static/octo-loop-test/cli/install.js -o /tmp/octo-install.js && node /tmp/octo-install.js
node /tmp/octo-install.js --version 1.2.0-next.1 --prefix /tmp/octo-install-check
```

Installer precedence: `--version`, then `OCTO_CLI_VERSION`, then the component's latest.json.
It accepts only the version type for its own branch and refuses redirects, unexpected
components/platforms, unsafe filenames, oversized downloads, and checksum mismatches.
Native archives are read with tar; only the exact regular binary is streamed out,
not arbitrary archive paths. Versions are checked by executing the candidate before activation: daemon uses `--version`, CLI uses `version` and its JSON result.

Default prefix: `~/.local` on macOS/Linux; `%LOCALAPPDATA%/Octo` on Windows.
Commands are installed under `bin/`; saved versions and installation receipts are under
`lib/octo-cli/`. Add the bin directory to PATH. Shell startup files are not edited.
The installer refuses overwriting unmanaged commands or symlinks. Existing global npm
or Homebrew commands may take precedence on PATH; inspect command resolution and migrate
explicitly. It does not uninstall those packages or alter npm registry configuration.
Windows may refuse replacing a running executable: stop it, then retry. Restart any running
daemon explicitly after installation and configure the separately installed CLI path.
This adds no daemon self-update support; keep the existing GitHub updater disabled for
COS-managed deployments until that path is deliberately adapted.

Rollback of a device: run the same environment's installer with an older explicit
version, then restart/verify the process where applicable. User credentials, profiles and
workspaces are not touched. Rollback of new installations: republish the previously built
release directory with `--execute --allow-rollback`. Do not rebuild old content under the
same version. Keep historical artifacts available in COS.

## Future CI integration

CI runs the release-tool tests without cloud credentials or publishing steps.
Add a separate manual COS workflow/job later that
calls these same scripts after all required checks. CLI npm/GitHub releases stay independent.
GitHub uses `workflow_dispatch`; GitLab must explicitly allow the chosen UI/API sources
(`web` and the selected `api`/`trigger` path), not just web. Require test/main branch refs,
an explicit default-off publishing switch, protected credentials, and matching checkout SHA.
This local builder resolves the named branch at invocation time; CI must additionally
verify that it equals the pipeline's expected commit before building, to avoid branch movement.
Use per-component/per-environment concurrency groups in addition to the COS lock.
Inject credentials into the publishing job only; never place them in artifacts.

## Self-contained npm packages for unified installation

The existing npm registry release remains unchanged. COS can additionally publish
six self-contained packages using the same CLI package name and native version:

```sh
node scripts/release/npm-artifacts.js --dist release-dist/X.Y.Z-next.N
node scripts/release/publish-npm.js --dist release-dist/X.Y.Z-next.N/npm
node --env-file=scripts/release/.env.local scripts/release/publish-npm.js --dist release-dist/X.Y.Z-next.N/npm --execute
```

The publisher accepts `test` (`X.Y.Z-next.N`) and `main` (`X.Y.Z`), selecting
`<environment-prefix>/cli/npm/releases/<version>/` from the verified source branch. It does not change the unified
installation pointer or publish anything to npm. Each `.tgz` contains package
metadata, a Node launcher, and one native binary with no external dependencies;
`private: true` prevents accidental npm registry publication. An empty-cache offline
npm install is tested. Package launchers forward SIGINT/SIGTERM on POSIX and wait
for native shutdown. Launcher fixes require a new component version; updating the
root installer does not change existing package contents. Do not regenerate uploaded
files under an existing version.

The daemon repository owns `scripts/release/install.js`,
`scripts/release/publish.js`, and `docs/cos-publishing.md`, including
the root installer, tested version-pair manifest, test prefix/state isolation, and
the end-to-end release runbook. Promote the pair there after both component npm
releases pass CDN verification. Both environments follow the same flow; the daemon installer owns the installation
policy (isolated test commands/state versus production npm global packages).

`scripts/release/tests/` contains automated tests for both release environments;
it is not a separate test-environment workflow. Run them with
`npm test --prefix scripts/release`.
