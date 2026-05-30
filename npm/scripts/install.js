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

// Redirects are followed only to these hosts. github.com is the first hop;
// release assets redirect to objects.githubusercontent.com via S3; codeload
// covers source archives. Anything else is treated as hostile.
const ALLOWED_HOSTS = new Set([
  "github.com",
  "objects.githubusercontent.com",
  "codeload.github.com",
]);

const MAX_REDIRECTS = 5;
const REQUEST_TIMEOUT_MS = 30_000;
const MAX_RETRIES = 3;
const MAX_DOWNLOAD_BYTES = 200 * 1024 * 1024; // 200 MiB, far above any sane archive

function fail(msg) {
  console.error(`\n[octo-cli] install failed: ${msg}`);
  console.error(`[octo-cli] Grab a binary manually from https://github.com/${REPO}/releases\n`);
  process.exit(1);
}

function assertSafeHost(u, label) {
  if (u.protocol !== "https:") {
    throw new Error(`${label}: refusing non-https URL (${u.protocol}//${u.hostname})`);
  }
  if (!ALLOWED_HOSTS.has(u.hostname)) {
    throw new Error(`${label}: host '${u.hostname}' is not in the redirect allowlist`);
  }
}

// Single HTTPS GET. Resolves to either a buffered body or a redirect target,
// never both. Times out on socket idle so a half-open connection cannot hang
// the install indefinitely.
function getOnce(url) {
  return new Promise((resolve, reject) => {
    const req = https.get(
      url,
      {
        headers: { "User-Agent": "octo-cli-npm-installer" },
        timeout: REQUEST_TIMEOUT_MS,
      },
      (res) => {
        const status = res.statusCode || 0;
        if (status >= 300 && status < 400 && res.headers.location) {
          res.resume();
          resolve({ kind: "redirect", next: new URL(res.headers.location, url) });
          return;
        }
        if (status !== 200) {
          res.resume();
          const err = new Error(`HTTP ${status} for ${url}`);
          err.httpStatus = status;
          reject(err);
          return;
        }
        const chunks = [];
        let size = 0;
        res.on("data", (c) => {
          size += c.length;
          if (size > MAX_DOWNLOAD_BYTES) {
            req.destroy(new Error(`response exceeded ${MAX_DOWNLOAD_BYTES} bytes for ${url}`));
            return;
          }
          chunks.push(c);
        });
        res.on("end", () => resolve({ kind: "body", body: Buffer.concat(chunks) }));
        res.on("error", reject);
      },
    );
    req.on("timeout", () => {
      req.destroy(new Error(`request timed out after ${REQUEST_TIMEOUT_MS}ms: ${url}`));
    });
    req.on("error", reject);
  });
}

async function downloadOnce(url) {
  let current = url;
  for (let hop = 0; hop <= MAX_REDIRECTS; hop++) {
    assertSafeHost(new URL(current), hop === 0 ? "request" : `redirect #${hop}`);
    const r = await getOnce(current);
    if (r.kind === "body") return r.body;
    current = r.next.toString();
  }
  throw new Error(`too many redirects (>${MAX_REDIRECTS})`);
}

// Retry the whole download on transient errors (timeout, socket reset, 5xx).
// 404 is treated as a non-retryable signal that the version doesn't exist on
// the release yet — fast-fail with a clear error rather than spin.
async function download(url) {
  for (let attempt = 0; ; attempt++) {
    try {
      return await downloadOnce(url);
    } catch (e) {
      const status = e && e.httpStatus;
      const retryable =
        attempt < MAX_RETRIES - 1 &&
        (status === undefined || (status >= 500 && status < 600));
      if (!retryable) throw e;
      const delay = 500 * Math.pow(2, attempt); // 500ms, 1s, 2s
      console.error(`[octo-cli] ${e.message} — retrying in ${delay}ms`);
      await new Promise((r) => setTimeout(r, delay));
    }
  }
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
      // Fallback: __dirname is <prefix>/lib/node_modules/@scope/pkg/scripts (unix,
      // 5 levels under prefix) or <prefix>/node_modules/@scope/pkg/scripts
      // (Windows, 4 levels).
      prefix = isWin
        ? path.resolve(__dirname, "..", "..", "..", "..")
        : path.resolve(__dirname, "..", "..", "..", "..", "..");
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
  // The version is interpolated into both the download URL and a filesystem
  // path; reject anything that isn't strict semver before either use.
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(VERSION)) {
    fail(`package version '${VERSION}' is not a valid semver`);
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
  if (!entry || entry.length < 2) fail(`no checksum entry for ${asset}`);
  const expected = entry[0];
  if (!/^[0-9a-f]{64}$/.test(expected)) {
    fail(`malformed checksum entry for ${asset}: '${expected}'`);
  }
  const got = crypto.createHash("sha256").update(archive).digest("hex");
  if (got !== expected) fail(`checksum mismatch for ${asset} (want ${expected}, got ${got})`);

  // Extract just the binary into bin/. bsdtar (macOS/Windows 10 1803+) and
  // GNU tar (Linux) both handle the formats we ship (.tar.gz via -xzf, .zip
  // via -xf). On older Windows / minimal containers tar may be absent — give
  // a targeted message instead of a bare ENOENT.
  fs.mkdirSync(binDir, { recursive: true });
  const tmp = path.join(binDir, asset);
  fs.writeFileSync(tmp, archive);
  try {
    const args = isWin ? ["-xf", tmp, "-C", binDir, binName] : ["-xzf", tmp, "-C", binDir, binName];
    execFileSync("tar", args, { stdio: "inherit" });
    fs.chmodSync(path.join(binDir, binName), 0o755);
  } catch (e) {
    if (e && e.code === "ENOENT") {
      fail(
        isWin
          ? "`tar.exe` not found on PATH. Windows 10 build 1803+ ships bsdtar; on older systems install Git for Windows or 7-Zip and re-run."
          : "`tar` not found on PATH. Install GNU tar or bsdtar (e.g. apt-get install tar, apk add tar) and re-run.",
      );
    }
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
