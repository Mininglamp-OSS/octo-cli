"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const {spawnSync, execFileSync} = require("node:child_process");
const {TARGETS, sha256, readConfig} = require("../lib");
const {publishNpm} = require("../publish-npm");
const config = readConfig(path.join(__dirname, "../config.example.json"));
function fixture(t, branch = "test", component = "cli") {
  const version = branch === "test" ? "1.2.3-next.1" : "1.2.3";
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-cos-publish-"));
  t.after(() => fs.rmSync(dir, {recursive: true, force: true}));
  const targets = {};
  for (const target of TARGETS) {
    const file = `octo-${component}-${version}-${target.replace("/", "-")}.tgz`;
    const bytes = Buffer.from(target);
    fs.writeFileSync(path.join(dir, file), bytes);
    targets[target] = {file, size: bytes.length, sha256: sha256(bytes), files: {"package.json": "b".repeat(64), "bin/run.js": "c".repeat(64), [`vendor/octo-${component}${target.startsWith("windows/") ? ".exe" : ""}`]: "d".repeat(64)}};
  }
  const manifest = {schemaVersion: 1, kind: "npm-release", name: `@mininglamp-oss/octo-${component}`, component, branch, version, commit: "a".repeat(40), sourceRef: `refs/remotes/origin/${branch}`, targets};
  const save = () => fs.writeFileSync(path.join(dir, "npm-release.json"), JSON.stringify(manifest));
  save();
  const objects = new Map(); const writes = [];
  const store = {
    async get(key) { return objects.get(key) || null; },
    async put(key, bytes, {immutable = false} = {}) {
      if (immutable && objects.has(key)) throw Object.assign(new Error("collision"), {statusCode: 409, code: "FileAlreadyExists"});
      writes.push(key); objects.set(key, Buffer.from(bytes));
    },
    async remove(key) { objects.delete(key); }
  };
  const fetcher = async url => {
    const key = new URL(url).pathname.slice(1);
    return new Response(objects.get(key) || "missing", {status: objects.has(key) ? 200 : 404});
  };
  return {dir, config, manifest, save, objects, writes, store, fetcher};
}
for (const branch of ["test", "main"]) {
  const root = config.prefixes[branch];
  test(`${branch} COS package preview has no cloud side effects`, async t => {
    const f = fixture(t, branch);
    const plan = await publishNpm({...f, fetcher: () => { throw new Error("Preview must not fetch"); }});
    assert.equal(plan.mode, "dry-run");
    assert.equal(plan.branch, branch);
    assert.equal(plan.upload.length, 7);
    assert.equal(plan.upload.every(key => key.startsWith(`${root}/cli/npm/releases/${f.manifest.version}/`)), true);
    assert.equal(f.writes.length, 0);
  });
  test(`${branch} COS package publication remains isolated and never promotes an installation`, async t => {
    const f = fixture(t, branch);
    const other = `${config.prefixes[branch === "test" ? "main" : "test"]}/cli/npm/existing`;
    f.objects.set(other, Buffer.from("unchanged"));
    const plan = await publishNpm({...f, execute: true});
    assert.equal(plan.branch, branch);
    assert.equal(f.writes.every(key => key.startsWith(`${root}/`)), true);
    assert.equal(f.writes.at(-1), `${root}/cli/npm/releases/${f.manifest.version}/npm-release.json`);
    assert.equal(f.writes.some(key => /install\.js|installation\.json|latest\.json/.test(key)), false);
    assert.equal([...f.objects.keys()].some(key => key.endsWith(".publish-lock")), false);
    assert.equal(f.objects.get(other).toString(), "unchanged");
  });
  test(`${branch} COS packages keep immutable retry behavior`, async t => {
    const f = fixture(t, branch);
    await publishNpm({...f, execute: true});
    const count = f.writes.filter(key => key.includes("/releases/")).length;
    await publishNpm({...f, execute: true});
    assert.equal(f.writes.filter(key => key.includes("/releases/")).length, count);
    const asset = Object.values(f.manifest.targets)[0];
    f.objects.set(`${root}/cli/npm/releases/${f.manifest.version}/${asset.file}`, Buffer.from("different"));
    await assert.rejects(publishNpm({...f, execute: true}), /different contents/);
  });
  test(`${branch} COS package manifests reject the other environment's version format`, async t => {
    const f = fixture(t, branch);
    f.manifest.version = branch === "test" ? "1.2.3" : "1.2.3-next.1";
    f.save();
    await assert.rejects(publishNpm(f), /test requires next.N; main requires a stable version/);
    assert.equal(f.writes.length, 0);
  });
}
test("COS package publication refuses foreign components", async t => {
  const f = fixture(t, "main", "daemon");
  await assert.rejects(publishNpm(f), /own repository/);
});
test("COS package CDN verification failure releases its own lock", async t => {
  const f = fixture(t, "main");
  await assert.rejects(publishNpm({...f, execute: true, fetcher: async () => new Response("bad", {status: 200})}), /CDN npm artifact mismatch|allowed size/);
  assert.equal([...f.objects.keys()].some(key => key.endsWith(".publish-lock")), false);
});
test("COS package publication preserves a contending publisher's lock", async t => {
  const f = fixture(t, "main");
  const key = `${config.prefixes.main}/cli/npm/.publish-lock`;
  f.objects.set(key, Buffer.from("other"));
  await assert.rejects(publishNpm({...f, execute: true}), /collision/);
  assert.equal(f.objects.get(key).toString(), "other");
});
for (const branch of ["test", "main"]) {
  test(`${branch} COS execute requires provenance matching the source branch`, t => {
    const f = fixture(t, branch);
    const repo = path.join(f.dir, "repo");
    const tools = path.join(repo, "scripts", "release");
    fs.mkdirSync(tools, {recursive: true});
    for (const file of ["publish-npm.js", "npm-artifacts.js", "npm-runtime.js", "install.js", "runtime.js", "lib.js", "project.json", "publish-cos.js"]) {
      fs.copyFileSync(path.join(__dirname, "..", file), path.join(tools, file));
    }
    execFileSync("git", ["init", "-q", "--initial-branch", branch, repo]);
    execFileSync("git", ["-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "fixture"], {cwd: repo});
    f.manifest.commit = execFileSync("git", ["rev-parse", "HEAD"], {cwd: repo, encoding: "utf8"}).trim();
    f.manifest.sourceRef = "refs/heads/other";
    f.save();
    const result = spawnSync(process.execPath, [path.join(tools, "publish-npm.js"), "--dist", f.dir, "--execute"], {encoding: "utf8"});
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, new RegExp(`source ref does not match this repository's ${branch} branch`));
  });
}
