"use strict";

// Unit tests for the pure functions in install.js. The download / extract
// paths are covered by the existing manual e2e against real GitHub releases;
// these tests guard the small input-output functions where regressions are
// most likely (parsing, validation, host allowlist, PATH-hint scenarios).
//
// Run with: `npm test` (from the `npm/` directory) or `node --test scripts/`.

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");

const { EventEmitter } = require("node:events");

const {
  assertSafeHost,
  isValidReleaseSemver,
  assetName,
  parseChecksumEntry,
  maybeHintPath,
  ALLOWED_HOSTS,
  armDeadline,
} = require("./install.js");

// ---------- assertSafeHost --------------------------------------------------

test("assertSafeHost: allows github.com over https", () => {
  assert.doesNotThrow(() => assertSafeHost(new URL("https://github.com/x"), "req"));
});

test("assertSafeHost: allows release-assets.githubusercontent.com", () => {
  assert.doesNotThrow(() =>
    assertSafeHost(new URL("https://release-assets.githubusercontent.com/x"), "redirect"),
  );
});

test("assertSafeHost: allows objects.githubusercontent.com (legacy)", () => {
  assert.doesNotThrow(() =>
    assertSafeHost(new URL("https://objects.githubusercontent.com/x"), "redirect"),
  );
});

test("assertSafeHost: rejects http (non-tls) even on allowlisted host", () => {
  assert.throws(
    () => assertSafeHost(new URL("http://github.com/x"), "req"),
    /refusing non-https/i,
  );
});

test("assertSafeHost: rejects an unlisted host", () => {
  assert.throws(
    () => assertSafeHost(new URL("https://attacker.example/x"), "redirect"),
    /not in the redirect allowlist/i,
  );
});

test("assertSafeHost: rejected errors are marked nonRetryable", () => {
  try {
    assertSafeHost(new URL("https://attacker.example/x"), "redirect");
    assert.fail("should have thrown");
  } catch (e) {
    assert.equal(e.nonRetryable, true);
  }
});

test("ALLOWED_HOSTS covers the four CDN hops in use", () => {
  // Lock in the set so adding/removing a host shows up in tests, not silently.
  assert.deepEqual(
    [...ALLOWED_HOSTS].sort(),
    [
      "codeload.github.com",
      "github.com",
      "objects.githubusercontent.com",
      "release-assets.githubusercontent.com",
    ],
  );
});

// ---------- isValidReleaseSemver -------------------------------------------

test("isValidReleaseSemver: accepts stable releases", () => {
  for (const v of ["1.0.0", "0.6.0", "10.20.30"]) assert.equal(isValidReleaseSemver(v), true, v);
});

test("isValidReleaseSemver: accepts prereleases", () => {
  for (const v of ["1.0.0-rc.1", "0.6.0-beta.2", "1.2.3-alpha"]) {
    assert.equal(isValidReleaseSemver(v), true, v);
  }
});

test("isValidReleaseSemver: rejects +build metadata (CI never produces it)", () => {
  assert.equal(isValidReleaseSemver("1.2.3+build.4"), false);
});

test("isValidReleaseSemver: rejects malformed strings", () => {
  for (const v of [
    "v1.2.3", // leading v
    "1.2", // missing patch
    "1.2.3.4", // four segments
    "1.2.3-", // empty prerelease
    "../etc/passwd", // path-traversal attempt
    "", // empty
    " 1.2.3", // leading space
  ]) {
    assert.equal(isValidReleaseSemver(v), false, v);
  }
});

test("isValidReleaseSemver: 0.0.0 is regex-valid (placeholder rejection is main()'s job)", () => {
  // The "package version is a placeholder" guard lives in main(), not in
  // this validator. Document the seam: 0.0.0 is structurally a valid semver
  // string; the placeholder check is a separate, deliberate check.
  assert.equal(isValidReleaseSemver("0.0.0"), true);
});

test("isValidReleaseSemver: rejects non-strings", () => {
  for (const v of [null, undefined, 1.2, {}, []]) {
    assert.equal(isValidReleaseSemver(v), false, String(v));
  }
});

// ---------- assetName -------------------------------------------------------

test("assetName: matches .goreleaser.yaml name_template (tar.gz on unix)", () => {
  assert.equal(assetName("0.6.0", "linux", "amd64", false), "octo-cli_0.6.0_linux_amd64.tar.gz");
  assert.equal(assetName("0.6.0", "darwin", "arm64", false), "octo-cli_0.6.0_darwin_arm64.tar.gz");
});

