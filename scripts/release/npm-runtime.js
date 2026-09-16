"use strict";
const {TARGETS, checkVersion, fileName} = require("./runtime");
function packageName(component) {
  if (!["cli", "daemon"].includes(component)) throw new Error("Unknown component");
  return `@mininglamp-oss/octo-${component}`;
}
function assertNpmManifest(m) {
  if (!m || m.schemaVersion !== 1 || m.kind !== "npm-release" || m.name !== packageName(m.component)) throw new Error("Invalid npm release identity");
  if (checkVersion(m.branch, m.version) !== m.version || !/^[a-f0-9]{40}$/.test(m.commit || "")) throw new Error("Invalid npm provenance");
  if (Object.keys(m.targets || {}).sort().join() !== [...TARGETS].sort().join()) throw new Error("npm release requires six platforms");
  for (const [target, asset] of Object.entries(m.targets)) {
    fileName(asset.file);
    if (!asset.file.endsWith(".tgz") || !Number.isSafeInteger(asset.size) || asset.size <= 0 || asset.size > 512 * 1024 * 1024 || !/^[a-f0-9]{64}$/.test(asset.sha256)) throw new Error("Invalid npm artifact");
    const binary = `octo-${m.component}${target.startsWith("windows/") ? ".exe" : ""}`;
    const keys = ["package.json", "bin/run.js", `vendor/${binary}`].sort();
    if (Object.keys(asset.files || {}).sort().join() !== keys.join() || Object.values(asset.files).some(h => !/^[a-f0-9]{64}$/.test(h))) throw new Error("Invalid npm file checksums");
  }
  return m;
}
module.exports = {assertNpmManifest, packageName};
