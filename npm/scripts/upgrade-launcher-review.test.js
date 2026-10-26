"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawn } = require("node:child_process");

const SHIM = path.resolve(__dirname, "../bin/octo-cli.js");
const PLATFORM_PACKAGE = `@mininglamp-oss/octo-cli-${process.platform}-${process.arch}`;
const BINARY_NAME = process.platform === "win32" ? "octo-cli.exe" : "octo-cli";

function runProcess(command, args, env) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { env, stdio: ["ignore", "pipe", "pipe"], timeout: 30_000 });
    let stdout = "";
    let stderr = "";
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk) => { stdout += chunk; });
    child.stderr.on("data", (chunk) => { stderr += chunk; });
    child.once("error", reject);
    child.once("close", (status, signal) => resolve({ status, signal, stdout, stderr }));
  });
}

function createLauncherFixture(t) {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-launcher-review-"));
  const launcher = path.join(tmp, "package", "bin", "octo-cli.js");
  fs.mkdirSync(path.dirname(launcher), { recursive: true });
  fs.copyFileSync(SHIM, launcher);
  fs.chmodSync(launcher, 0o755);

  const preload = path.join(tmp, "preload.js");
  fs.writeFileSync(preload, `"use strict";
const Module = require("node:module");
if (process.env.OCTO_CLI_TEST_PLATFORM) Object.defineProperty(process, "platform", { configurable: true, value: process.env.OCTO_CLI_TEST_PLATFORM });
const resolveFilename = Module._resolveFilename;
Module._resolveFilename = function(request, ...args) {
  if (request === process.env.OCTO_CLI_TEST_MISSING_REQUEST) {
    const error = new Error("NATIVE_RESOLUTION_PRIVATE_DETAIL");
    error.code = "MODULE_NOT_FOUND";
    throw error;
  }
  return resolveFilename.call(this, request, ...args);
};
`);
  t.after(() => fs.rmSync(tmp, { recursive: true, force: true }));
  return { launcher, preload };
}

function runLauncher(fixture, args, extraEnv = {}) {
  return runProcess(process.execPath, [fixture.launcher, ...args], {
    ...process.env,
    NODE_OPTIONS: `--require ${JSON.stringify(fixture.preload)}`,
    OCTO_CLI_TEST_MISSING_REQUEST: `${PLATFORM_PACKAGE}/bin/${BINARY_NAME}`,
    ...extraEnv,
  });
}

function structuredError(result, code) {
  assert.equal(result.status, 2, result.stderr);
  assert.equal(result.stdout, "");
  const envelope = JSON.parse(result.stderr.trim());
  assert.equal(envelope.ok, false);
  assert.equal(envelope.error.type, "validation");
  assert.equal(envelope.error.code, code);
  return envelope.error;
}


test("ordinary root flag values named upgrade retain platform dependency diagnostics", async (t) => {
  const fixture = createLauncherFixture(t);
  for (const args of [
    ["--profile", "upgrade", "config", "show"],
    ["-q", "upgrade", "config", "show"],
    ["config", "show", "--profile", "upgrade"],
  ]) {
    await t.test(args.join(" "), async () => {
      const result = await runLauncher(fixture, args);
      assert.equal(result.status, 1);
      assert.equal(result.stdout, "");
      assert.match(result.stderr, /^\[octo-cli\] platform package .* is not installed\.\n/);
      assert.match(result.stderr, /Try reinstalling: npm install -g @mininglamp-oss\/octo-cli/);
      assert.doesNotMatch(result.stderr, /UPGRADE_(?:HANDOFF_FAILED|PLATFORM_DEPENDENCY_MISSING)/);
      assert.doesNotMatch(result.stderr, /NATIVE_RESOLUTION_PRIVATE_DETAIL/);
    });
  }
});


test("a top-level upgrade after root flag values reports a missing platform dependency", async (t) => {
  const fixture = createLauncherFixture(t);
  const result = await runLauncher(fixture, ["--profile", "agent", "upgrade"]);
  const error = structuredError(result, "UPGRADE_PLATFORM_DEPENDENCY_MISSING");
  assert.match(error.hint, /reinstall|optional dependenc/i);
  assert.doesNotMatch(result.stderr, /NATIVE_RESOLUTION_PRIVATE_DETAIL/);
});


test("a disabled root help flag preserves upgrade platform diagnostics", async (t) => {
  const fixture = createLauncherFixture(t);
  const result = await runLauncher(fixture, ["--help=false", "upgrade"]);
  structuredError(result, "UPGRADE_PLATFORM_DEPENDENCY_MISSING");
});

test("a top-level upgrade on an unsupported platform reports an actionable validation error", async (t) => {
  const fixture = createLauncherFixture(t);
  const result = await runLauncher(fixture, ["-q", "expression", "upgrade"], {
    OCTO_CLI_TEST_PLATFORM: "unsupported-os",
    OCTO_CLI_TEST_MISSING_REQUEST: "",
  });
  const error = structuredError(result, "UPGRADE_UNSUPPORTED_PLATFORM");
  assert.match(error.hint, /source|reinstall|supported platform/i);
  assert.doesNotMatch(result.stderr, /unsupported-os.*exception/i);
});
