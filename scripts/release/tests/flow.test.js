"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const {spawnSync, execFileSync} = require("node:child_process");
const {project, TARGETS, sha256, readConfig, renderInstaller, verifyDist, assertManifest, sourceRef} = require("../lib");
const {publishRelease} = require("../publish-cos");
const {install} = require("../install");
const config = readConfig(path.join(__dirname, "../config.example.json"));
function fixture(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "octo-release-test-"));
  t.after(() => fs.rmSync(dir, {recursive: true, force: true}));
  const targets = {};
  for (const target of TARGETS) {
    const name = `${project.binary}-1.2.3-next.1-${target.replace("/", "-")}.tar.gz`;
    const bytes = Buffer.from(target); fs.writeFileSync(path.join(dir, name), bytes);
    targets[target] = {file: name, sha256: sha256(bytes), size: bytes.length, format: "tar.gz"};
  }
  const manifest = {schemaVersion: 1, component: project.component, branch: "test", version: "1.2.3-next.1", commit: "a".repeat(40), targets};
  saveManifest(dir, manifest);
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
  return {dir, manifest, objects, writes, store, fetcher};
}
function saveManifest(dir, m) {
  fs.writeFileSync(path.join(dir, "release.json"), JSON.stringify(m));
  fs.writeFileSync(path.join(dir, "checksums.txt"), Object.values(m.targets).map(a => `${a.sha256}  ${a.file}\n`).join(""));
}
test("dry run never instantiates a cloud client or makes HTTP requests", async t => {
  const f = fixture(t);
  const plan = await publishRelease({...f, config, fetcher: () => {throw new Error("must not fetch");}});
  assert.equal(plan.mode, "dry-run"); assert.equal(f.writes.length, 0);
});
test("publication uploads immutable artifacts then verifies CDN before latest", async t => {
  const f = fixture(t);
  await publishRelease({...f, config, execute: true});
  assert.equal(f.writes.at(-1), `static/octo-loop-test/${project.component}/latest.json`);
  assert.equal([...f.objects.keys()].some(k => k.endsWith(".publish-lock")), false);
  assert.equal(f.writes.some(k => k.startsWith("static/octo-loop/")), false);
});
test("failed CDN verification does not promote and releases lock", async t => {
  const f = fixture(t);
  await assert.rejects(publishRelease({...f, config, execute: true, fetcher: async () => new Response("bad", {status: 200})}), /CDN mismatch|allowed size/);
  assert.equal(f.writes.some(k => k.endsWith("latest.json")), false);
  assert.equal([...f.objects.keys()].some(k => k.endsWith(".publish-lock")), false);
});
test("immutable collision fails without promoting", async t => {
  const f = fixture(t); const a = Object.values(f.manifest.targets)[0];
  f.objects.set(`static/octo-loop-test/${project.component}/releases/${f.manifest.version}/${a.file}`, Buffer.from("different"));
  await assert.rejects(publishRelease({...f, config, execute: true}), /different contents/);
  assert.equal(f.writes.some(k => k.endsWith("latest.json")), false);
});
test("lock contention never removes another publisher's lock", async t => {
  const f = fixture(t); const key = `static/octo-loop-test/${project.component}/.publish-lock`;
  f.objects.set(key, Buffer.from("other"));
  await assert.rejects(publishRelease({...f, config, execute: true}), /collision/);
  assert.equal(f.objects.get(key).toString(), "other");
});
test("older release cannot accidentally replace a newer latest", async t => {
  const f = fixture(t);
  f.objects.set(`static/octo-loop-test/${project.component}/latest.json`, Buffer.from(JSON.stringify({schemaVersion: 1, component: project.component, branch: "test", version: "1.2.3-next.10"})));
  await assert.rejects(publishRelease({...f, config, execute: true}), /backwards/);
  assert.equal(f.writes.some(k => k.endsWith("latest.json")), false);
});
test("tampered local artifact fails even in dry-run", async t => {
  const f = fixture(t); fs.writeFileSync(path.join(f.dir, Object.values(f.manifest.targets)[0].file), "tampered");
  await assert.rejects(publishRelease({...f, config}), /Artifact mismatch/);
});
test("manifest filenames cannot escape the release directory", t => {
  const f = fixture(t); Object.values(f.manifest.targets)[0].file = "../secret.tar.gz";
  assert.throws(() => assertManifest(f.manifest), /Unsafe/);
});
test("generated installer runs from stdin without SDK or neighboring files", () => {
  const script = renderInstaller(config, "test");
  const r = spawnSync(process.execPath, ["-", "--help"], {input: script, encoding: "utf8", cwd: os.tmpdir()});
  assert.equal(r.status, 0, r.stderr); assert.match(r.stdout, /--version/);
});
test("installer refuses environment mismatch and bad hashes before touching prefix", async t => {
  const f = fixture(t); const prefix = path.join(f.dir, "prefix");
  const settings = {component: project.component, binary: project.binary, branch: "test", baseUrl: "https://cdn.example.com/static/octo-loop-test/"};
  await assert.rejects(install(settings, ["--version", "1.2.3", "--prefix", prefix], f.fetcher), /test requires/);
  const root = `static/octo-loop-test/${project.component}/releases/1.2.3-next.1/`;
  f.objects.set(root + "release.json", Buffer.from(JSON.stringify(f.manifest)));
  for (const a of Object.values(f.manifest.targets)) f.objects.set(root + a.file, Buffer.from("bad"));
  await assert.rejects(install(settings, ["--version", "1.2.3-next.1", "--prefix", prefix], f.fetcher), /mismatch/);
  assert.equal(fs.existsSync(prefix), false);
});
test("host installer installs a verified archive, pinned and latest, without registry", {skip: process.platform === "win32"}, async t => {
  const f = fixture(t); const stage = path.join(f.dir, "stage"); fs.mkdirSync(stage);
  const flag = project.component === "daemon" ? "--version" : "version";
  const output = project.component === "daemon" ? "octo-daemon 1.2.3-next.1" : '{"data":{"version":"1.2.3-next.1"}}';
  fs.writeFileSync(path.join(stage, project.binary), `#!/bin/sh\n[ "$1" = "${flag}" ] || exit 7\nprintf '%s\\n' '${output}'\n`, {mode: 0o755});
  const target = `${process.platform}/${process.arch === "x64" ? "amd64" : process.arch}`;
  const a = f.manifest.targets[target]; const archive = path.join(f.dir, a.file);
  execFileSync("tar", ["-czf", archive, "-C", stage, project.binary]);
  const bytes = fs.readFileSync(archive); a.size = bytes.length; a.sha256 = sha256(bytes); saveManifest(f.dir, f.manifest);
  await publishRelease({...f, config, execute: true});
  const settings = {component: project.component, binary: project.binary, branch: "test", baseUrl: "https://cdn.example.com/static/octo-loop-test/"};
  const prefix = path.join(f.dir, "prefix");
  const installed = await install(settings, ["--prefix", prefix], f.fetcher);
  assert.equal(execFileSync(installed, [flag], {encoding: "utf8"}).trim(), output);
  await install(settings, ["--version", "v1.2.3-next.1", "--prefix", prefix], f.fetcher);
  fs.writeFileSync(installed, "unmanaged");
  await assert.rejects(install(settings, ["--prefix", prefix], f.fetcher), /receipt/);
});
test("origin branch selection never silently uses feature branches", t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "octo-ref-test-")); t.after(() => fs.rmSync(dir, {recursive: true, force: true}));
  execFileSync("git", ["init", "-q", dir]);
  assert.throws(() => sourceRef(dir, "test"), /not available/);
  assert.throws(() => sourceRef(dir, "feature/test"), /main or test/);
});


