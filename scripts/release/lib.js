"use strict";
const fs = require("node:fs");
const path = require("node:path");
const {spawnSync} = require("node:child_process");
const runtime = require("./runtime");
const project = require("./project.json");
const ROOT = path.resolve(__dirname, "../..");
function run(command, args, options = {}) {
  const result = spawnSync(command, args, {encoding: "utf8", ...options});
  if (result.error) throw new Error(`${command}: ${result.error.message}`);
  if (result.status !== 0) throw new Error(`${command} failed (${result.status}): ${(result.stderr || "").slice(-2000)}`);
  return (result.stdout || "").trim();
}
function readConfig(file = path.join(__dirname, "config.local.json"), execute = false) {
  const c = JSON.parse(fs.readFileSync(file, "utf8"));
  if (Object.keys(c).some(k => !["bucket", "region", "cdnOrigin", "prefixes"].includes(k))) throw new Error("Unknown configuration key; credentials belong in environment variables only");
  if (!/^[a-z0-9-]+-\d+$/.test(c.bucket || "") || !/^[a-z]+-[a-z0-9-]+$/.test(c.region || "")) throw new Error("Invalid bucket/region");
  const u = new URL(c.cdnOrigin);
  if (u.protocol !== "https:" || u.username || u.password || u.search || u.hash || u.pathname !== "/") throw new Error("cdnOrigin must be a plain HTTPS origin");
  c.cdnOrigin = u.origin;
  if (!c.prefixes || Object.keys(c.prefixes).sort().join() !== "main,test") throw new Error("Configure exactly main/test prefixes");
  for (const prefix of Object.values(c.prefixes)) {
    if (typeof prefix !== "string" || !/^[a-zA-Z0-9_-]+(?:\/[a-zA-Z0-9_-]+)*$/.test(prefix)) throw new Error("Invalid object prefix");
  }
  if (c.prefixes.main === c.prefixes.test || c.prefixes.main.startsWith(c.prefixes.test + "/") || c.prefixes.test.startsWith(c.prefixes.main + "/")) throw new Error("Environment prefixes must be disjoint");
  if (execute && /replace-me|example\.(com|org|net)/.test(JSON.stringify(c))) throw new Error("Replace example configuration before uploading");
  return c;
}
function sourceRef(repo, branch) {
  if (!["main", "test"].includes(branch)) throw new Error("--ref must be main or test");
  for (const ref of [`refs/remotes/origin/${branch}`, `refs/heads/${branch}`]) {
    const result = spawnSync("git", ["rev-parse", "--verify", `${ref}^{commit}`], {cwd: repo, encoding: "utf8"});
    if (result.status === 0) return {branch, ref, commit: result.stdout.trim()};
  }
  throw new Error(`Branch ${branch} is not available; fetch/create the intended branch explicitly first`);
}
function assertManifest(m) { return runtime.assertManifest(m, project.component); }
function verifyDist(dir) {
  const manifest = assertManifest(JSON.parse(fs.readFileSync(path.join(dir, "release.json"), "utf8")));
  const sums = fs.readFileSync(path.join(dir, "checksums.txt"), "utf8");
  for (const asset of Object.values(manifest.targets)) {
    const file = path.join(dir, asset.file);
    if (!fs.lstatSync(file).isFile()) throw new Error("Artifact must be a regular file");
    const bytes = fs.readFileSync(file);
    if (bytes.length !== asset.size || runtime.sha256(bytes) !== asset.sha256) throw new Error(`Artifact mismatch: ${asset.file}`);
    if (!sums.split(/\r?\n/).includes(`${asset.sha256}  ${asset.file}`)) throw new Error(`Missing checksum entry: ${asset.file}`);
  }
  return manifest;
}
function renderInstaller(config, branch) {
  const settings = {component: project.component, binary: project.binary, branch, baseUrl: `${config.cdnOrigin}/${config.prefixes[branch]}/`};
  const runtimeSource = fs.readFileSync(path.join(__dirname, "runtime.js"), "utf8");
  return fs.readFileSync(path.join(__dirname, "install.js"), "utf8")
    .replace('require("./runtime")', `(() => { const module = {exports: {}};\n${runtimeSource}\nreturn module.exports; })()`)
    .replace("/* RELEASE_SETTINGS */ null", JSON.stringify(settings))
    .replace("if (require.main === module)", "if (true)");
}
module.exports = {...runtime, project, ROOT, run, readConfig, sourceRef, assertManifest, verifyDist, renderInstaller};
