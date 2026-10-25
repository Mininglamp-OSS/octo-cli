"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
const managers = require("../lib/package-managers.js");

const PACKAGE_NAME = "@mininglamp-oss/octo-cli";
const VERSION = "1.2.3";

function run(manager, args, options) {
  return spawnSync(manager.file, [...manager.args, ...args], {
    ...options,
    encoding: "utf8",
    windowsHide: true,
  });
}

function npmEnvironment(home, prefix) {
  const env = { ...process.env };
  for (const key of Object.keys(env)) {
    if (/^(?:npm_config_|npm_execpath$)/i.test(key)) delete env[key];
  }
  return {
    ...env,
    HOME: home,
    USERPROFILE: home,
    npm_config_prefix: prefix,
    NPM_CONFIG_PREFIX: prefix,
    npm_config_userconfig: path.join(home, ".npmrc"),
    npm_config_cache: path.join(home, "npm-cache"),
    npm_config_audit: "false",
    npm_config_fund: "false",
    npm_config_update_notifier: "false",
    CI: "1",
  };
}

function fakeNpmManager(directory) {
  const script = path.join(directory, "fake-npm.js");
  fs.writeFileSync(script, `
const command = process.argv[2];
const output = command === "prefix" ? process.env.REVIEW_PREFIX + "\\n"
  : command === "root" ? process.env.REVIEW_ROOT + "\\n"
  : process.env.REVIEW_LISTING;
process.stdout.write(output, () => {
  if (command !== "ls") process.exit(0);
  if (process.env.REVIEW_SIGNAL === "1") process.kill(process.pid, "SIGTERM");
  process.exit(Number(process.env.REVIEW_STATUS || "0"));
});
`);
  return { name: "npm", file: process.execPath, args: [script] };
}
function npmWithLsStatusOne(directory, npm) {
  const script = path.join(directory, "npm-ls-status-wrapper.js");
  fs.writeFileSync(script, `
const { spawnSync } = require("node:child_process");
const args = process.argv.slice(2);
const result = spawnSync(
  process.env.REVIEW_REAL_NPM_FILE,
  [...JSON.parse(process.env.REVIEW_REAL_NPM_ARGS), ...args],
  { cwd: process.cwd(), env: process.env, encoding: "utf8", windowsHide: true }
);
if (result.stdout) process.stdout.write(result.stdout);
if (result.stderr) process.stderr.write(result.stderr);
if (result.error) {
  process.stderr.write(result.error.message + String.fromCharCode(10));
  process.exitCode = 1;
} else if (args[0] === "ls" && result.status === 0 && !result.signal) {
  process.exitCode = 1;
} else {
  process.exitCode = result.status === null ? 1 : result.status;
}
`);
  return {
    manager: { name: "npm", file: process.execPath, args: [script] },
    env: {
      REVIEW_REAL_NPM_FILE: npm.file,
      REVIEW_REAL_NPM_ARGS: JSON.stringify(npm.args),
    },
  };
}


test("POSIX manager discovery skips non-executable PATH entries and finds a working later candidate", { skip: process.platform === "win32" }, async (t) => {
  for (const badEntry of ["non-executable file", "directory"]) {
    await t.test(badEntry, () => {
      const directory = fs.mkdtempSync(path.join(os.tmpdir(), "octo-manager-path-"));
      t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
      const badDirectory = path.join(directory, "first");
      const goodDirectory = path.join(directory, "later");
      fs.mkdirSync(badDirectory);
      fs.mkdirSync(goodDirectory);
      const badCandidate = path.join(badDirectory, "npm");
      if (badEntry === "directory") {
        fs.mkdirSync(badCandidate);
      } else {
        fs.writeFileSync(badCandidate, "#!/bin/sh\nexit 19\n", { mode: 0o644 });
      }
      const goodCandidate = path.join(goodDirectory, "npm");
      fs.writeFileSync(goodCandidate, "#!/bin/sh\nexit 0\n", { mode: 0o755 });
      fs.chmodSync(goodCandidate, 0o755);

      const manager = managers.inspectManagerCommands({ PATH: [badDirectory, goodDirectory].join(path.delimiter) }).npm;
      assert.equal(manager.path, goodCandidate, "discovery must continue past unusable earlier PATH entries");
      const result = run(manager, [], {});
      assert.equal(result.status, 0, `${result.error || ""} ${result.stderr || ""}`);
    });
  }
});