test("a failed upload never writes latest", async t => {
  const f = fixture(t); const originalPut = f.store.put;
  f.store.put = async (key, ...rest) => {
    if (key.includes("/releases/")) throw new Error("simulated upload failure");
    return originalPut(key, ...rest);
  };
  await assert.rejects(publishRelease({...f, config, execute: true}), /simulated/);
  assert.equal(f.writes.some(k => k.endsWith("latest.json")), false);
});
test("retry reuses byte-identical immutable versions", async t => {
  const f = fixture(t);
  await publishRelease({...f, config, execute: true});
  const firstCount = f.writes.filter(k => k.includes("/releases/")).length;
  await publishRelease({...f, config, execute: true});
  assert.equal(f.writes.filter(k => k.includes("/releases/")).length, firstCount);
});
test("explicit rollback may promote an older existing release", async t => {
  const f = fixture(t); const key = `static/octo-loop-test/${project.component}/latest.json`;
  f.objects.set(key, Buffer.from(JSON.stringify({schemaVersion: 1, component: project.component, branch: "test", version: "1.2.3-next.10"})));
  await publishRelease({...f, config, execute: true, allowRollback: true});
  assert.equal(JSON.parse(f.objects.get(key)).version, "1.2.3-next.1");
});
test("CDN pointer verification failure is reported as already written", async t => {
  const f = fixture(t);
  const fetcher = async url => url.endsWith("latest.json") ? new Response("stale", {status: 200}) : f.fetcher(url);
  await assert.rejects(publishRelease({...f, config, execute: true, fetcher}), /latest was written/);
  assert.equal(f.writes.some(k => k.endsWith("latest.json")), true);
});
test("archive symlinks are refused without extracting their target", {skip: process.platform === "win32"}, t => {
  const f = fixture(t); const stage = path.join(f.dir, "links"); fs.mkdirSync(stage);
  fs.symlinkSync("/etc/passwd", path.join(stage, project.binary));
  const archive = path.join(f.dir, "link.tar.gz");
  execFileSync("tar", ["-czf", archive, "-C", stage, project.binary]);
  assert.throws(() => require("../install").extractBinary(archive, project.binary), /regular file/);
});
test("redirects are rejected, never followed to another origin", async () => {
  const {download} = require("../runtime");
  await assert.rejects(download("https://cdn.example.com/a", 100, async (_url, options) => {
    assert.equal(options.redirect, "manual");
    return new Response(null, {status: 302, headers: {location: "https://other.invalid/"}});
  }), /redirects/);
});
test("credentials cannot be embedded in local JSON configuration", t => {
  const f = fixture(t); const file = path.join(f.dir, "config.local.json");
  fs.writeFileSync(file, JSON.stringify({...config, SecretKey: "unit-test-placeholder"}));
  assert.throws(() => readConfig(file), /credentials belong/);
});
test("SDK transport uses authenticated HTTPS and sanitizes SDK errors", async t => {
  const before = {id: process.env.COS_SECRET_ID, key: process.env.COS_SECRET_KEY};
  t.after(() => {
    for (const [key, value] of [["COS_SECRET_ID", before.id], ["COS_SECRET_KEY", before.key]]) {
      if (value === undefined) delete process.env[key]; else process.env[key] = value;
    }
  });
  process.env.COS_SECRET_ID = "unit-test-id"; process.env.COS_SECRET_KEY = "unit-test-key";
  const calls = [];
  class SDK {
    constructor(options) { assert.equal(options.Protocol, "https:"); }
    putObject(params, callback) { calls.push(params); callback(null, {}); }
    getObject(_params, callback) { callback({statusCode: 403, message: "unit-test-key signed-url"}); }
  }
  const store = require("../publish-cos").createStore(config, SDK);
  await store.put("some-key", Buffer.from("bytes"), {immutable: true});
  assert.equal(calls[0].Headers["x-cos-forbid-overwrite"], "true");
  assert.equal(calls[0].ContentLength, 5);
  await assert.rejects(store.get("some-key"), error => error.message.includes("403") && !error.message.includes("unit-test-key"));
});

