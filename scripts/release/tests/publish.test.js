"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const {spawnSync, execFileSync} = require("node:child_process");
const {TARGETS, sha256, readConfig} = require("../lib");
const {publishPackages} = require("../publish");
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
  const sourceBranch = branch === "test" ? "dev/v0.14.1" : "main";
  const repo = path.join(dir, "source");
  execFileSync("git", ["init", "-q", "--initial-branch", sourceBranch, repo]);
  execFileSync("git", ["-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "fixture"], {cwd: repo});
  const commit = execFileSync("git", ["rev-parse", "HEAD"], {cwd: repo, encoding: "utf8"}).trim();
  execFileSync("git", ["update-ref", `refs/remotes/origin/${sourceBranch}`, commit], {cwd: repo});
  const manifest = {schemaVersion: 1, kind: "npm-release", name: `@mininglamp-oss/octo-${component}`, component, branch, version, commit, sourceBranch, sourceRef: `refs/remotes/origin/${sourceBranch}`, targets};
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
  return {dir, repo, config, manifest, save, objects, writes, store, fetcher};
}
for (const branch of ["test", "main"]) {
  const root = config.prefixes[branch];
  test(`${branch} COS package preview has no cloud side effects`, async t => {
    const f = fixture(t, branch);
    const plan = await publishPackages({...f, fetcher: () => { throw new Error("Preview must not fetch"); }});
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
    const plan = await publishPackages({...f, execute: true});
    assert.equal(plan.branch, branch);
    assert.equal(f.writes.every(key => key.startsWith(`${root}/`)), true);
    assert.equal(f.writes.at(-1), `${root}/cli/npm/releases/${f.manifest.version}/npm-release.json`);
    assert.equal(f.writes.some(key => /install\.js|installation\.json|latest\.json/.test(key)), false);
    assert.equal([...f.objects.keys()].some(key => key.endsWith(".publish-lock")), false);
    assert.equal(f.objects.get(other).toString(), "unchanged");
  });
  test(`${branch} COS packages keep immutable retry behavior`, async t => {
    const f = fixture(t, branch);
    await publishPackages({...f, execute: true});
    const count = f.writes.filter(key => key.includes("/releases/")).length;
    await publishPackages({...f, execute: true});
    assert.equal(f.writes.filter(key => key.includes("/releases/")).length, count);
    const asset = Object.values(f.manifest.targets)[0];
    f.objects.set(`${root}/cli/npm/releases/${f.manifest.version}/${asset.file}`, Buffer.from("different"));
    await assert.rejects(publishPackages({...f, execute: true}), /different contents/);
  });
  test(`${branch} COS package manifests reject the other environment's version format`, async t => {
    const f = fixture(t, branch);
    f.manifest.version = branch === "test" ? "1.2.3" : "1.2.3-next.1";
    f.save();
    await assert.rejects(publishPackages(f), /test requires next.N; main requires a stable version/);
    assert.equal(f.writes.length, 0);
  });
}
test("COS package publication refuses foreign components", async t => {
  const f = fixture(t, "main", "daemon");
  await assert.rejects(publishPackages(f), /own repository/);
});
test("COS package CDN verification failure releases its own lock", async t => {
  const f = fixture(t, "main");
  await assert.rejects(publishPackages({...f, execute: true, fetcher: async () => new Response("bad", {status: 200})}), /CDN npm artifact mismatch|allowed size/);
  assert.equal([...f.objects.keys()].some(key => key.endsWith(".publish-lock")), false);
});
test("COS package publication preserves a contending publisher's lock", async t => {
  const f = fixture(t, "main");
  const key = `${config.prefixes.main}/cli/npm/.publish-lock`;
  f.objects.set(key, Buffer.from("other"));
  await assert.rejects(publishPackages({...f, execute: true}), /collision/);
  assert.equal(f.objects.get(key).toString(), "other");
});
for (const branch of ["test", "main"]) for (const scenario of ["valid", "wrong ref", "manifest replaced"]) {
  test(`${branch} CLI source validation: ${scenario}`, t => {
    const f = fixture(t, branch);
    const tools = path.join(f.repo, "scripts", "release");
    fs.mkdirSync(tools, {recursive: true});
    for (const file of ["publish.js", "build.js", "lib.js", "cos.js"]) {
      fs.copyFileSync(path.join(__dirname, "..", file), path.join(tools, file));
    }
    const configFile = path.join(f.dir, "config.json");
    fs.writeFileSync(configFile, JSON.stringify({...config, bucket: "validation-1234567890", region: "ap-guangzhou", cdnOrigin: "https://cdn.validation.invalid"}));
    if (scenario === "wrong ref") { f.manifest.sourceRef = "refs/heads/other"; f.save(); }
    const otherBranch = branch === "test" ? "main" : "test";
    const replacement = {...f.manifest, branch: otherBranch, version: otherBranch === "main" ? "1.2.3" : "1.2.3-next.1",
      sourceBranch: otherBranch === "main" ? "main" : "dev/v0.14.1", sourceRef: "refs/heads/other"};
    const preload = path.join(f.dir, "preload.cjs");
    fs.writeFileSync(preload, `
      const fs = require("node:fs");
      require(${JSON.stringify(path.join(tools, "cos.js"))}).createStore = () => { throw Error("CLOUD_REACHED"); };
      const read = fs.readFileSync;
      fs.readFileSync = function(file, ...args) {
        const bytes = read.call(this, file, ...args);
        if (${JSON.stringify(scenario)} === "manifest replaced" && file === ${JSON.stringify(configFile)}) {
          fs.writeFileSync(${JSON.stringify(path.join(f.dir, "npm-release.json"))}, ${JSON.stringify(JSON.stringify(replacement))});
        }
        return bytes;
      };
    `);
    const result = spawnSync(process.execPath, ["--require", preload, path.join(tools, "publish.js"), "--dist", f.dir, "--config", configFile, "--execute"], {encoding: "utf8"});
    assert.equal(result.status, 1);
    if (scenario === "valid") assert.match(result.stderr, /CLOUD_REACHED/, "valid source reaches transport setup");
    else {
      assert.doesNotMatch(result.stderr, /CLOUD_REACHED/);
      assert.match(result.stderr, /source ref does not match|not available/);
    }
  });
}

for (const method of ["get", "remove"]) {
  for (const failUpload of [false, true]) {
    test(`lock cleanup ${method} failure preserves ${failUpload ? "upload error" : "successful publication"}`, async t => {
      const f = fixture(t);
      const key = `${config.prefixes.test}/cli/npm/.publish-lock`;
      const original = f.store[method];
      f.store[method] = async (objectKey, ...rest) => {
        if (objectKey === key) throw new Error("private signed-url secret");
        return original(objectKey, ...rest);
      };
      const originalPut = f.store.put;
      const uploadError = new Error("artifact upload failed");
      if (failUpload) f.store.put = async (objectKey, ...rest) => {
        if (objectKey.includes("/releases/")) throw uploadError;
        return originalPut(objectKey, ...rest);
      };
      const warnings = [];
      const promise = publishPackages({...f, execute: true, warn: text => warnings.push(text)});
      if (failUpload) await assert.rejects(promise, error => error === uploadError);
      else assert.equal((await promise).mode, "execute");
      assert.equal(warnings.length, 1);
      assert.ok(warnings[0].includes(key));
      assert.match(warnings[0], /inspect/i);
      assert.doesNotMatch(warnings[0], /private|signed-url|secret/);
      assert.equal(f.objects.has(key), true);
    });
  }
}
test("lock cleanup preserves changed ownership and reports the object key", async t => {
  const f = fixture(t);
  const key = `${config.prefixes.test}/cli/npm/.publish-lock`;
  const warnings = [];
  const fetcher = async url => {
    f.objects.set(key, Buffer.from("different owner"));
    return f.fetcher(url);
  };
  await publishPackages({...f, fetcher, execute: true, warn: text => warnings.push(text)});
  assert.equal(f.objects.get(key).toString(), "different owner");
  assert.equal(warnings.length, 1);
  assert.ok(warnings[0].includes(key));
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
  const store = require("../cos").createStore(config, SDK);
  await store.put("some-key", Buffer.from("bytes"), {immutable: true});
  assert.equal(calls[0].Headers["x-cos-forbid-overwrite"], "true");
  assert.equal(calls[0].ContentLength, 5);
  await assert.rejects(store.get("some-key"), error => error.message.includes("403") && !error.message.includes("unit-test-key"));
});

test("conditional-write probe rejects overwriting stores before release uploads", async t => {
  const f = fixture(t);
  const put = f.store.put;
  f.store.put = (key, bytes) => put(key, bytes);
  await assert.rejects(publishPackages({...f, execute: true}), /does not enforce conditional creation/);
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
  await assert.rejects(publishPackages({...f, execute: true}), /denied/);
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
  await assert.rejects(publishPackages({...f, execute: true}), /Conditional creation changed/);
  assert.equal(f.objects.size, 0);
});
test("conditional-write probe cleanup failure prevents release uploads", async t => {
  const f = fixture(t);
  f.store.remove = async () => { throw new Error("delete denied"); };
  await assert.rejects(publishPackages({...f, execute: true}), /Could not clean up the COS publish probe/);
  assert.equal(f.writes.every(key => key.includes(".publish-probe-")), true);
});

test("a failed artifact upload never writes the release manifest", async t => {
  const f = fixture(t);
  const put = f.store.put;
  f.store.put = async (key, ...rest) => {
    if (key.endsWith(".tgz")) throw new Error("simulated upload failure");
    return put(key, ...rest);
  };
  await assert.rejects(publishPackages({...f, execute: true}), /simulated upload failure/);
  assert.equal(f.writes.some(key => key.endsWith("npm-release.json")), false);
  assert.equal([...f.objects.keys()].some(key => key.endsWith(".publish-lock")), false);
});
test("tampered local package fails before any cloud requests", async t => {
  const f = fixture(t);
  fs.writeFileSync(path.join(f.dir, Object.values(f.manifest.targets)[0].file), "tampered");
  await assert.rejects(publishPackages({...f, execute: true}), /mismatch/);
  assert.equal(f.writes.length, 0);
});
test("immutable upload verifies COS read-back before CDN requests", async t => {
  const f = fixture(t);
  const get = f.store.get;
  f.store.get = async key => key.endsWith(".tgz") && f.objects.has(key) ? Buffer.from("tampered") : get(key);
  await assert.rejects(publishPackages({...f, execute: true, fetcher: () => {throw new Error("must not fetch");}}), /COS read-back mismatch/);
  assert.equal(f.writes.some(key => key.endsWith("npm-release.json")), false);
});
test("probe cleanup failure cannot mask a conditional-creation failure", async t => {
  const f = fixture(t);
  const put = f.store.put;
  f.store.put = (key, bytes) => put(key, bytes);
  f.store.remove = async () => {throw new Error("signed-url secret");};
  const warnings = [];
  await assert.rejects(publishPackages({...f, execute: true, warn: text => warnings.push(text)}), /does not enforce conditional creation/);
  assert.equal(warnings.length, 1);
  assert.match(warnings[0], /inspect .*\.publish-probe-/i);
  assert.doesNotMatch(warnings[0], /signed-url|secret/);
  assert.equal(f.writes.every(key => key.includes(".publish-probe-")), true);
});
test("probe cleanup refuses to delete an externally replaced probe", async t => {
  const f = fixture(t);
  const get = f.store.get;
  let reads = 0;
  f.store.get = async key => {
    if (key.includes(".publish-probe-") && ++reads === 2) f.objects.set(key, Buffer.from("foreign"));
    return get(key);
  };
  await assert.rejects(publishPackages({...f, execute: true}), /Could not clean up the COS publish probe/);
  assert.deepEqual([...f.objects.values()].map(bytes => bytes.toString()), ["foreign"]);
  assert.equal(f.writes.every(key => key.includes(".publish-probe-")), true);
});
test("publisher help does not require configuration or cloud credentials", () => {
  const result = spawnSync(process.execPath, [path.join(__dirname, "../publish.js"), "--help"], {encoding: "utf8"});
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /--execute/);
  assert.match(result.stdout, /dry-run/);
});

