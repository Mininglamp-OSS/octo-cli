#!/usr/bin/env node
"use strict";

// Resolve the prebuilt Go binary from the platform sub-package selected by
// npm via optionalDependencies, then forward argv and stdio to it.

const os = require("os");
const { spawnSync } = require("child_process");

const PLATFORM_PACKAGES = {
  "darwin-arm64": "@mininglamp-oss/octo-cli-darwin-arm64",
  "darwin-x64": "@mininglamp-oss/octo-cli-darwin-x64",
  "linux-arm64": "@mininglamp-oss/octo-cli-linux-arm64",
  "linux-x64": "@mininglamp-oss/octo-cli-linux-x64",
  "win32-arm64": "@mininglamp-oss/octo-cli-win32-arm64",
  "win32-x64": "@mininglamp-oss/octo-cli-win32-x64",
};

function resolveBinary() {
  const key = `${process.platform}-${process.arch}`;
  const pkg = PLATFORM_PACKAGES[key];
  if (!pkg) {
    console.error(`[octo-cli] no prebuilt binary for ${key}.`);
    console.error(
      "[octo-cli] Prebuilt binaries cover darwin/linux/win32 on x64/arm64. " +
        "Build from source instead: https://github.com/Mininglamp-OSS/octo-cli",
    );
    process.exit(1);
  }

  const binName = process.platform === "win32" ? "octo-cli.exe" : "octo-cli";
  try {
    return require.resolve(`${pkg}/bin/${binName}`);
  } catch (_err) {
    console.error(`[octo-cli] platform package ${pkg} is not installed.`);
    console.error(
      "[octo-cli] npm skips optionalDependencies when installed with " +
        "--no-optional / --omit=optional, and some package managers need a " +
        "lockfile refresh after a platform change.\n" +
        "[octo-cli] Try reinstalling: npm install -g @mininglamp-oss/octo-cli",
    );
    process.exit(1);
  }
}

const res = spawnSync(resolveBinary(), process.argv.slice(2), { stdio: "inherit" });

if (res.error) {
  console.error(`[octo-cli] ${res.error.message}`);
  process.exit(1);
}

// Re-raise terminating signals so the shell observes the conventional
// 128+signum exit code; for default-ignored signals (SIGPIPE, ...) the
// explicit exit below sets the code instead.
if (res.signal) {
  process.kill(process.pid, res.signal);
  const signum = (os.constants && os.constants.signals && os.constants.signals[res.signal]) || 0;
  process.exit(128 + signum);
}

process.exit(res.status === null ? 1 : res.status);
