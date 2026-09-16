#!/usr/bin/env node
"use strict";
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const {verifyNpm} = require("./build");
const {readConfig, parseArgs, sha256, download, ROOT, sourceRef, run, project} = require("./lib");
const {createStore, putImmutable, assertConditionalCreation} = require("./cos");
async function publishNpm({dir, config, execute = false, store, fetcher = fetch, warn = console.warn}) {
  const m = verifyNpm(dir);
  if (m.component !== project.component) throw new Error("Publish this component from its own repository");
  const root = `${config.prefixes[m.branch]}/${m.component}/npm`;
  const versionRoot = `${root}/releases/${m.version}`;
  const files = [...Object.values(m.targets).map(a => a.file), "npm-release.json"];
  const plan = {mode: execute ? "execute" : "dry-run", component: m.component, branch: m.branch, version: m.version, upload: files.map(f => `${versionRoot}/${f}`)};
  if (!execute) return plan;
  store ||= createStore(config);
  await assertConditionalCreation(store, root, warn);
  const key = `${root}/.publish-lock`;
  const owner = Buffer.from(crypto.randomUUID());
  await store.put(key, owner, {immutable: true});
  try {
    for (const file of files) await putImmutable(store, `${versionRoot}/${file}`, fs.readFileSync(path.join(dir, file)));
    for (const file of files) {
      const bytes = fs.readFileSync(path.join(dir, file));
      if (sha256(await download(`${config.cdnOrigin}/${versionRoot}/${file}`, bytes.length, fetcher)) !== sha256(bytes)) throw new Error(`CDN npm artifact mismatch: ${file}`);
    }
    return plan;
  } finally {
    // Cleanup must not change the publication result or replace its original error.
    try {
      const current = await store.get(key);
      if (current?.equals(owner)) await store.remove(key);
      else if (current) warn(`Warning: COS publish lock changed ownership; left ${key} untouched. Inspect it before retrying.`);
    } catch {
      warn(`Warning: Could not release the COS publish lock. Inspect ${key} and remove it only after confirming no publisher is active.`);
    }
  }
}
async function main(argv = process.argv.slice(2)) {
  const args = parseArgs(argv, ["--dist", "--config"], ["--execute", "--help"]);
  if (args["--help"]) {
    console.log("node scripts/release/publish.js --dist release-dist/<version>/npm [--config scripts/release/config.local.json] [--execute]\nDefault: dry-run. Uploads COS packages only; does not publish to npm or activate an installation.");
    return;
  }
  if (!args["--dist"]) throw new Error("--dist is required (directory containing npm-release.json)");
  const dir = path.resolve(args["--dist"]); const m = verifyNpm(dir);
  if (args["--execute"]) {
    const source = sourceRef(ROOT, m.branch);
    run("git", ["merge-base", "--is-ancestor", m.commit, source.commit], {cwd: ROOT});
    if (m.sourceRef !== source.ref) throw new Error(`npm source ref does not match this repository's ${m.branch} branch`);
  }
  console.log(JSON.stringify(await publishNpm({dir, config: readConfig(args["--config"], !!args["--execute"]), execute: !!args["--execute"]}), null, 2));
}
module.exports = {publishNpm};
if (require.main === module) main().catch(e => {console.error(e.message); process.exitCode = 1;});