for (const branch of ["test", "main"]) {
  test(`${branch} rejects a package changed while acquiring the publish lock`, async t => {
    const f = fixture(t, branch); const put = f.store.put;
    const asset = Object.values(f.manifest.targets)[0];
    f.store.put = async (key, bytes, options) => {
      await put(key, bytes, options);
      if (key.endsWith("/.publish-lock")) fs.writeFileSync(path.join(f.dir, asset.file), "changed after validation");
    };
    await assert.rejects(publishPackages({...f, execute: true}), /npm artifact checksum mismatch/);
    assert.equal(f.writes.some(key => key.includes("/releases/")), false);
    assert.equal([...f.objects.keys()].some(key => key.endsWith(".publish-lock")), false);
  });
  test(`${branch} CDN verification uses uploaded bytes if the local package later changes`, async t => {
    const f = fixture(t, branch); const put = f.store.put;
    f.store.put = async (key, bytes, options) => {
      await put(key, bytes, options);
      if (key.endsWith(".tgz")) fs.writeFileSync(path.join(f.dir, path.basename(key)), "changed after upload");
    };
    assert.equal((await publishPackages({...f, execute: true})).mode, "execute");
    const root = `${config.prefixes[branch]}/cli/npm/releases/${f.manifest.version}/`;
    for (const asset of Object.values(f.manifest.targets)) assert.equal(sha256(f.objects.get(root + asset.file)), asset.sha256);
  });
  test(`${branch} publication uses the validated manifest when the local manifest changes`, async t => {
    const f = fixture(t, branch); const put = f.store.put;
    f.store.put = async (key, bytes, options) => {
      await put(key, bytes, options);
      if (key.endsWith("/.publish-lock")) fs.writeFileSync(path.join(f.dir, "npm-release.json"), '{"tampered":true}');
    };
    await publishPackages({...f, execute: true});
    const key = `${config.prefixes[branch]}/cli/npm/releases/${f.manifest.version}/npm-release.json`;
    assert.deepEqual(JSON.parse(f.objects.get(key)), f.manifest);
  });
}
for (const object of ["lock", "probe"]) for (const committed of [false, true]) {
  test(`${object} acquisition timeout cleans up only its possibly committed object (${committed})`, async t => {
    const f = fixture(t); const put = f.store.put;
    const failure = new Error("simulated acquisition timeout");
    let attempted;
    f.store.put = async (key, bytes, options) => {
      const matches = object === "lock" ? key.endsWith("/.publish-lock") : key.includes("/.publish-probe-");
      if (matches && !attempted) {
        attempted = key;
        if (committed) await put(key, bytes, options);
        throw failure;
      }
      await put(key, bytes, options);
    };
    await assert.rejects(publishPackages({...f, execute: true}), error => error === failure);
    assert.ok(attempted);
    assert.equal(f.objects.has(attempted), false);
    assert.equal(f.writes.some(key => key.includes("/releases/")), false);
  });
}

