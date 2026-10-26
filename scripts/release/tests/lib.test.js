"use strict";
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const {execFileSync, spawnSync} = require("node:child_process");
const {sourceRef, download} = require("../lib");
const test = require("node:test");
const assert = require("node:assert/strict");
const { normalizeVersion, checkVersion, targetName, readConfig, assertManifest, project, TARGETS } = require("../lib");

test("versions follow existing CLI next.N convention and branch mapping", () => {
  assert.equal(normalizeVersion("v1.2.3-next.4"), "1.2.3-next.4");
  assert.equal(checkVersion("test", "1.2.3-next.4"), "1.2.3-next.4");
  assert.equal(checkVersion("main", "1.2.3"), "1.2.3");
  for (const [branch, version] of [["test", "1.2.3"], ["main", "1.2.3-next.1"], ["feature", "1.2.3"], ["test", "1.2.3-dev.1"], ["test", "1.2.3-next.01"], ["main", "01.2.3"]]) {
    assert.throws(() => checkVersion(branch, version));
  }
});
test("Node platform names map to the existing Go matrix", () => {
  assert.equal(targetName("win32", "x64"), "windows/amd64");
  assert.equal(targetName("darwin", "arm64"), "darwin/arm64");
  assert.throws(() => targetName("linux", "ia32"));
});
test("example configuration loads but cannot be used for actual upload", () => {
  const path = require("node:path").join(__dirname, "../config.example.json");
  assert.equal(readConfig(path).cdnOrigin, "https://cdn.example.com");
  assert.throws(() => readConfig(path, true), /example|replace/);
});
test("manifest accepts all platforms and rejects foreign components, invalid paths and missing platforms", () => {
  const make = () => ({schemaVersion: 1, component: project.component, branch: "test", version: "1.2.3-next.1", commit: "a".repeat(40),
    targets: Object.fromEntries(TARGETS.map(target => [target, {file: target.replace("/", "-") + ".tar.gz", size: 1, sha256: "b".repeat(64), format: "tar.gz"}]))});
  assert.doesNotThrow(() => assertManifest(make()));
  const foreign = make(); foreign.component = "daemon";
  assert.throws(() => assertManifest(foreign), /Unexpected release schema\/component/);
  const unsafe = make(); unsafe.targets[TARGETS[0]].file = "../secret.tar.gz";
  assert.throws(() => assertManifest(unsafe), /Unsafe artifact filename/);
  const missing = make(); delete missing.targets[TARGETS[0]];
  assert.throws(() => assertManifest(missing), /Release must contain exactly six platforms/);
  const extra = make(); extra.targets["linux/386"] = {...extra.targets["linux/amd64"], file: "linux-386.tar.gz"};
  assert.throws(() => assertManifest(extra), /Release must contain exactly six platforms/);
});

test("origin branch selection never silently uses feature branches", t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "octo-ref-test-")); t.after(() => fs.rmSync(dir, {recursive: true, force: true}));
  execFileSync("git", ["init", "-q", dir]);
  assert.throws(() => sourceRef(dir, "dev/v0.14.1"), /not available/);
  assert.throws(() => sourceRef(dir, "feature/test"), /main or dev\/v/);
});
test("redirects are rejected, never followed to another origin", async () => {
  await assert.rejects(download("https://cdn.example.com/a", 100, async (_url, options) => {
    assert.equal(options.redirect, "manual");
    return new Response(null, {status: 302, headers: {location: "https://other.invalid/"}});
  }), /redirects/);
});
test("credentials cannot be embedded in local JSON configuration", t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "octo-config-test-"));
  t.after(() => fs.rmSync(dir, {recursive:true, force:true}));
  const config = readConfig(path.join(__dirname, "../config.example.json"));
  const file = path.join(dir, "config.local.json");
  fs.writeFileSync(file, JSON.stringify({...config, SecretKey: "unit-test-placeholder"}));
  assert.throws(() => readConfig(file), /credentials belong/);
});

for (const file of ["build.js", "publish.js"]) test(`${file} help needs no configuration or credentials`, () => {
  const result = spawnSync(process.execPath, [path.join(__dirname, "..", file), "--help"], {cwd:os.tmpdir(), encoding:"utf8"});
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /--help|--ref|--dist/);
});

