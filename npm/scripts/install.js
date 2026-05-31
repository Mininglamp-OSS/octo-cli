#!/usr/bin/env node
"use strict";

// postinstall: download the prebuilt octo-cli binary that matches this package
// version and the host platform from the GitHub Release, verify its sha256
// against the `checksums.txt` published on the same release, and extract it
// into ../bin. Mirrors goreleaser's archive naming
// (octo-cli_<version>_<os>_<arch>.tar.gz, .zip on Windows; os/arch lowercase).
//
// Trust model: the checksum proves the archive matches whatever
// `checksums.txt` that release currently serves — i.e. it catches transport
// corruption, partial CDN poisoning, and accidental clobber. It does NOT
// prove provenance: both files share one trust root (the GitHub Release), so
// an actor who can write to the release replaces both consistently. Treat
// this as an integrity check; provenance is the GitHub Release itself plus
// the npm publish pipeline.
//
// Failure behavior: every error path calls `fail()` which logs to stderr and
// `process.exit(1)`. This is deliberate for a CLI installed with `-g`: a
// silent partial install would leave the user with an `octo-cli` shim that
// can't find its binary, surfacing as a confusing ENOENT only when the user
// tries to run a command later. Loud failure at install time is the right
// signal. There is intentionally no `OCTO_CLI_SKIP_DOWNLOAD` opt-out — if a
// transitive/air-gapped consumer ever needs one, add it then; today it would
// be unused complexity. The retry loop in `download()` already handles
// transient network failures, so a single ECONNRESET does not trip `fail()`.

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
const BIN_NAME = isWin ? "octo-cli.exe" : "octo-cli";

// Redirects are followed only to these hosts. github.com is the first hop;
// release assets currently 302 to release-assets.githubusercontent.com (a
// signed-URL CDN); objects.githubusercontent.com is the older asset CDN and
// is kept for backwards/forwards compatibility; codeload covers source
// archives. Anything else is treated as hostile.
const ALLOWED_HOSTS = new Set([
  "github.com",
  "release-assets.githubusercontent.com",
  "objects.githubusercontent.com",
  "codeload.github.com",
]);

const MAX_REDIRECTS = 5;
const REQUEST_TIMEOUT_MS = 30_000;
const MAX_RETRIES = 3;
const MAX_DOWNLOAD_BYTES = 200 * 1024 * 1024; // 200 MiB, far above any sane archive
const TOTAL_DOWNLOAD_DEADLINE_MS = 5 * 60_000; // wall-clock cap per asset

function fail(msg) {
  console.error(`\n[octo-cli] install failed: ${msg}`);
  console.error(`[octo-cli] Grab a binary manually from https://github.com/${REPO}/releases\n`);
  process.exit(1);
}

// Mark errors that must not be retried (allowlist / parse failures, deadline
// exceeded). retryable status codes are still retried; everything explicitly
// marked here is not.
function nonRetryable(err) {
  err.nonRetryable = true;
  return err;
}

function assertSafeHost(u, label) {
  if (u.protocol !== "https:") {
    throw nonRetryable(new Error(`${label}: refusing non-https URL (${u.protocol}//${u.hostname})`));
  }
  if (!ALLOWED_HOSTS.has(u.hostname)) {
    throw nonRetryable(new Error(`${label}: host '${u.hostname}' is not in the redirect allowlist`));
  }
}

// True if `s` matches the strict semver subset we accept as a release tag:
// MAJOR.MINOR.PATCH with optional prerelease. No `+build` metadata — CI never
// produces it, so accepting it on the install side would be a silent
// divergence from .github/workflows/npm-publish.yml.
function isValidReleaseSemver(s) {
  return typeof s === "string" && /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(s);
}

// The archive filename goreleaser produces for (version, os, arch). The shape
// is fixed by .goreleaser.yaml's `name_template` — keep these in sync.
function assetName(version, os, arch, win) {
  const ext = win ? "zip" : "tar.gz";
  return `octo-cli_${version}_${os}_${arch}.${ext}`;
}

// Look up `asset` in a `checksums.txt`-style payload ("<sha256>  <filename>"
// per line). Returns the expected 64-char lowercase hex digest. Throws if
// the entry is missing or the digest doesn't match that shape — so the
// caller doesn't have to special-case a "garbage line that happens to match
// by filename" scenario.
function parseChecksumEntry(sums, asset) {
  const entry = sums
    .split("\n")
    .map((l) => l.trim().split(/\s+/))
    .find((p) => p[1] === asset);
  if (!entry) throw new Error(`no checksum entry for ${asset}`);
  const digest = entry[0];
  if (!/^[0-9a-f]{64}$/.test(digest)) {
    throw new Error(`malformed checksum entry for ${asset}: '${digest}'`);
  }
  return digest;
}