test("publication preserves original manifest bytes when retrying an existing release", async t => {
  const f = fixture(t);
  const key = `${config.prefixes.test}/cli/npm/releases/${f.manifest.version}/npm-release.json`;
  const original = fs.readFileSync(path.join(f.dir, "npm-release.json"));
  f.objects.set(key, original);
  assert.equal((await publishPackages({...f, execute: true})).mode, "execute");
  assert.deepEqual(f.objects.get(key), original);
});

for (const branch of ["test", "main"]) test(`${branch} rejects mismatched source environments before cloud access`, async t => {
  const f = fixture(t, branch);
  f.manifest.sourceBranch = branch === "test" ? "main" : "dev/v0.14.1";
  f.save();
  for (const execute of [false, true]) {
    await assert.rejects(publishPackages({...f, execute}), /Source branch does not match the COS environment/);
    assert.deepEqual(f.writes, []);
  }
});

for (const [variable, debug] of [["NODE_DEBUG", "request"], ["NODE_DEBUG", "http"], ["NODE_DEBUG", "*"], ["NODE_DEBUG", "REQUEST,https"], ["NODE_DEBUG_NATIVE", "http"], ["NODE_DEBUG", ""]]) test(`COS transport ${debug ? "refuses" : "allows"} ${variable}=${debug} before loading SDK`, () => {
  const code = `
    const Module = require("node:module");
    const original = Module._load;
    Module._load = function(name, ...args) {
      if (name === "cos-nodejs-sdk-v5") throw Error("SDK_LOAD_ATTEMPT");
      return original.call(this, name, ...args);
    };
    try { require(${JSON.stringify(path.join(__dirname, "../cos"))}).createStore({}); }
    catch (error) { console.error(error.message); process.exitCode = 1; }
  `;
  const env = {...process.env, NODE_DEBUG: "", NODE_DEBUG_NATIVE: "", [variable]: debug, COS_SECRET_ID: "unit-test-id", COS_SECRET_KEY: "unit-test-key", COS_SESSION_TOKEN: "unit-test-token"};
  const result = spawnSync(process.execPath, ["-e", code], {env, encoding: "utf8"});
  assert.equal(result.status, 1);
  if (debug) {
    assert.match(result.stderr, /Unset NODE_DEBUG/);
    assert.doesNotMatch(result.stderr, /SDK_LOAD_ATTEMPT/);
  } else assert.match(result.stderr, /SDK_LOAD_ATTEMPT/);
  assert.doesNotMatch(result.stdout + result.stderr, /unit-test-id|unit-test-key|unit-test-token|q-sign-algorithm/);
});

