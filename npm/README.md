# @mininglamp-oss/octo-cli

npm distribution of [`octo-cli`](https://github.com/Mininglamp-OSS/octo-cli) — the
command-line interface for the Octo ecosystem, built for AI Agent Bots.

```bash
npm install -g @mininglamp-oss/octo-cli
octo-cli --help
```

This package is a thin Node wrapper around the prebuilt Go binary. On install it
downloads the binary matching your platform and this package's version from the
[GitHub Release](https://github.com/Mininglamp-OSS/octo-cli/releases) and
verifies its sha256 against the `checksums.txt` published on the same release;
the `octo-cli` command then execs that binary directly.

Supported platforms: macOS, Linux, Windows on `x64` / `arm64`.

For other install methods (Homebrew, raw binary, `go install`) and full usage,
see the [main README](https://github.com/Mininglamp-OSS/octo-cli#readme).

## Trust model

The sha256 check is an **integrity** check, not a **provenance** check: the
archive and its `checksums.txt` are fetched from the same GitHub Release over
the same channel, so an actor who can write to the release replaces both
consistently. The effective trust root is GitHub Release integrity plus the
npm publish pipeline.

The wrapper itself (the tarball you install from npm) is published with npm
`--provenance`, producing a Sigstore attestation that links the tarball back
to the GitHub Actions workflow that published it. You can verify it with:

```bash
npm audit signatures
```

A future release may sign `checksums.txt` itself (cosign keyless) so the
installer can verify a signature whose key is not co-located with the
artifact.

## License

[Apache-2.0](https://github.com/Mininglamp-OSS/octo-cli/blob/main/LICENSE)