// Single HTTPS GET. Resolves to either a buffered body or a redirect target,
// never both. Two timeouts apply:
//   - REQUEST_TIMEOUT_MS  — socket-idle timeout (resets on each received
//     byte). Catches half-open connections / dead sockets.
//   - deadline (epoch ms) — wall-clock deadline armed via setTimeout, calls
//     req.destroy() when reached. Catches a peer that trickles bytes just
//     under the idle threshold and would otherwise stall the install
//     indefinitely. The caller (download) passes a deadline that bounds the
//     entire retry chain, not just this single request.
// Both are non-retryable so the caller fails fast instead of looping.
function getOnce(url, deadline) {
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
          let nextUrl;
          try {
            nextUrl = new URL(res.headers.location, url);
          } catch (e) {
            reject(nonRetryable(new Error(`malformed redirect location for ${url}: ${e.message}`)));
            return;
          }
          resolve({ kind: "redirect", next: nextUrl });
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
            req.destroy(nonRetryable(new Error(`response exceeded ${MAX_DOWNLOAD_BYTES} bytes for ${url}`)));
            return;
          }
          chunks.push(c);
        });
        res.on("end", () => resolve({ kind: "body", body: Buffer.concat(chunks) }));
        res.on("error", reject);
      },
    );
    req.on("timeout", () => {
      // Idle timeout (no bytes for REQUEST_TIMEOUT_MS) IS retryable — a flaky
      // network can recover on the next attempt. Only the wall-clock deadline
      // below is non-retryable.
      req.destroy(new Error(`request idle for ${REQUEST_TIMEOUT_MS}ms: ${url}`));
    });
    req.on("error", reject);

    // Wall-clock deadline. Fires *during* the response body so a slow-trickle
    // peer is aborted mid-stream, not just between retries.
    if (deadline) armDeadline(req, deadline, url);
  });
}

// Arm a wall-clock deadline on an in-flight request. When `deadline` is
// reached, `req.destroy(err)` raises an error on the request — caught by
// the caller's `.on('error', reject)` — and the promise rejects with our
// deadline error. The timer is cleared on `'close'` so a successful request
// doesn't leave a hanging timer that prevents Node from exiting. Exported
// for tests; production callers use `getOnce(url, deadline)`.
function armDeadline(req, deadline, url) {
  const remaining = Math.max(deadline - Date.now(), 0);
  const timer = setTimeout(() => {
    req.destroy(
      nonRetryable(
        new Error(`download exceeded wall-clock deadline (${TOTAL_DOWNLOAD_DEADLINE_MS / 1000}s): ${url}`),
      ),
    );
  }, remaining);
  req.on("close", () => clearTimeout(timer));
  return timer;
}

async function downloadOnce(url, deadline) {
  let current = url;
  for (let hop = 0; hop <= MAX_REDIRECTS; hop++) {
    let parsed;
    try {
      parsed = new URL(current);
    } catch (e) {
      throw nonRetryable(new Error(`malformed URL at hop ${hop}: ${e.message}`));
    }
    assertSafeHost(parsed, hop === 0 ? "request" : `redirect #${hop}`);
    const r = await getOnce(current, deadline);
    if (r.kind === "body") return r.body;
    current = r.next.toString();
  }
  throw nonRetryable(new Error(`too many redirects (>${MAX_REDIRECTS})`));
}

// Retry the whole download on transient errors (timeout, socket reset, 5xx).
// 404 is treated as a non-retryable signal that the version doesn't exist on
// the release yet — fast-fail with a clear error rather than spin. Errors
// explicitly flagged `nonRetryable` (allowlist / parse / deadline) are not
// retried either. The deadline is passed all the way down to getOnce so it
// can abort an in-flight request mid-stream, not just guard the gap between
// retries.
async function download(url) {
  const deadline = Date.now() + TOTAL_DOWNLOAD_DEADLINE_MS;
  for (let attempt = 0; ; attempt++) {
    if (Date.now() >= deadline) {
      throw nonRetryable(new Error(`download exceeded ${TOTAL_DOWNLOAD_DEADLINE_MS / 1000}s wall-clock deadline: ${url}`));
    }
    try {
      return await downloadOnce(url, deadline);
    } catch (e) {
      const status = e && e.httpStatus;
      const retryable =
        !(e && e.nonRetryable) &&
        attempt < MAX_RETRIES - 1 &&
        (status === undefined || (status >= 500 && status < 600));
      if (!retryable) throw e;
      const delay = 500 * Math.pow(2, attempt); // 500ms, 1s, 2s
      console.error(`[octo-cli] ${e.message} — retrying in ${delay}ms`);
      await new Promise((r) => setTimeout(r, delay));
    }
  }
}

