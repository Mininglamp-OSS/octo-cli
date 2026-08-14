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
