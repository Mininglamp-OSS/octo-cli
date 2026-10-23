# Package-manager-aware CLI upgrade

Date: 2026-10-23

Status: confirmed design.

## Goal and scope

Provide a non-interactive `octo-cli upgrade` command for global installations
managed by npm, Bun or pnpm. Detect the manager that owns the invoked
installation, query its configured npm registry and update that installation
through the same manager. Verify the installed command before reporting success.

Included:

- npm, Bun and pnpm global installations on the existing distribution matrix:
  macOS, Linux and Windows, each on x64 and arm64.
- Updating to the stable release selected by the configured registry's `latest`
  dist-tag; equal or newer installed versions are successful no-ops.
- Native package-manager registry, mirror, scoped-registry and authentication
  configuration, including custom global installation locations.
- Installation-ownership checks, existing structured output and an offline dry run.

Outside scope:

- Updating project-local dependencies, temporary `npx`/`bunx`/`pnpm dlx` installs,
  linked source checkouts, standalone binaries or other installation channels.
  These receive installation-method guidance rather than being migrated.
- Version/channel selection, background updates, automatic privilege elevation,
  automatic package-manager installation, or a rollback system around a manager.

## Existing boundaries

- `npm/package.json` publishes `@mininglamp-oss/octo-cli` with a Node launcher
  at `bin/octo-cli.js` and requires Node 18 or later.
- `npm/bin/octo-cli.js` resolves the matching platform dependency and launches
  its Go executable. Installation already uses registry tarballs, not a
  postinstall download from GitHub.
- `npm/scripts/prepare-packages.js` pins platform optional dependencies to the
  main package's exact release version. These dependencies contain the executable.
- `cmd/root.go` owns registration, command preflight, credential-free commands,
  global output flags and the Loop task-mode restriction.
- Global layouts and command entries vary by manager and OS. pnpm's normal store
  links are valid package-manager installation structure, not source-checkout
  links. pnpm 11+ isolates global install groups: updating can replace both the
  package directory and its versioned store target while preserving the command
  entry and manager-owned package identity.

## Reference decisions