// On a global install, warn (without modifying anything) if `octo-cli` would
// not be found on PATH. Best-effort: any failure here must not break the
// install.
//
// Strategy: if npm's own bin (<prefix>/bin on unix, <prefix> on Windows) is
// already on PATH, do nothing — npm's bin shim works, the user is fine. If
// it isn't (or we can't tell because npm_config_prefix isn't set), point at
// the directory the postinstall just wrote the Go binary into. That path is
// `path.join(__dirname, "..", "bin")` — derived from __dirname, so it's
// always exact and doesn't depend on guessing npm's directory layout
// (counting `..` to reach <prefix>). Cost: adding our bin to PATH only
// helps octo-cli, not other global npm packages — but if npm's bin isn't on
// PATH the user already has that problem repo-wide.
function maybeHintPath() {
  try {
    const isGlobal =
      process.env.npm_config_global === "true" || process.env.npm_config_location === "global";
    if (!isGlobal) return;

    const PATH = (process.env.PATH || "").split(path.delimiter).filter(Boolean);
    const onPath = (dir) => PATH.some((p) => path.resolve(p) === path.resolve(dir));

    // Common case: npm's bin is on PATH, the shim it installed for `octo-cli`
    // will be found, no hint needed.
    const npmPrefix = process.env.npm_config_prefix || process.env.PREFIX;
    if (npmPrefix) {
      const npmBin = isWin ? npmPrefix : path.join(npmPrefix, "bin");
      if (onPath(npmBin)) return;
    }

    // Either we don't know where npm put its bin, or it isn't on PATH. Tell
    // the user to add our own bin (where the Go binary lives) instead. That
    // path is exact; the npm-bin path would be a guess.
    const ourBinDir = path.resolve(__dirname, "..", "bin");
    if (onPath(ourBinDir)) return;

    const lines = [
      "",
      `[octo-cli] Installed ${ourBinDir}/${BIN_NAME}, but that directory is not on PATH.`,
      "[octo-cli] Add it to use the `octo-cli` command:",
    ];
    if (isWin) {
      lines.push(`    setx PATH "%PATH%;${ourBinDir}"`);
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
        lines.push(`    fish_add_path ${ourBinDir}`);
      } else {
        lines.push(`[octo-cli] Add this to ${rc}, then reopen the terminal:`);
        lines.push(`    export PATH="${ourBinDir}:$PATH"`);
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
  if (!isValidReleaseSemver(VERSION)) {
    fail(`package version '${VERSION}' is not a valid release semver`);
  }

  const asset = assetName(VERSION, OS, ARCH, isWin);
  const base = `https://github.com/${REPO}/releases/download/v${VERSION}`;
  const binDir = path.join(__dirname, "..", "bin");

  console.log(`[octo-cli] downloading ${asset} ...`);
  let archive, sums;
  try {
    archive = await download(`${base}/${asset}`);
    sums = (await download(`${base}/checksums.txt`)).toString("utf8");
  } catch (e) {
    fail(e.message);
  }

  let expected;
  try {
    expected = parseChecksumEntry(sums, asset);
  } catch (e) {
    fail(e.message);
  }
  const got = crypto.createHash("sha256").update(archive).digest("hex");
  if (got !== expected) fail(`checksum mismatch for ${asset} (want ${expected}, got ${got})`);

  // Extract just the binary into bin/. bsdtar (macOS/Windows 10 1803+) and
  // GNU tar (Linux) both handle the formats we ship (.tar.gz via -xzf, .zip
  // via -xf). On older Windows / minimal containers tar may be absent — give
  // a targeted message instead of a bare ENOENT.
  fs.mkdirSync(binDir, { recursive: true });
  const tmp = path.join(binDir, asset);
  const extractedPath = path.join(binDir, BIN_NAME);
  fs.writeFileSync(tmp, archive);
  try {
    const args = isWin ? ["-xf", tmp, "-C", binDir, BIN_NAME] : ["-xzf", tmp, "-C", binDir, BIN_NAME];
    execFileSync("tar", args, { stdio: "inherit" });

    // Refuse non-regular files (symlink/dir/...). Named-member extraction
    // prevents zip-slip from other members of the archive, but the named
    // member itself can be a symlink; a later chmodSync / spawnSync would
    // follow it, letting a malicious archive chmod files outside bin/ or
    // execute an arbitrary local binary as `octo-cli`. goreleaser never emits
    // symlink members, so this is defense-in-depth behind the release trust
    // root, not a remotely-reachable bug. Use lstat (not stat) so we see the
    // symlink itself rather than its target.
    const st = fs.lstatSync(extractedPath);
    if (!st.isFile()) {
      try {
        fs.unlinkSync(extractedPath);
      } catch {
        /* leave it; the fail() below is what matters */
      }
      fail(`extracted '${BIN_NAME}' is not a regular file (mode ${(st.mode & 0o170000).toString(8)})`);
    }
    fs.chmodSync(extractedPath, 0o755);
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
module.exports = {
  maybeHintPath,
  assertSafeHost,
  isValidReleaseSemver,
  assetName,
  parseChecksumEntry,
  ALLOWED_HOSTS,
  armDeadline,
};
