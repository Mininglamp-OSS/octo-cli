#!/usr/bin/env node
"use strict";

// Resolve the matching prebuilt Go executable and coordinate the private
// post-exit handoff used by global package-manager upgrades.

const fs = require("fs");
const os = require("os");
const path = require("path");
const { spawnSync } = require("child_process");

const PLATFORM_PACKAGES = {
  "darwin-arm64": "@mininglamp-oss/octo-cli-darwin-arm64",
  "darwin-x64": "@mininglamp-oss/octo-cli-darwin-x64",
  "linux-arm64": "@mininglamp-oss/octo-cli-linux-arm64",
  "linux-x64": "@mininglamp-oss/octo-cli-linux-x64",
  "win32-arm64": "@mininglamp-oss/octo-cli-win32-arm64",
  "win32-x64": "@mininglamp-oss/octo-cli-win32-x64",
};

function platformKey(platform, arch) {
  return `${platform}-${arch}`;
}

function platformPackageFor(platform, arch) {
  return PLATFORM_PACKAGES[platformKey(platform, arch)];
}

function binaryNameFor(platform) {
  return platform === "win32" ? "octo-cli.exe" : "octo-cli";
}

function resolveBinary(platform = process.platform, arch = process.arch, resolver = require.resolve) {
  const key = platformKey(platform, arch);
  const pkg = PLATFORM_PACKAGES[key];
  if (!pkg) {
    const err = new Error(`[octo-cli] no prebuilt binary for ${key}.`);
    err.detail =
      "[octo-cli] Prebuilt binaries cover darwin/linux/win32 on x64/arm64. " +
      "Build from source instead: https://github.com/Mininglamp-OSS/octo-cli";
    throw err;
  }

  const binName = binaryNameFor(platform);
  try {
    return resolver(`${pkg}/bin/${binName}`);
  } catch (_err) {
    const err = new Error(`[octo-cli] platform package ${pkg} is not installed.`);
    err.detail =
      "[octo-cli] npm skips optionalDependencies when installed with " +
      "--no-optional / --omit=optional, and some package managers need a " +
      "lockfile refresh after a platform change.\n" +
      "[octo-cli] Try reinstalling: npm install -g @mininglamp-oss/octo-cli";
    throw err;
  }
}

function finishChild(res) {
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
}

function emitUpgradeHandoffFailure() {
  process.stderr.write(`${JSON.stringify({
    ok: false,
    error: {
      type: "api_error",
      code: "UPGRADE_HANDOFF_FAILED",
      message: "The upgrade could not be completed.",
      hint: "Retry the upgrade through the installed package manager.",
    },
  })}\n`);
  process.exit(1);
}


function main() {
  const args = process.argv.slice(2);
  const upgradeInvocation = args.includes("upgrade");
  let bin;
  try {
    bin = resolveBinary();
  } catch (err) {
    if (upgradeInvocation) emitUpgradeHandoffFailure();
    console.error(err.message);
    if (err.detail) console.error(err.detail);
    process.exit(1);
  }

  let handoffDir = null;
  let childEnv = process.env;
  if (upgradeInvocation) {
    try {
      handoffDir = fs.mkdtempSync(path.join(path.resolve(os.tmpdir()), "octo-cli-upgrade-"));
      fs.chmodSync(handoffDir, 0o700);
    } catch (_err) {
      if (handoffDir) {
        try {
          fs.rmSync(handoffDir, { recursive: true, force: true });
        } catch (_cleanupError) {
          // The startup error is the public result.
        }
      }
      emitUpgradeHandoffFailure();
    }
    childEnv = { ...process.env, OCTO_CLI_UPGRADE_HANDOFF: handoffDir };
    delete childEnv.OCTO_CLI_UPGRADE_REPORT;
  }

  let result;
  let handoffFailure = false;
  try {
    result = spawnSync(bin, args, { stdio: "inherit", env: childEnv });
    if (upgradeInvocation && result.error) {
      handoffFailure = true;
    } else {
      const requestPath = handoffDir && path.join(handoffDir, "request.json");
      if (handoffDir && result.status === 0 && fs.existsSync(requestPath)) {
        const request = JSON.parse(fs.readFileSync(requestPath, "utf8"));
        const upgrade = require("../lib/upgrade");
        const reporterPath = path.join(handoffDir, binaryNameFor(process.platform));
        fs.copyFileSync(bin, reporterPath);
        fs.chmodSync(reporterPath, fs.statSync(bin).mode & 0o777);
        upgrade.runUpgrade({
          launcherPath: process.argv[1],
          binaryPath: bin,
          request,
          handoffDir,
          cwd: process.cwd(),
          env: process.env,
        });
        result = spawnSync(reporterPath, args, {
          stdio: "inherit",
          env: {
            ...process.env,
            OCTO_CLI_UPGRADE_HANDOFF: handoffDir,
            OCTO_CLI_UPGRADE_REPORT: "1",
          },
        });
        handoffFailure = Boolean(result.error);
      }
    }
  } catch (_err) {
    if (!upgradeInvocation) throw _err;
    handoffFailure = true;
  } finally {
    if (handoffDir) {
      try {
        fs.rmSync(handoffDir, { recursive: true, force: true });
      } catch (_cleanupError) {
        // Cleanup must not replace the public result.
      }
    }
  }

  if (handoffFailure) emitUpgradeHandoffFailure();
  finishChild(result);
}

module.exports = {
  PLATFORM_PACKAGES,
  binaryNameFor,
  platformKey,
  platformPackageFor,
  resolveBinary,
};

if (require.main === module) {
  main();
}