test("conditional-write probe rejects overwriting stores before release uploads", async t => {
  const f = fixture(t);
  const put = f.store.put;
  f.store.put = (key, bytes) => put(key, bytes);
  await assert.rejects(publishRelease({...f, config, execute: true}), /does not enforce conditional creation/);
  assert.equal(f.writes.every(key => key.includes(".publish-probe-")), true);
  assert.equal(f.objects.size, 0);
});
test("conditional-write probe does not treat permission denial as conflict", async t => {
  const f = fixture(t);
  const put = f.store.put;
  f.store.put = async (key, bytes, options) => {
    if (f.objects.has(key)) throw Object.assign(new Error("denied"), {statusCode: 403});
    return put(key, bytes, options);
  };
  await assert.rejects(publishRelease({...f, config, execute: true}), /denied/);
  assert.equal(f.objects.size, 0);
});
test("conditional-write probe verifies original bytes survive the rejected overwrite", async t => {
  const f = fixture(t);
  const put = f.store.put;
  f.store.put = async (key, bytes, options) => {
    if (f.objects.has(key)) {
      f.objects.set(key, Buffer.from(bytes));
      throw Object.assign(new Error("collision"), {statusCode: 409, code: "FileAlreadyExists"});
    }
    return put(key, bytes, options);
  };
  await assert.rejects(publishRelease({...f, config, execute: true}), /Conditional creation changed/);
  assert.equal(f.objects.size, 0);
});
test("conditional-write probe cleanup failure prevents release uploads", async t => {
  const f = fixture(t);
  f.store.remove = async () => { throw new Error("delete denied"); };
  await assert.rejects(publishRelease({...f, config, execute: true}), /delete denied/);
  assert.equal(f.writes.every(key => key.includes(".publish-probe-")), true);
});