test("npm global state accepts complete real npm JSON with an injected ls status 1", { timeout: 120_000 }, (t) => {
  const npm = managers.inspectManagerCommands(process.env).npm;
  assert.ok(npm, "npm must be available for this real package-manager regression");
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "octo-npm-unhealthy-global-"));
  t.after(() => fs.rmSync(home, { recursive: true, force: true }));
  const prefix = path.join(home, "prefix");
  const root = process.platform === "win32"
    ? path.join(prefix, "node_modules")
    : path.join(prefix, "lib", "node_modules");
  const packagePath = path.join(root, ...PACKAGE_NAME.split("/"));
  const launcherPath = path.join(packagePath, "bin", "octo-cli.js");
  const commandPath = process.platform === "win32"
    ? path.join(prefix, "octo-cli.cmd")
    : path.join(prefix, "bin", "octo-cli");
  fs.mkdirSync(path.dirname(launcherPath), { recursive: true });
  fs.mkdirSync(path.dirname(commandPath), { recursive: true });
  fs.writeFileSync(path.join(packagePath, "package.json"), JSON.stringify({
    name: PACKAGE_NAME,
    version: VERSION,
    bin: { "octo-cli": "bin/octo-cli.js" },
  }));
  fs.writeFileSync(launcherPath, ["#!/usr/bin/env node", "process.exit(0);", ""].join(String.fromCharCode(10)), { mode: 0o755 });
  if (process.platform === "win32") {
    const relativeTarget = path.relative(prefix, launcherPath);
    const backslash = String.fromCharCode(92);
    const newline = String.fromCharCode(13, 10);
    fs.writeFileSync(commandPath, `@SET "OCTO_CLI=%~dp0${backslash}${relativeTarget}"${newline}"%_prog%" "%OCTO_CLI%" %*${newline}`);
  } else {
    fs.symlinkSync(launcherPath, commandPath);
  }

  fs.writeFileSync(path.join(home, ".npmrc"), "");
  const statusOne = npmWithLsStatusOne(home, npm);
  const env = { ...npmEnvironment(home, prefix), ...statusOne.env };
  const options = { cwd: home, env };
  const lsArgs = ["ls", "--global", "--long", "--json", "--depth=0"];
  const listingResult = run(statusOne.manager, lsArgs, options);
  assert.equal(listingResult.status, 1, `${listingResult.stderr} ${listingResult.stdout}`);
  const listing = JSON.parse(listingResult.stdout);
  assert.equal(listing.dependencies[PACKAGE_NAME].version, VERSION, "the real npm listing includes the complete own-package record");

  const state = managers.queryLocalState(statusOne.manager, options);
  assert.ok(state, "a complete real npm listing must remain usable when only the command status is nonzero");
  const owner = managers.stateOwnsInvocation(state, PACKAGE_NAME, VERSION, packagePath, launcherPath);
  assert.ok(owner, "the selected package must still pass its real version, path, and command-link checks");
  assert.equal(owner.commandPath, commandPath);
});

test("npm global state rejects spawn, signal, truncated, invalid, and non-listing results", async (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "octo-npm-state-failures-"));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const prefix = path.join(directory, "prefix");
  const root = path.join(prefix, "lib", "node_modules");
  fs.mkdirSync(root, { recursive: true });
  const env = {
    ...npmEnvironment(directory, prefix),
    REVIEW_PREFIX: prefix,
    REVIEW_ROOT: root,
  };
  const manager = fakeNpmManager(directory);
  const listing = JSON.stringify({ name: "global", dependencies: {} });
  const invalidCases = [
    {
      name: "signal during listing",
      env: { REVIEW_LISTING: listing, REVIEW_SIGNAL: "1" },
      skip: process.platform === "win32" && "Windows does not provide POSIX self-signal exit semantics",
    },
    { name: "truncated JSON", env: { REVIEW_LISTING: "{\"dependencies\":", REVIEW_STATUS: "1" } },
    { name: "valid JSON with the wrong npm data shape", env: { REVIEW_LISTING: "[]", REVIEW_STATUS: "1" } },
    { name: "unexpected nonzero exit", env: { REVIEW_LISTING: listing, REVIEW_STATUS: "2" } },
  ];
  assert.equal(managers.queryLocalState({ name: "npm", file: path.join(directory, "missing-npm"), args: [] }, { cwd: directory, env }), null, "a failed spawn must not produce state");
  for (const invalidCase of invalidCases) {
    await t.test(invalidCase.name, { skip: invalidCase.skip }, () => {
      const result = managers.queryLocalState(manager, { cwd: directory, env: { ...env, ...invalidCase.env } });
      assert.equal(result, null, invalidCase.name);
    });
  }
});
