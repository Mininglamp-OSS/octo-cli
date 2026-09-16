"use strict";
const crypto = require("node:crypto");
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
function compareVersions(a, b) {
  const x = normalizeVersion(a).match(VERSION).slice(1);
  const y = normalizeVersion(b).match(VERSION).slice(1);
  for (let i = 0; i < 4; i++) {
    if (x[i] === y[i]) continue;
    if (i === 3 && (x[i] === undefined || y[i] === undefined)) return x[i] === undefined ? 1 : -1;
    return BigInt(x[i]) > BigInt(y[i]) ? 1 : -1;
  }
  return 0;
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
function assertManifest(m, component) {
  if (!m || m.schemaVersion !== 1 || m.component !== component) throw new Error("Unexpected release schema/component");
  if (checkVersion(m.branch, m.version) !== m.version) throw new Error("Manifest version must not contain v prefix");
  if (!/^[a-f0-9]{40}$/.test(m.commit || "")) throw new Error("Missing full source commit");
  if (!m.targets || Object.keys(m.targets).sort().join() !== [...TARGETS].sort().join()) throw new Error("Release must contain exactly six platforms");
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
module.exports = {TARGETS, normalizeVersion, checkVersion, compareVersions, targetName, sha256, fileName, assertManifest, parseArgs, download};
