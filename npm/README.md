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
verifies its checksum; the `octo-cli` command then execs that binary directly.

Supported platforms: macOS, Linux, Windows on `x64` / `arm64`.

For other install methods (Homebrew, raw binary, `go install`) and full usage,
see the [main README](https://github.com/Mininglamp-OSS/octo-cli#readme).

## License

[Apache-2.0](https://github.com/Mininglamp-OSS/octo-cli/blob/main/LICENSE)