test("assetName: uses .zip on Windows", () => {
  assert.equal(assetName("0.6.0", "windows", "amd64", true), "octo-cli_0.6.0_windows_amd64.zip");
});

test("assetName: locks in the os/arch axis", () => {
  // Iterate the full grid so a goreleaser change that drops a platform shows up.
  const grid = [
    ["linux", "amd64", false, "tar.gz"],
    ["linux", "arm64", false, "tar.gz"],
    ["darwin", "amd64", false, "tar.gz"],
    ["darwin", "arm64", false, "tar.gz"],
    ["windows", "amd64", true, "zip"],
    ["windows", "arm64", true, "zip"],
  ];
  for (const [os, arch, win, ext] of grid) {
    assert.equal(assetName("1.2.3", os, arch, win), `octo-cli_1.2.3_${os}_${arch}.${ext}`);
  }
});

// ---------- parseChecksumEntry ---------------------------------------------

const VALID_DIGEST = "a".repeat(64); // 64 lowercase hex chars
const ASSET = "octo-cli_0.6.0_linux_amd64.tar.gz";

test("parseChecksumEntry: returns the digest for a well-formed entry", () => {
  const sums = `${VALID_DIGEST}  ${ASSET}\n${"b".repeat(64)}  some-other-file\n`;
  assert.equal(parseChecksumEntry(sums, ASSET), VALID_DIGEST);
});

test("parseChecksumEntry: ignores leading/trailing whitespace lines", () => {
  const sums = `\n   \n${VALID_DIGEST}  ${ASSET}\n\n`;
  assert.equal(parseChecksumEntry(sums, ASSET), VALID_DIGEST);
});

test("parseChecksumEntry: throws when the asset isn't listed", () => {
  const sums = `${VALID_DIGEST}  other-file\n`;
  assert.throws(() => parseChecksumEntry(sums, ASSET), /no checksum entry/i);
});

test("parseChecksumEntry: rejects a non-hex digest", () => {
  const sums = `not-a-digest  ${ASSET}\n`;
  assert.throws(() => parseChecksumEntry(sums, ASSET), /malformed checksum entry/i);
});

test("parseChecksumEntry: rejects uppercase hex (locking lowercase invariant)", () => {
  const sums = `${"A".repeat(64)}  ${ASSET}\n`;
  assert.throws(() => parseChecksumEntry(sums, ASSET), /malformed checksum entry/i);
});

test("parseChecksumEntry: rejects a digest of the wrong length", () => {
  const sums = `${"a".repeat(63)}  ${ASSET}\n`;
  assert.throws(() => parseChecksumEntry(sums, ASSET), /malformed checksum entry/i);
});

// ---------- armDeadline (wall-clock guard on an in-flight request) ---------

// Minimal stand-in for http.ClientRequest: an EventEmitter with destroy().
function fakeReq() {
  const e = new EventEmitter();
  e.destroyed = false;
  e.destroyError = null;
  e.destroy = (err) => {
    e.destroyed = true;
    e.destroyError = err;
  };
  return e;
}

test("armDeadline: destroys req with a nonRetryable error when deadline elapses", async () => {
  const req = fakeReq();
  armDeadline(req, Date.now() + 30, "https://example/x");
  await new Promise((r) => setTimeout(r, 60));
  assert.equal(req.destroyed, true);
  assert.ok(req.destroyError, "expected a destroy error");
  assert.equal(req.destroyError.nonRetryable, true);
  assert.match(req.destroyError.message, /wall-clock deadline/i);
  assert.match(req.destroyError.message, /example/);
});

test("armDeadline: fires immediately when deadline is in the past", async () => {
  const req = fakeReq();
  armDeadline(req, Date.now() - 5000, "https://example/x");
  await new Promise((r) => setTimeout(r, 10));
  assert.equal(req.destroyed, true);
});

test("armDeadline: cleared by 'close' so a fast request leaves no pending timer", async () => {
  const req = fakeReq();
  armDeadline(req, Date.now() + 30, "https://example/x");
  // Simulate the request completing well before the deadline.
  req.emit("close");
  // Wait past the original deadline and confirm destroy was never called.
  await new Promise((r) => setTimeout(r, 60));
  assert.equal(req.destroyed, false);
  assert.equal(req.destroyError, null);
});

// ---------- maybeHintPath ---------------------------------------------------

