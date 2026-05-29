#!/usr/bin/env node
"use strict";

// postinstall: download the prebuilt octo-cli binary that matches this package
// version and the host platform from the GitHub Release, verify its checksum,
// and extract it into ../bin. Mirrors goreleaser's archive naming
// (octo-cli_<version>_<os>_<arch>.tar.gz, .zip on Windows; os/arch lowercase).

const fs = require("fs");
const path = require("path");
const https = require("https");
const crypto = require("crypto");
const { execFileSync } = require("child_process");

const REPO = "Mininglamp-OSS/octo-cli";
const VERSION = require("../package.json").version;

const OS = { darwin: "darwin", linux: "linux", win32: "windows" }[process.platform];
const ARCH = { x64: "amd64", arm64: "arm64" }[process.arch];
const isWin = process.platform === "win32";

function fail(msg) {
  console.error(`\n[octo-cli] install failed: ${msg}`);
  console.error(`[octo-cli] Grab a binary manually from https://github.com/${REPO}/releases\n`);
  process.exit(1);
}

// GET with redirect following (GitHub release assets 302 to a CDN).
function download(url, redirects = 0) {
  return new Promise((resolve, reject) => {
    if (redirects > 5) {
      reject(new Error("too many redirects"));
      return;
    }
    https
      .get(url, { headers: { "User-Agent": "octo-cli-npm-installer" } }, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          res.resume();
          resolve(download(res.headers.location, redirects + 1));
          return;
        }
        if (res.statusCode !== 200) {
          res.resume();
          reject(new Error(`HTTP ${res.statusCode} for ${url}`));
          return;
        }
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => resolve(Buffer.concat(chunks)));
      })
      .on("error", reject);
  });
}

// On a global install, warn (without modifying anything) if the directory npm
// links the `octo-cli` command into is not on PATH, so the command would not be
// found. Best-effort: any failure here must not break the install.
function maybeHintPath() {
  try {
    const isGlobal =
      process.env.npm_config_global === "true" || process.env.npm_config_location === "global";
    if (!isGlobal) return;

    // npm links bins into <prefix>/bin on unix, and into <prefix> itself on Windows.
    let prefix = process.env.npm_config_prefix || process.env.PREFIX;
    if (!prefix) {
      // Fallback: walk up from <prefix>/lib/node_modules/@scope/pkg/scripts (unix)
      // or <prefix>/node_modules/@scope/pkg/scripts (Windows).
      prefix = path.resolve(__dirname, "..", "..", "..", "..", isWin ? ".." : "../..");
    }
    const binDir = isWin ? prefix : path.join(prefix, "bin");

    const onPath = (process.env.PATH || "")
      .split(path.delimiter)
      .some((p) => p && path.resolve(p) === path.resolve(binDir));
    if (onPath) return;

    const lines = [
      "",
      `[octo-cli] Installed, but ${binDir} is not on your PATH —`,
      "[octo-cli] the `octo-cli` command will not be found until you add it.",
    ];
    if (isWin) {
      lines.push("[octo-cli] Add it for your user (then reopen the terminal):");
      lines.push(`    setx PATH "%PATH%;${binDir}"`);
    } else {
      const shell = path.basename(process.env.SHELL || "");
      const rc =
        shell === "zsh"
          ? "~/.zshrc"
          : shell === "fish"
            ? "~/.config/fish/config.fish"
            : shell === "bash"
              ? "~/.bashrc"
              : "your shell profile";
      if (shell === "fish") {
        lines.push(`[octo-cli] Add this to ${rc}, then reopen the terminal:`);
        lines.push(`    fish_add_path ${binDir}`);
      } else {
        lines.push(`[octo-cli] Add this to ${rc}, then reopen the terminal:`);
        lines.push(`    export PATH="${binDir}:$PATH"`);
      }
    }
    lines.push("");
    console.error(lines.join("\n"));
  } catch {
    /* hint is best-effort; never fail the install over it */
  }
}

async function main() {
  if (!OS || !ARCH) fail(`unsupported platform ${process.platform}/${process.arch}`);
  if (!VERSION || VERSION === "0.0.0") {
    fail("package version is a placeholder (0.0.0); this package must be published with a real release version");
  }

  const ext = isWin ? "zip" : "tar.gz";
  const asset = `octo-cli_${VERSION}_${OS}_${ARCH}.${ext}`;
  const base = `https://github.com/${REPO}/releases/download/v${VERSION}`;
  const binName = isWin ? "octo-cli.exe" : "octo-cli";
  const binDir = path.join(__dirname, "..", "bin");

  console.log(`[octo-cli] downloading ${asset} ...`);
  let archive, sums;
  try {
    archive = await download(`${base}/${asset}`);
    sums = (await download(`${base}/checksums.txt`)).toString("utf8");
  } catch (e) {
    fail(e.message);
  }

  // Verify sha256 against checksums.txt ("<sha256>  <filename>" per line).
  const entry = sums
    .split("\n")
    .map((l) => l.trim().split(/\s+/))
    .find((p) => p[1] === asset);
  if (!entry) fail(`no checksum entry for ${asset}`);
  const got = crypto.createHash("sha256").update(archive).digest("hex");
  if (got !== entry[0]) fail(`checksum mismatch for ${asset} (want ${entry[0]}, got ${got})`);

  // Extract just the binary into bin/. bsdtar (macOS/Windows) and GNU tar
  // (Linux) both handle the formats we ship (.tar.gz via -xzf, .zip via -xf).
  fs.mkdirSync(binDir, { recursive: true });
  const tmp = path.join(binDir, asset);
  fs.writeFileSync(tmp, archive);
  try {
    const args = isWin ? ["-xf", tmp, "-C", binDir, binName] : ["-xzf", tmp, "-C", binDir, binName];
    execFileSync("tar", args, { stdio: "inherit" });
    fs.chmodSync(path.join(binDir, binName), 0o755);
  } catch (e) {
    fail(`extract failed: ${e.message}`);
  } finally {
    try {
      fs.unlinkSync(tmp);
    } catch {
      /* ignore */
    }
  }

  console.log(`[octo-cli] installed octo-cli ${VERSION} (${OS}/${ARCH})`);
  maybeHintPath();
}

// Run as a postinstall script; also importable (e.g. to unit-test maybeHintPath).
if (require.main === module) {
  main();
}
module.exports = { maybeHintPath };
