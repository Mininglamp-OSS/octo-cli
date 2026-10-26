# @mininglamp-oss/octo-cli

npm distribution of [`octo-cli`](https://github.com/Mininglamp-OSS/octo-cli) — the
command-line interface for the Octo ecosystem, built for AI Agent Bots.

```bash
npm install -g @mininglamp-oss/octo-cli
octo-cli --help
```

Agent-facing Skill documentation is embedded in the same signed binary. A
runtime can load the official Agent Mail guide without downloading a separate
Skill package:

```bash
octo-cli skills octo-mail
```

This package is a thin Node wrapper around the prebuilt Go binary. The matching
platform binary is shipped inside an npm optional dependency such as
`@mininglamp-oss/octo-cli-darwin-arm64`; the `octo-cli` command resolves that
sub-package and execs the binary directly. Install does not download anything
from GitHub.

Supported platforms: macOS, Linux, Windows on `x64` / `arm64`.

For other install methods (Homebrew, raw binary, `go install`) and full usage,
see the [main README](https://github.com/Mininglamp-OSS/octo-cli#readme).

## Upgrading

Global installations made with npm, Bun or pnpm support:

```bash
octo-cli upgrade --dry-run
octo-cli upgrade
```

The command verifies the invoked installation's owning manager and global
command entry rather than selecting another copy on PATH. It uses that manager
to resolve the configured registry's stable `latest` release and install the
exact version. Equal or newer installed releases are successful no-ops.
Success requires verification of the resulting package and platform executable.
Global Bun ownership uses its native global manifest and command entry, so dry
runs and updates also work when global lockfiles are absent.

During installation, npm/pnpm explicitly enable optional dependencies, and Bun
installs the matching platform package alongside the main package. The platform
executable is required even when optional dependencies are disabled in your
manager configuration.

Use the manager's native mirror and authentication configuration: npm's
user/global `.npmrc` or environment; Bun's supported `.npmrc` and global
`.bunfig.toml`; pnpm's own global configuration and registry/authentication
settings. Scoped registries are supported. Configuration in the current project
does not select the update source.

Dry run is offline: it reports the owning manager, installed version and global
paths without querying `latest` or changing packages. Both modes use the normal
JSON envelope and support `--format` and `--jq`, without Octo credentials.
Loop task mode rejects the command. Project-local, temporary, source-linked and
standalone installations must be updated through their original method.

If the manager or verification fails, upgrade returns a structured error and
does not report success. The manager may already have changed installation
files; there is no automatic rollback or switch to another manager.

## Trust model

The main package and platform sub-packages are published with npm
`--provenance`, producing Sigstore attestations that link each tarball back to
the GitHub Actions workflow that published it. You can verify them with:

```bash
npm audit signatures
```

GitHub Release archives are still published for non-npm channels and are
covered by `checksums.txt`, but npm installs use npm registry tarballs and npm's
own integrity checks.

## License

[Apache-2.0](https://github.com/Mininglamp-OSS/octo-cli/blob/main/LICENSE)