function withEnv(env, fn) {
  const keys = ["npm_config_global", "npm_config_location", "npm_config_prefix", "PREFIX", "PATH", "SHELL"];
  const saved = {};
  for (const k of keys) saved[k] = process.env[k];
  for (const k of keys) delete process.env[k];
  Object.assign(process.env, env);
  try {
    return fn();
  } finally {
    for (const k of keys) {
      if (saved[k] === undefined) delete process.env[k];
      else process.env[k] = saved[k];
    }
  }
}

function captureStderr(fn) {
  const original = process.stderr.write.bind(process.stderr);
  const buf = [];
  process.stderr.write = (chunk) => {
    buf.push(typeof chunk === "string" ? chunk : chunk.toString("utf8"));
    return true;
  };
  try {
    fn();
  } finally {
    process.stderr.write = original;
  }
  return buf.join("");
}

// __dirname here is .../npm/scripts; install.js computes ourBinDir as
// path.resolve(__dirname, "..", "bin") which is .../npm/bin.
const OUR_BIN_DIR = path.resolve(__dirname, "..", "bin");

test("maybeHintPath: silent when not a global install", () => {
  const out = withEnv({ PATH: "/usr/bin", SHELL: "/bin/zsh" }, () => captureStderr(maybeHintPath));
  assert.equal(out, "");
});

test("maybeHintPath: silent when npm's bin is already on PATH", () => {
  const out = withEnv(
    { npm_config_global: "true", npm_config_prefix: "/usr/local", PATH: "/usr/local/bin:/usr/bin", SHELL: "/bin/zsh" },
    () => captureStderr(maybeHintPath),
  );
  assert.equal(out, "");
});

test("maybeHintPath: silent when our own bin is already on PATH", () => {
  const out = withEnv(
    { npm_config_global: "true", PATH: `${OUR_BIN_DIR}:/usr/bin`, SHELL: "/bin/zsh" },
    () => captureStderr(maybeHintPath),
  );
  assert.equal(out, "");
});

test("maybeHintPath: hints OUR bin (not npm's) when npm's bin isn't on PATH", () => {
  const out = withEnv(
    { npm_config_global: "true", npm_config_prefix: "/usr/local", PATH: "/usr/bin", SHELL: "/bin/zsh" },
    () => captureStderr(maybeHintPath),
  );
  assert.match(out, new RegExp(OUR_BIN_DIR.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
  assert.doesNotMatch(out, /\/usr\/local\/bin/);
});

test("maybeHintPath: hints OUR bin when no prefix is known at all", () => {
  // The old fallback would `path.resolve(__dirname, "..","..","..","..","..")`
  // to guess <prefix>; we no longer do that. Lock in that we never print a
  // path inferred by counting `..`.
  const out = withEnv(
    { npm_config_global: "true", PATH: "/usr/bin", SHELL: "/bin/zsh" },
    () => captureStderr(maybeHintPath),
  );
  assert.match(out, new RegExp(OUR_BIN_DIR.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
});

test("maybeHintPath: zsh hint uses ~/.zshrc + export PATH", () => {
  const out = withEnv(
    { npm_config_global: "true", PATH: "/usr/bin", SHELL: "/bin/zsh" },
    () => captureStderr(maybeHintPath),
  );
  assert.match(out, /~\/\.zshrc/);
  assert.match(out, /export PATH=/);
});

test("maybeHintPath: fish hint uses fish_add_path", () => {
  const out = withEnv(
    { npm_config_global: "true", PATH: "/usr/bin", SHELL: "/usr/local/bin/fish" },
    () => captureStderr(maybeHintPath),
  );
  assert.match(out, /fish_add_path/);
  assert.doesNotMatch(out, /export PATH=/);
});

test("maybeHintPath: bash hint uses ~/.bashrc", () => {
  const out = withEnv(
    { npm_config_global: "true", PATH: "/usr/bin", SHELL: "/bin/bash" },
    () => captureStderr(maybeHintPath),
  );
  assert.match(out, /~\/\.bashrc/);
});

test("maybeHintPath: unknown shell falls back to generic profile wording", () => {
  const out = withEnv(
    { npm_config_global: "true", PATH: "/usr/bin", SHELL: "/bin/eshell" },
    () => captureStderr(maybeHintPath),
  );
  assert.match(out, /your shell profile/);
});

test("maybeHintPath: respects npm_config_location=global", () => {
  // npm sets this on global installs as an alternative to npm_config_global.
  const out = withEnv(
    { npm_config_location: "global", PATH: "/usr/bin", SHELL: "/bin/zsh" },
    () => captureStderr(maybeHintPath),
  );
  assert.notEqual(out, "");
});
