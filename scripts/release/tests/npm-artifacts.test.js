"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const {execFileSync} = require("node:child_process");
const {packNpm, verifyNpm} = require("../npm-artifacts");
const {TARGETS, targetName} = require("../runtime");
test("six self-contained packages install offline and preserve package identity", t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "octo-npm-test-"));
  t.after(() => fs.rmSync(dir, {recursive: true, force: true}));
  const m = {component: "cli", branch: "test", version: "1.2.3-next.1", commit: "a".repeat(40), targets: {}};
  for (const target of TARGETS) {
    const binary = `octo-cli${target.startsWith("windows/") ? ".exe" : ""}`;
    const file = target.replace("/", "-") + ".tar.gz";
    fs.writeFileSync(path.join(dir, binary), '#!/bin/sh\necho \'{"data":{"version":"1.2.3-next.1"}}\'\n', {mode: 0o755});
    execFileSync("tar", ["-czf", path.join(dir, file), "-C", dir, binary]);
    m.targets[target] = {file};
  }
  const out = path.join(dir, "npm"); const packed = packNpm(dir, m, out);
  assert.equal(verifyNpm(out).name, "@mininglamp-oss/octo-cli");
  const prefix = path.join(dir, "prefix"); const a = packed.targets[targetName(process.platform, process.arch)];
  execFileSync("npm", ["install", "--global", "--prefix", prefix, "--offline", "--ignore-scripts", "--no-audit", "--no-fund", "--cache", path.join(dir, "cache"), path.join(out, a.file)], {stdio: "pipe"});
  const root = path.join(prefix, process.platform === "win32" ? "node_modules" : "lib/node_modules", packed.name);
  const pkg = JSON.parse(fs.readFileSync(path.join(root, "package.json")));
  assert.equal(pkg.version, m.version); assert.equal(pkg.dependencies, undefined); assert.equal(pkg.optionalDependencies, undefined);
  if (process.platform !== "win32") assert.match(execFileSync(process.execPath, [path.join(root, "bin/run.js"), "version"], {encoding: "utf8"}), /1.2.3-next.1/);
  fs.appendFileSync(path.join(out, a.file), "corrupt");
  assert.throws(() => verifyNpm(out), /checksum/);
});

async function assertSignalForwarded(t, entry, signal, handled) {
  const {spawn} = require("node:child_process");
  const {once} = require("node:events");
  const child = spawn(process.execPath, [entry, handled ? "--signal-probe" : "--signal-exit", signal], {detached: true, stdio: ["ignore", "pipe", "pipe"]});
  let output = "", stderr = "";
  child.stdout.on("data", bytes => { output += bytes; });
  child.stderr.on("data", bytes => { stderr += bytes; });
  t.after(() => { try { process.kill(-child.pid, "SIGKILL"); } catch (error) { if (error.code !== "ESRCH") throw error; } });
  const timeout = AbortSignal.timeout(5000);
  const exited = once(child, "close", {signal: timeout});
  // Observe early errors/exits while waiting for the native process to be ready.
  exited.catch(() => {});
  while (!output.includes("ready")) {
    await Promise.race([
      once(child.stdout, "data", {signal: timeout}),
      exited.then(() => { throw new Error(`Launcher exited before ready: ${stderr}`); })
    ]);
  }
  child.kill(signal);
  const [code, exitSignal] = await exited;
  if (handled) {
    assert.match(output, /handled/, "native process must receive the signal before launcher exit");
    assert.equal(code, 23, stderr);
    assert.equal(exitSignal, null);
  } else {
    assert.equal(code, null, stderr);
    assert.equal(exitSignal, signal);
  }
}

for (const [signal, handled] of [["SIGTERM", true], ["SIGINT", true], ["SIGTERM", false]]) test(`COS CLI package forwards ${signal} (${handled ? "graceful" : "signal exit"})`, {skip: process.platform === "win32"}, async t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-signal-"));
  t.after(() => fs.rmSync(dir, {recursive: true, force: true}));
  const m = {component: "cli", branch: "test", version: "1.2.3-next.1", commit: "a".repeat(40), targets: {}};
  const code = '#!/usr/bin/env node\nif(process.argv.includes("--signal-probe"))process.on(process.argv.at(-1),()=>{console.log("handled");process.exit(23);});console.log("ready");setInterval(()=>{},1000);\n';
  for (const target of TARGETS) {
    const binary = `octo-cli${target.startsWith("windows/") ? ".exe" : ""}`;
    fs.writeFileSync(path.join(dir, binary), code, {mode: 0o755});
    const file = target.replace("/", "-") + ".tar.gz";
    execFileSync("tar", ["-czf", path.join(dir, file), "-C", dir, binary]);
    m.targets[target] = {file};
  }
  const out = path.join(dir, "npm"), packed = packNpm(dir, m, out);
  execFileSync("tar", ["-xzf", path.join(out, packed.targets[targetName(process.platform, process.arch)].file), "-C", dir]);
  await assertSignalForwarded(t, path.join(dir, "package/bin/run.js"), signal, handled);
});
