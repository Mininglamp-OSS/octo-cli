"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const {execFileSync} = require("node:child_process");
const {packNpm, verifyNpm} = require("../build");
const {TARGETS, targetName} = require("../lib");
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

for (const format of ["table", "csv"]) test(`host version probe overrides OCTO_FORMAT=${format} without COS credentials`, {skip: process.platform === "win32"}, async t => {
  const {sha256} = require("../lib");
  const {smokeDist} = require("../build");
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-format-"));
  t.after(() => fs.rmSync(dir, {recursive: true, force: true}));
  const before = process.env.OCTO_FORMAT;
  t.after(() => { if (before === undefined) delete process.env.OCTO_FORMAT; else process.env.OCTO_FORMAT = before; });
  process.env.OCTO_FORMAT = format;
  // Behave like the real CLI: the explicit flag overrides the environment.
  const code = '#!/usr/bin/env node\nif(process.env.COS_SECRET_KEY)process.exit(91);const args=process.argv.slice(2); const format=args.includes("--format") ? args[args.indexOf("--format")+1] : process.env.OCTO_FORMAT; console.log(format === "json" ? JSON.stringify({data:{version:"1.2.3-next.1"}}) : "build_date,commit,version");\n';
  fs.writeFileSync(path.join(dir, "octo-cli"), code, {mode: 0o755});
  const file = "host.tar.gz";
  execFileSync("tar", ["-czf", path.join(dir, file), "-C", dir, "octo-cli"]);
  const bytes = fs.readFileSync(path.join(dir, file));
  const targets = {};
  for (const target of TARGETS) {
    const name = target.replace("/", "-") + ".tar.gz";
    fs.writeFileSync(path.join(dir, name), bytes);
    targets[target] = {file:name, size:bytes.length, sha256:sha256(bytes), format:"tar.gz"};
  }
  fs.writeFileSync(path.join(dir, "release.json"), JSON.stringify({schemaVersion:1, component:"cli", branch:"test", version:"1.2.3-next.1", commit:"a".repeat(40), targets}));
  fs.writeFileSync(path.join(dir, "checksums.txt"), Object.values(targets).map(a => `${a.sha256}  ${a.file}\n`).join(""));
  const beforeKey = process.env.COS_SECRET_KEY;
  t.after(() => { if (beforeKey === undefined) delete process.env.COS_SECRET_KEY; else process.env.COS_SECRET_KEY = beforeKey; });
  process.env.COS_SECRET_KEY = "unit-test-placeholder";
  await assert.doesNotReject(async () => smokeDist(dir));
});

test("archive symlinks are refused without extracting their target", {skip: process.platform === "win32"}, t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "octo-archive-link-"));
  t.after(() => fs.rmSync(dir, {recursive: true, force: true}));
  const stage = path.join(dir, "links"); fs.mkdirSync(stage);
  fs.symlinkSync("/etc/passwd", path.join(stage, "octo-cli"));
  const archive = path.join(dir, "link.tar.gz");
  execFileSync("tar", ["-czf", archive, "-C", stage, "octo-cli"]);
  assert.throws(() => require("../build").extractBinary(archive, "octo-cli"), /regular file/);
});
