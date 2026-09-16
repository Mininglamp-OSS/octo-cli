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
