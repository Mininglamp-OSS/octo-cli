#!/usr/bin/env node
"use strict";
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const {ROOT, project, readConfig, verifyDist, renderInstaller, sha256, download, compareVersions, checkVersion, sourceRef, run, parseArgs} = require("./lib");
function createStore(config, sdkConstructor) {
  const SecretId = process.env.COS_SECRET_ID;
  const SecretKey = process.env.COS_SECRET_KEY;
  if (!SecretId || !SecretKey || [SecretId, SecretKey].includes("replace-me")) throw new Error("Set COS_SECRET_ID and COS_SECRET_KEY in the local environment");
  const COS = sdkConstructor || require("cos-nodejs-sdk-v5");
  const cos = new COS({SecretId, SecretKey, SecurityToken: process.env.COS_SESSION_TOKEN, Protocol: "https:", Timeout: 120000});
  const base = {Bucket: config.bucket, Region: config.region};
  function call(method, parameters) {
    return new Promise((resolve, reject) => cos[method]({...base, ...parameters}, (error, data) => {
      if (!error) return resolve(data);
      // SDK errors may include signed request details. Never print the raw error.
      const safe = new Error(`COS ${method} failed (HTTP ${Number(error.statusCode) || "unknown"})`);
      safe.statusCode = Number(error.statusCode);
      if (error.code === "FileAlreadyExists") safe.code = error.code;
      reject(safe);
    }));
  }
  return {
    async get(key) {
      try { const r = await call("getObject", {Key: key}); return Buffer.isBuffer(r.Body) ? r.Body : Buffer.from(r.Body); }
      catch (e) { if (e.statusCode === 404) return null; throw e; }
    },
    async put(key, bytes, {immutable = false} = {}) {
      await call("putObject", {Key: key, Body: bytes, ContentLength: bytes.length,
        ContentType: key.endsWith(".json") ? "application/json" : key.endsWith(".js") ? "application/javascript" : "application/octet-stream",
        CacheControl: immutable && !key.includes("/.publish-") ? "public, max-age=31536000, immutable" : "no-store",
        Headers: immutable ? {"x-cos-forbid-overwrite": "true"} : {}});
    },
    async remove(key) { await call("deleteObject", {Key: key}); }
  };
}
async function assertConditionalCreation(store, root) {
  // Exercise the actual object-level guarantee without bucket-list/admin access.
  // Version-enabled buckets ignore forbid-overwrite and must fail this check.
  const key = `${root}/.publish-probe-${crypto.randomUUID()}`;
  const original = Buffer.from(crypto.randomUUID());
  const replacement = Buffer.from(crypto.randomUUID());
  await store.put(key, original, {immutable: true});
  try {
    let rejected = false;
    try { await store.put(key, replacement, {immutable: true}); }
    catch (e) {
      if (e.statusCode !== 409 || e.code !== "FileAlreadyExists") throw e;
      rejected = true;
    }
    if (!rejected) throw new Error("COS does not enforce conditional creation; publishing requires effective forbid-overwrite support");
    const bytes = await store.get(key);
    if (!bytes || !bytes.equals(original)) throw new Error("Conditional creation changed the existing probe contents");
  } finally {
    const bytes = await store.get(key);
    if (bytes && (bytes.equals(original) || bytes.equals(replacement))) await store.remove(key);
    else if (bytes) throw new Error(`Publish probe changed externally; inspect ${key}`);
  }
}
async function putImmutable(store, key, bytes) {
  const existing = await store.get(key);
  if (existing) {
    if (sha256(existing) !== sha256(bytes)) throw new Error(`Immutable object already has different contents: ${key}`);
    return;
  }
  await store.put(key, bytes, {immutable: true});
  const returned = await store.get(key);
  if (!returned || sha256(returned) !== sha256(bytes)) throw new Error(`COS read-back mismatch: ${key}`);
}
function installerSnapshot(dir, config, branch, persist) {
  const scriptPath = path.join(dir, "install.js");
  const recordPath = path.join(dir, "installer.local.json");
  const identity = {component: project.component, branch, baseUrl: `${config.cdnOrigin}/${config.prefixes[branch]}/`};
  if (fs.existsSync(scriptPath) || fs.existsSync(recordPath)) {
    if (!fs.existsSync(scriptPath) || !fs.existsSync(recordPath)) throw new Error("Incomplete installer snapshot; inspect the release directory before retrying");
    const bytes = fs.readFileSync(scriptPath);
    const record = JSON.parse(fs.readFileSync(recordPath, "utf8"));
    if (Object.entries(identity).some(([key, value]) => record[key] !== value) || record.sha256 !== sha256(bytes)) throw new Error("Saved installer snapshot does not match this release channel/origin");
    return bytes;
  }
  const bytes = Buffer.from(renderInstaller(config, branch));
  if (persist) {
    fs.writeFileSync(scriptPath, bytes, {flag: "wx"});
    fs.writeFileSync(recordPath, JSON.stringify({...identity, sha256: sha256(bytes)}, null, 2), {flag: "wx"});
  }
  return bytes;
}
async function publishRelease({dir, config, execute = false, allowRollback = false, store, fetcher = fetch}) {
  const manifest = verifyDist(dir);
  const prefix = config.prefixes[manifest.branch];
  const root = `${prefix}/${project.component}`;
  const versionRoot = `${root}/releases/${manifest.version}`;
  const scriptKey = project.component === "daemon" ? `${prefix}/install.js` : `${root}/install.js`;
  const installer = installerSnapshot(dir, config, manifest.branch, execute);
  const files = [...Object.values(manifest.targets).map(a => [a.file, fs.readFileSync(path.join(dir, a.file))]),
    ["checksums.txt", fs.readFileSync(path.join(dir, "checksums.txt"))],
    ["release.json", fs.readFileSync(path.join(dir, "release.json"))], ["install.js", installer]];
  const plan = {mode: execute ? "execute" : "dry-run", component: project.component, branch: manifest.branch, version: manifest.version,
    upload: files.map(([name]) => `${versionRoot}/${name}`), installer: scriptKey, promoteLast: `${root}/latest.json`};
  if (!execute) return plan;
  store ||= createStore(config);
  await assertConditionalCreation(store, root);
  const lockKey = `${root}/.publish-lock`;
  const owner = Buffer.from(JSON.stringify({owner: crypto.randomUUID(), createdAt: new Date().toISOString(), version: manifest.version}));
  await store.put(lockKey, owner, {immutable: true});
  let promoted = false;
  try {
    const prior = await store.get(`${root}/latest.json`);
    if (prior) {
      const latest = JSON.parse(prior.toString());
      if (latest.schemaVersion !== 1 || latest.component !== project.component || latest.branch !== manifest.branch) throw new Error("Existing latest has unexpected component/channel");
      checkVersion(latest.branch, latest.version);
      if (compareVersions(manifest.version, latest.version) < 0 && !allowRollback) throw new Error("Refusing to move latest backwards; use --allow-rollback explicitly");
    }
    for (const [name, bytes] of files) await putImmutable(store, `${versionRoot}/${name}`, bytes);
    // Verify exact immutable URLs through CDN before exposing a new pointer.
    for (const [name, bytes] of files) {
      const returned = await download(`${config.cdnOrigin}/${versionRoot}/${name}`, bytes.length, fetcher);
      if (sha256(returned) !== sha256(bytes)) throw new Error(`CDN mismatch: ${name}; latest remains unchanged`);
    }
    await store.put(scriptKey, installer);
    if (sha256(await download(`${config.cdnOrigin}/${scriptKey}`, installer.length, fetcher)) !== sha256(installer)) throw new Error("CDN installer is stale; refresh that URL and retry; latest remains unchanged");
    // Recheck pointer while holding the lock to detect external/older publishers.
    const current = await store.get(`${root}/latest.json`);
    if ((current?.toString() || "") !== (prior?.toString() || "")) throw new Error("latest changed outside the publish lock; refusing to promote");
    const latest = Buffer.from(JSON.stringify({schemaVersion: 1, component: project.component, branch: manifest.branch, version: manifest.version}) + "\n");
    await store.put(`${root}/latest.json`, latest); promoted = true;
    if (sha256(await download(`${config.cdnOrigin}/${root}/latest.json`, latest.length, fetcher)) !== sha256(latest)) throw new Error("CDN latest is stale");
    return plan;
  } catch (e) {
    if (promoted) throw new Error(`latest was written but final verification failed; inspect/refresh the CDN pointer before retrying: ${e.message}`);
    throw e;
  } finally {
    const currentOwner = await store.get(lockKey);
    if (currentOwner && currentOwner.equals(owner)) await store.remove(lockKey);
  }
}
async function main(argv = process.argv.slice(2)) {
  const args = parseArgs(argv, ["--dist", "--config"], ["--execute", "--allow-rollback", "--help"]);
  if (args["--help"]) { console.log("node scripts/release/publish-cos.js --dist release-dist/<version> [--config scripts/release/config.local.json] [--execute] [--allow-rollback]\nDefault: dry-run. Never changes npm or GitHub releases."); return; }
  if (!args["--dist"]) throw new Error("--dist is required");
  const config = readConfig(args["--config"], !!args["--execute"]);
  const dir = path.resolve(args["--dist"]);
  const m = verifyDist(dir);
  if (args["--execute"]) {
    const source = sourceRef(ROOT, m.branch);
    run("git", ["merge-base", "--is-ancestor", m.commit, source.commit], {cwd: ROOT});
    if (!m.tests || m.sourceRef !== source.ref) throw new Error("Missing build provenance; build with this repository's release script");
  }
  const plan = await publishRelease({dir, config, execute: !!args["--execute"], allowRollback: !!args["--allow-rollback"]});
  console.log(JSON.stringify(plan, null, 2));
}
module.exports = {publishRelease, putImmutable, createStore, assertConditionalCreation};
if (require.main === module) main().catch(e => { console.error(e.message); process.exitCode = 1; });