for (const explicit of [false, true]) test(`release children cannot inherit COS credentials (explicit env: ${explicit})`, t => {
  const {run} = require("../lib");
  const keys = ["COS_SECRET_ID", "COS_SECRET_KEY", "COS_SESSION_TOKEN"];
  const previous = keys.map(key => process.env[key]);
  t.after(() => keys.forEach((key, i) => { if (previous[i] === undefined) delete process.env[key]; else process.env[key] = previous[i]; }));
  for (const key of keys) process.env[key] = "unit-test-placeholder";
  const env = {...process.env, BUILD_PROBE: "retained", cos_secret_key: "unit-test-placeholder"};
  const options = explicit ? {env} : {};
  const result = JSON.parse(run(process.execPath, ["-e", 'console.log(JSON.stringify({cosKeys:Object.keys(process.env).filter(k=>/^COS_/i.test(k)),probe:process.env.BUILD_PROBE}))'], options));
  assert.deepEqual(result.cosKeys, []);
  if (explicit) assert.equal(result.probe, "retained");
  for (const key of keys) assert.equal(process.env[key], "unit-test-placeholder", "publisher credentials must remain in the parent");
  assert.equal(env.cos_secret_key, "unit-test-placeholder", "caller-supplied environment must not be mutated");
});

for (const branch of ["main", "dev/v0.14.1", "dev/v1.0.0"]) test(`source ${branch} selects its COS environment and prefers origin`, t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "octo-source-channel-"));
  t.after(() => fs.rmSync(dir, {recursive: true, force: true}));
  const git = (...args) => execFileSync("git", args, {cwd: dir, encoding: "utf8"}).trim();
  git("init", "-q", "--initial-branch", branch);
  git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "initial");
  const commit = git("rev-parse", "HEAD");
  const channel = branch === "main" ? "main" : "test";
  assert.deepEqual(sourceRef(dir, branch), {branch, channel, ref: `refs/heads/${branch}`, commit});
  git("update-ref", `refs/remotes/origin/${branch}`, commit);
  git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "local-only");
  assert.deepEqual(sourceRef(dir, branch), {branch, channel, ref: `refs/remotes/origin/${branch}`, commit});
});
test("only main and versioned development sources are allowed", () => {
  for (const branch of ["test", "feature/test", "dev/v", "dev/v01.2.3", "dev/v1.2.3/nested", "dev/v1.2.3-next.1"]) {
    assert.throws(() => sourceRef(os.tmpdir(), branch), /main or dev\/v/);
  }
});

test("native manifest rejects a comma-joined platform key", () => {
  const m = {schemaVersion: 1, component: "cli", branch: "test", version: "1.2.3-next.1", commit: "a".repeat(40),
    targets: {[TARGETS.slice().sort().join()]: {file: "cli.tar.gz", format: "tar.gz", size: 1, sha256: "a".repeat(64)}}};
  assert.throws(() => assertManifest(m), /exactly six platforms/);
});
test("configuration rejects comma-joined environment keys explicitly", t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "octo-config-keys-"));
  t.after(() => fs.rmSync(dir, {recursive: true, force: true}));
  const file = path.join(dir, "config.json");
  const config = readConfig(path.join(__dirname, "../config.example.json"));
  fs.writeFileSync(file, JSON.stringify({...config, prefixes: {"main,test": "static/invalid"}}));
  assert.throws(() => readConfig(file), /Configure exactly main\/test prefixes/);
});

for (const [name, prefixes, error] of [
  ["extra environment", {main: "static/main", test: "static/test", extra: "static/extra"}, /Configure exactly main\/test prefixes/],
  ["identical prefixes", {main: "static/shared", test: "static/shared"}, /must be disjoint/],
  ["test inside main", {main: "static/octo", test: "static/octo/test"}, /must be disjoint/],
  ["main inside test", {main: "static/octo/main", test: "static/octo"}, /must be disjoint/],
  ["disjoint prefixes with a shared string prefix", {main: "static/octo", test: "static/octo-test"}, null]
]) test(`configuration validates ${name}`, t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "octo-prefix-test-"));
  t.after(() => fs.rmSync(dir, {recursive: true, force: true}));
  const config = readConfig(path.join(__dirname, "../config.example.json"));
  const file = path.join(dir, "config.json");
  fs.writeFileSync(file, JSON.stringify({...config, prefixes}));
  if (error) assert.throws(() => readConfig(file), error);
  else assert.deepEqual(readConfig(file).prefixes, prefixes);
});