for (const branch of ["test", "main"]) for (const field of ["commit", "sourceRef"]) test(`${branch} programmatic publication validates ${field} before cloud access`, async t => {
  const f = fixture(t, branch);
  if (field === "commit") {
    execFileSync("git", ["checkout", "--orphan", "unrelated"], {cwd: f.repo, stdio: "pipe"});
    execFileSync("git", ["-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "unrelated"], {cwd: f.repo});
    f.manifest.commit = execFileSync("git", ["rev-parse", "HEAD"], {cwd: f.repo, encoding: "utf8"}).trim();
  } else f.manifest.sourceRef = "refs/heads/unrelated";
  f.save();
  await assert.rejects(publishPackages({...f, execute: true}), /git failed|source ref does not match/);
  assert.deepEqual(f.writes, []);
});

for (const kind of ["targets", "files"]) test(`npm manifest rejects comma-joined ${kind} keys before cloud access`, async t => {
  const f = fixture(t);
  if (kind === "targets") f.manifest.targets = {[TARGETS.slice().sort().join()]: f.manifest.targets["darwin/amd64"]};
  else {
    const asset = f.manifest.targets["darwin/amd64"];
    asset.files = {[Object.keys(asset.files).sort().join()]: "a".repeat(64)};
  }
  f.save();
  await assert.rejects(publishPackages({...f, execute: true}), /six platforms|file checksums/);
  assert.deepEqual(f.writes, []);
});