| Reference | Observed strategy | Decision for octo-cli |
| --- | --- | --- |
| [Codex launcher](https://github.com/openai/codex/blob/main/codex-cli/bin/codex.js) and [update actions](https://github.com/openai/codex/blob/main/codex-rs/tui/src/update_action.rs) | Detect a manager in the launcher, propagate context to the native program and dispatch npm/Bun/pnpm install commands | Reuse manager-aware dispatch; verify ownership rather than treating an unknown installation as npm |
| [Claude Code setup](https://code.claude.com/docs/en/setup) | Distinguish native and package-managed installs; document channel-specific update commands and npm installation of the latest package | Preserve the installation channel and install a resolved release rather than relying on a saved version range |
| [Oh My Pi updater](https://github.com/can1357/oh-my-pi/blob/main/packages/coding-agent/src/cli/update-cli.ts) and [registry resolver](https://github.com/can1357/oh-my-pi/blob/main/packages/coding-agent/src/cli/npm-registry.ts) | Probe npm/Bun global locations, use manager install commands and verify the result; resolve registry metadata with its own configuration parser | Reuse location checks and result verification; delegate registry configuration and metadata requests to the owning manager |

These observations concern the public source/documentation consulted during
design. They do not assert identical support across these projects: Oh My Pi's
examined updater has no pnpm dispatch, and Claude Code's documentation is not
proof of its internal bun/pnpm detection. No reference updater was executed.

## Command contract

```sh
octo-cli upgrade
octo-cli upgrade --dry-run
```

The command takes no positional arguments and adds no command-specific flags.
It uses existing output-format and jq preflight before mutation. It requires no
Octo credential, does not resolve a profile and does not modify Octo configuration.
Identity and Space selectors do not choose the installation to update.

Loop task mode continues to reject `upgrade` through the existing root policy.
Help performs no ownership probes or update operations.

## Installation ownership and manager selection

Start from the actual running Node launcher: retain its logical package entry,
canonical main-package directory and resolved platform executable. Do not select
the update target by looking for the first `octo-cli` on PATH.

Probe available supported managers using local global-directory and installed-
package queries. Examples of existing interfaces are:

| Manager | Local ownership information | Registry version lookup | Exact release installation |
| --- | --- | --- | --- |
| npm | `npm prefix -g`, `npm root -g`, global installed-package metadata | `npm view -g @mininglamp-oss/octo-cli@latest version --json` | `npm install -g @mininglamp-oss/octo-cli@<version>` |
| Bun | `bun pm bin -g`, `bun pm ls -g`, installed package/lock metadata | `bun info -g @mininglamp-oss/octo-cli@latest version --json` | `bun install -g @mininglamp-oss/octo-cli@<version>` |
| pnpm | `pnpm root -g`, `pnpm bin -g`, `pnpm list -g` package metadata | `pnpm view @mininglamp-oss/octo-cli@latest version --json` in the verified project-neutral context | `pnpm add -g @mininglamp-oss/octo-cli@<version>` |

The interfaces above guide implementation; do not infer global roots from the
presence of `.bun` or `.pnpm` in a path, and do not assume Bun's list output is
JSON. Use manager-native configuration for custom global directories. Layout
metadata and launcher paths may narrow the candidates, but the parent shell's
`npm_config_user_agent` or `npm_execpath` alone cannot prove ownership.

pnpm 10 delegates `view` to npm; pnpm 11+ implements it natively. The command
must still use pnpm's effective registry configuration in both cases. Native
pnpm 12 does not accept `view --global`, so do not assume that a global flag is
valid for every metadata query. Global ownership checks remain global; metadata
lookup uses the same effective registry/authentication context as installation.

An eligible manager must establish all of the following:

1. Its global package metadata and logical package entry identify
   `@mininglamp-oss/octo-cli`; that entry resolves to the invoked main package.
2. Its global command entry targets this package's launcher. Unix symlinks and
   Windows command shims must be interpreted according to the manager/OS layout.
3. The platform executable resolves from this main package's exact pinned
   platform dependency, not from an unrelated installation.
4. The installed main-package version and the running executable's build version
   agree after normalizing an optional leading `v` on the latter.
5. The package is in the manager's verified global package set, not a linked
   source checkout or a temporary/project-local installation. Refuse source-link
   or non-registry descriptors when native metadata exposes them. Normal pnpm
   store links and symlinked parent directories, including Node version-manager
   prefixes, remain valid.

Select the unique matching manager. Zero matches, conflicting ownership, a
missing owning manager, a mismatched global directory/entry, or a directly
invoked Go executable produces an explicit local error before registry access
or installation. Another manager's valid global copy elsewhere does not qualify.
Never install into a second directory to make an ownership mismatch succeed.

Keep the chosen manager executable, global installation context, package name
and global command entry fixed for the operation. The manager may replace the
package's logical group directory and canonical store path, particularly with
pnpm's isolated global installs. Pin the verified npm prefix when invoking npm.
For Bun and pnpm, retain the verified native global-location configuration; do
not translate it into an npm prefix. Recheck ownership before installation if
preparation crosses a subprocess handoff. The implementation is not required to
serialize unrelated package-manager operations.

This is operational ownership, not historical installer provenance or an OS-user
security boundary. Installed metadata cannot prove which tool originally wrote
an otherwise indistinguishable directory. Unknown or conflicting evidence is not
silently adopted.

The verified npm 10 global listing retains the installed version and path but
does not retain the original install specifier or write a global package lock.
Eligibility therefore follows the current global package and command ownership;
it does not reconstruct installation history from logs or cache contents.

## Registry configuration and version selection

Let the selected manager load its own supported configuration. Query and install
using the same manager, environment and global configuration context:

- npm uses native npm environment, user/global `.npmrc`, scoped registries and
  registry-scoped credentials.
- Bun uses its native npm-registry configuration, including supported `.npmrc`
  settings, global `.bunfig.toml`, scoped registries and environment settings.
  Bun selects `$XDG_CONFIG_HOME/.bunfig.toml` when that directory is configured,
  otherwise `$HOME/.bunfig.toml`.
- pnpm uses the registry/authentication and global configuration supported by
  the installed pnpm release.

Do not implement a `.npmrc`/TOML parser or an independent registry HTTP client.
Do not modify package-manager configuration or replace a failed mirror with
another source. No GitHub/API availability check is required for this update path.
Do not expose credentials in commands, output or errors.

Run helpers in a project-neutral working context so the directory from which
`octo-cli upgrade` was invoked cannot select a project registry or redirect the
update. Preserve the meaning of explicit relative configuration paths when
establishing that context. Global directory settings and registry lookup must
agree with the context used for installation. This does not promise identical
configuration precedence across managers: each manager remains authoritative.

Resolve `latest` through the selected manager and require a stable SemVer
release. Compare SemVer precedence against the installed package version. An
equal or older target returns a successful no-op without an install. A newer
target is installed by exact version so a dist-tag change between lookup and
installation cannot change the chosen release. An invalid or prerelease `latest`
is a configuration/metadata error; development builds do not meet the installed
release contract.

Ensure the matching platform optional dependency is included, even when native
configuration normally omits optional packages. Keep the manager's integrity and
registry-authentication handling intact. Execute arguments without interpolating
package names or paths into shell command text.

For Bun, install the exact matching platform package explicitly alongside the
main package. This keeps the declared platform dependency resolvable when
optional packages are omitted. npm and pnpm use their native optional-dependency
overrides.

## Process lifecycle and result verification

1. **Prepare:** existing command/argument, task-mode and output preflight;
   ownership selection; registry lookup and version comparison. No install may
   begin unless preparation has succeeded.
2. **Install:** the Node launcher coordinates the chosen manager only after the
   old Go child has exited. Load everything the wrapper needs to finish before
   its own package files are replaced. Do not terminate other CLI processes;
   an external file lock remains an operational install failure.
3. **Verify:** locate the named package afresh through the same manager's global
   metadata and the fixed global command entry. Reload the main manifest and
   platform dependency; run the resulting executable and require all their
   versions to equal the exact target. A new isolated group directory or store
   target is valid; neither the old package directory nor a new unrelated
   installation on PATH is the verification target.
4. **Report:** emit update success only after verification. A successful manager
   exit by itself is insufficient, as is finding the target version in another
   installation on PATH.

The Go command remains authoritative for validation, task-mode policy and the
public output contract. Launcher/Go preparation and result handoff is private,
local and separate from stdout. Without valid launcher ownership context, a
direct invocation of the Go executable cannot initiate a global installation.

The launcher retains a temporary copy of the previous Go executable for final
reporting, loaded outside the package being replaced. The initial Go process
exits before installation; the retained reporter starts only afterward and
applies the original format/jq controls to the private result. Verification
separately executes the newly installed platform binary. The temporary copy is
removed after reporting and is not an installation rollback mechanism.
Irrecoverable launcher failures emit the same safe stderr error envelope
directly, without echoing native exceptions.

The launcher lifecycle must preserve existing format/jq behavior and one final
public result, including an error envelope if the new executable cannot start.
A successful jq expression that yields no rows is not an upgrade failure. Manager
stdout must not be mixed with public stdout.

### Success data

Use the existing success envelope with this data object:

```json
{
  "manager": "pnpm",
  "previous_version": "0.18.0",
  "target_version": "0.19.0",
  "current_version": "0.19.0",
  "updated": true
}
```

`manager` is `npm`, `bun` or `pnpm`. Versions are normalized without a leading
`v`. A no-op returns `updated: false`, preserves the installed `current_version`
and reports the resolved `target_version`, including when it is older.

### Dry run

`--dry-run` performs command preflight and local ownership queries only. It makes
no registry requests, installation or configuration changes. Return a structured
plan with `dry_run: true`, `package: "@mininglamp-oss/octo-cli"`, the selected
`manager`, normalized `current_version`, `target: "latest"` and the verified
logical `package_path` and `command_path`. It does not claim to know the latest
version or whether an update is available. Unsupported installations are refused
rather than described as eligible.

### Failures

Use the existing stderr error envelope. Ownership/unsupported-installation
errors and invalid registry version metadata are validation/configuration
failures (exit 2). A missing required manager, manager execution, registry access,
installation or post-install verification failure is operational (exit 1).
Preserve existing argument, output-preflight and Loop task-mode error contracts.

Distinguish an unavailable owning manager from an installation-method mismatch
when evidence permits. Give the relevant manager/install-method guidance without
guessing an owner from ambiguous evidence. Diagnostics must not leak registry
authentication data or create a second public envelope.

Manager failure stops the operation: do not switch managers, elevate privileges,
force installation or replace the package with a standalone binary. Installation
may already have modified files before an install/verification failure; do not
claim rollback or that the old installation is intact.

## Acceptance criteria

1. **Each manager:** install an older release globally into an isolated npm, Bun
   and pnpm context. Invoke its command, update to the resolved target, and run
   that same logical command again to observe the target version. The reported
   manager is correct and no second manager/global directory is modified.
2. **Multiple installations:** with different copies installed through all three
   managers and a conflicting PATH/user-agent, update only the invoked copy.
   Ownership is based on the actual launcher, global metadata and command entry.
3. **Layouts:** custom global directories, normal pnpm store links, symlinked
   parent prefixes and OS-specific command shims work. After pnpm changes the
   versioned store target, verification uses the new logical entry. Linked source
   checkouts and mismatched package/binary versions are refused before networking.
4. **Mirrors:** test each manager with its supported user/global mirror,
   environment and scoped-registry configuration. Metadata and package downloads
   use that context. A conflicting project configuration does not affect the
   operation. Missing/unsynchronized platform packages surface an error without
   fallback. Required platform dependencies remain installed when optional
   packages are normally omitted.
5. **Version transitions:** equal and newer installed versions return accurate
   no-op data and run no install. A changing dist-tag cannot change the exact
   target selected for installation. Invalid/prerelease `latest` is refused.
6. **Local refusal:** unknown/ambiguous ownership, wrong global context,
   project-local or temporary installation, source link and standalone invocation
   cause no registry request or install. An unavailable owning manager reports
   the documented operational failure and does not select another manager.
7. **Dry run and preflight:** dry run returns an ownership-checked local plan
   without registry access or mutation. Invalid arguments/output options and
   Loop task mode cannot initiate an install. Ordinary upgrade needs no Octo
   credential or profile resolution.
8. **Failures and output:** inaccessible mirror, authentication failure,
   permissions/file locks, manager failure, stale installed manifest/platform
   package, and an incorrect/unlaunchable new executable all produce nonzero
   structured failure, never update success. Preserve format/jq behavior; keep
   manager stdout and authentication secrets out of the public result.
9. **Platforms:** exercise the actual installed-command update on macOS, Linux
   and Windows. In particular, the upgrade's old Go process is no longer holding
   its binary open when Windows installation starts. Cover layout/resolution
   across all six shipped OS/architecture combinations; distinguish actual
   execution evidence from cross-build or resolution-only checks.
10. **Integration:** existing launcher behavior for other commands is preserved.
    Upgrade help, main and npm package documentation, the shared agent guide and
    changelog describe manager selection, supported global installations and
    native mirror configuration.

Implementation verification uses temporary global locations and a local registry
fixture with real package installation and command execution, not the operator's
active installations. Permanent tests target ownership, version transitions and
consumer-visible failures. No production Octo service requests are needed.

Implementation boundaries:

- `cmd/upgrade.go` and root preflight own the credential-free command and private
  Go request/report stages.
- `npm/bin/octo-cli.js` coordinates process exit, the retained reporter and
  cleanup; `npm/lib/` owns package-manager metadata, version selection and
  verification. Helpers execute from the private handoff directory, preserving
  explicitly relative configuration-file paths.
  Post-install dependency resolution uses the freshly resolved physical main
  manifest, matching Node's launcher behavior through pnpm virtual-store links.
  On Windows, ownership canonicalization expands native short-path and
  extended-length aliases before comparing package and command paths.
- `npm/scripts/upgrade-e2e.test.js` builds real host executables, prepares release
  packages and exercises isolated global installs against an authenticated local
  registry. `.github/workflows/upgrade-acceptance.yml` runs it on macOS, Linux and
  Windows with pnpm 10 and 12. Foreign-platform archives in this fixture test
  packaging only; their binaries are not execution evidence.

## Design-time verification

Read-only probes on the development host established:

- npm 10.9.8 reports the installed CLI's global metadata as `0.18.0`; the invoked
  CLI's `version --format json` reports the same version.
- Bun 1.4.0 exposes its global bin and dependency root through `bun pm`; its list
  output remains textual with `--json` and must not be assumed to be JSON.
- `bun info -g @mininglamp-oss/octo-cli@latest version --json` works from a
  directory without a project manifest and returned `"0.18.0"`. On this host,
  the corresponding query without global mode failed because no manifest was
  present; global-context lookup is therefore part of acceptance.
- pnpm 10.32.0 exposes its global root/list metadata and its package version
  query returned `"0.18.0"`.
- In an isolated home/configuration context, pnpm 10.32.0 and 12.15.0 both
  returned the fixture release `"0.18.123"` from a local registry configured
  through `pnpm config set registry ... --global`. This exercised metadata
  routing only. pnpm 12.15.0 rejected `view --global`, accepted `view` in the
  neutral context, and exposed its `global/v11` root through local queries.

These are interface/metadata checks, not implementation acceptance. No upgrade
or active-installation modification was performed.

Configuration references:

- [npm configuration](https://docs.npmjs.com/cli/v10/configuring-npm/npmrc/)
- [Bun npmrc support](https://bun.com/docs/pm/npmrc)
- [Bun global configuration](https://bun.com/docs/runtime/bunfig)
- [pnpm global root](https://pnpm.io/cli/root)
- [pnpm update](https://pnpm.io/10.x/cli/update)
- [pnpm 11 global-layout and native-command changes](https://pnpm.io/blog/releases/11.0)

## Open issues

None.
