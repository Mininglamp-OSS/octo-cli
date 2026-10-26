"use strict";
const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const {spawnSync} = require("node:child_process");
const project = {component: "cli", binary: "octo-cli", goreleaser: ".goreleaser.yaml"};
const ROOT = path.resolve(__dirname, "../..");
const VERSION = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-next\.(0|[1-9]\d*))?$/;
const TARGETS = ["darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"];
function normalizeVersion(value) {
  const version = String(value || "").replace(/^v/, "");
  if (!VERSION.test(version)) throw new Error("Version must be X.Y.Z or X.Y.Z-next.N (no leading zeroes)");
  return version;
}
function checkVersion(branch, value) {
  const version = normalizeVersion(value);
  if (!["test", "main"].includes(branch)) throw new Error("Only test/main branches may publish");
  if ((branch === "test") !== version.includes("-next.")) throw new Error("test requires next.N; main requires a stable version");
  return version;
}
function targetName(platform, arch) {
  const target = `${platform === "win32" ? "windows" : platform}/${arch === "x64" ? "amd64" : arch}`;
  if (!TARGETS.includes(target)) throw new Error(`Unsupported platform: ${platform}/${arch}`);
  return target;
}
function sha256(bytes) { return crypto.createHash("sha256").update(bytes).digest("hex"); }
function fileName(value) {
  if (typeof value !== "string" || !/^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(value) || value.includes("..")) throw new Error("Unsafe artifact filename");
  return value;
}
function hasExactKeys(value, keys) {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    && Object.keys(value).length === keys.length && keys.every(key => Object.hasOwn(value, key));
}
function assertManifest(m, component = project.component) {
  if (!m || m.schemaVersion !== 1 || m.component !== component) throw new Error("Unexpected release schema/component");
  if (checkVersion(m.branch, m.version) !== m.version) throw new Error("Manifest version must not contain v prefix");
  if (!/^[a-f0-9]{40}$/.test(m.commit || "")) throw new Error("Missing full source commit");
  if (!hasExactKeys(m.targets, TARGETS)) throw new Error("Release must contain exactly six platforms");
  const files = new Set();
  for (const asset of Object.values(m.targets)) {
    fileName(asset.file);
    if (files.has(asset.file)) throw new Error("Duplicate artifact filename");
    files.add(asset.file);
    if (!/^[a-f0-9]{64}$/.test(asset.sha256 || "") || !Number.isSafeInteger(asset.size) || asset.size <= 0 || asset.size > 512 * 1024 * 1024) throw new Error("Invalid artifact checksum/size");
    if (!["tar.gz", "zip"].includes(asset.format) || !asset.file.endsWith(`.${asset.format}`)) throw new Error("Invalid archive format");
  }
  return m;
}
function parseArgs(argv, values, switches = []) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    const key = argv[i];
    if (Object.hasOwn(out, key) || (!values.includes(key) && !switches.includes(key))) throw new Error(`Unknown or duplicate option: ${key}`);
    if (switches.includes(key)) out[key] = true;
    else {
      if (!argv[i + 1] || argv[i + 1].startsWith("--")) throw new Error(`Missing value for ${key}`);
      out[key] = argv[++i];
    }
  }
  return out;
}
async function download(url, maxBytes = 1024 * 1024, fetcher = fetch) {
  if (new URL(url).protocol !== "https:") throw new Error("Downloads require HTTPS");
  let last;
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      const response = await fetcher(url, {redirect: "manual", signal: AbortSignal.timeout(120000)});
      if (response.status !== 200) throw new Error(`Download returned HTTP ${response.status} (redirects are not allowed)`);
      const chunks = []; let total = 0;
      for await (const chunk of response.body) {
        total += chunk.length;
        if (total > maxBytes) throw new Error("Download exceeds allowed size");
        chunks.push(Buffer.from(chunk));
      }
      return Buffer.concat(chunks);
    } catch (error) { last = error; }
  }
  throw last;
}
function withoutCosCredentials(env = process.env) {
  return Object.fromEntries(Object.entries(env).filter(([key]) => !/^COS_/i.test(key)));
}
function run(command, args, options = {}) {
  const result = spawnSync(command, args, {encoding: "utf8", ...options, env: withoutCosCredentials(options.env)});
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
  if (!hasExactKeys(c.prefixes, ["main", "test"])) throw new Error("Configure exactly main/test prefixes");
  for (const prefix of Object.values(c.prefixes)) {
    if (typeof prefix !== "string" || !/^[a-zA-Z0-9_-]+(?:\/[a-zA-Z0-9_-]+)*$/.test(prefix)) throw new Error("Invalid object prefix");
  }
  if (c.prefixes.main === c.prefixes.test || c.prefixes.main.startsWith(c.prefixes.test + "/") || c.prefixes.test.startsWith(c.prefixes.main + "/")) throw new Error("Environment prefixes must be disjoint");
  if (execute && /replace-me|example\.(com|org|net)/.test(JSON.stringify(c))) throw new Error("Replace example configuration before uploading");
  return c;
}
function releaseChannel(branch) {
  if (branch === "main") return "main";
  if (typeof branch === "string" && /^dev\/v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(branch)) return "test";
  throw new Error("--ref must be main or dev/vX.Y.Z");
}
function sourceRef(repo, branch) {
  const channel = releaseChannel(branch);
  for (const ref of [`refs/remotes/origin/${branch}`, `refs/heads/${branch}`]) {
    const result = spawnSync("git", ["rev-parse", "--verify", `${ref}^{commit}`], {cwd: repo, encoding: "utf8", env: withoutCosCredentials()});
    if (result.status === 0) return {branch, channel, ref, commit: result.stdout.trim()};
  }
  throw new Error(`Branch ${branch} is not available; fetch/create the intended branch explicitly first`);
}
function verifyDist(dir) {
  const manifest = assertManifest(JSON.parse(fs.readFileSync(path.join(dir, "release.json"), "utf8")));
  const sums = fs.readFileSync(path.join(dir, "checksums.txt"), "utf8");
  for (const asset of Object.values(manifest.targets)) {
    const file = path.join(dir, asset.file);
    if (!fs.lstatSync(file).isFile()) throw new Error("Artifact must be a regular file");
    const bytes = fs.readFileSync(file);
    if (bytes.length !== asset.size || sha256(bytes) !== asset.sha256) throw new Error(`Artifact mismatch: ${asset.file}`);
    if (!sums.split(/\r?\n/).includes(`${asset.sha256}  ${asset.file}`)) throw new Error(`Missing checksum entry: ${asset.file}`);
  }
  return manifest;
}
function packageName(component) {
  if (!["cli", "daemon"].includes(component)) throw new Error("Unknown component");
  return `@mininglamp-oss/octo-${component}`;
}
function assertNpmManifest(m) {
  if (!m || m.schemaVersion !== 1 || m.kind !== "npm-release" || m.name !== packageName(m.component)) throw new Error("Invalid npm release identity");
  if (checkVersion(m.branch, m.version) !== m.version || !/^[a-f0-9]{40}$/.test(m.commit || "")) throw new Error("Invalid npm provenance");
  if (m.sourceBranch !== undefined && releaseChannel(m.sourceBranch) !== m.branch) throw new Error("Source branch does not match the COS environment");
  if (!hasExactKeys(m.targets, TARGETS)) throw new Error("npm release requires six platforms");
  for (const [target, asset] of Object.entries(m.targets)) {
    fileName(asset.file);
    if (!asset.file.endsWith(".tgz") || !Number.isSafeInteger(asset.size) || asset.size <= 0 || asset.size > 512 * 1024 * 1024 || !/^[a-f0-9]{64}$/.test(asset.sha256)) throw new Error("Invalid npm artifact");
    const binary = `octo-${m.component}${target.startsWith("windows/") ? ".exe" : ""}`;
    const keys = ["package.json", "bin/run.js", `vendor/${binary}`];
    if (!hasExactKeys(asset.files, keys) || Object.values(asset.files).some(h => !/^[a-f0-9]{64}$/.test(h))) throw new Error("Invalid npm file checksums");
  }
  return m;
}
module.exports = {TARGETS, normalizeVersion, checkVersion, targetName, sha256, fileName, assertManifest, parseArgs, download, project, ROOT, run, withoutCosCredentials, readConfig, sourceRef, verifyDist, assertNpmManifest, packageName};
