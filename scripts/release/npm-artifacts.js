"use strict";
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const {execFileSync} = require("node:child_process");
const {TARGETS, sha256} = require("./runtime");
const {extractBinary} = require("./install");
const {assertNpmManifest, packageName} = require("./npm-runtime");
const LAUNCHER = `#!/usr/bin/env node
"use strict";
const path = require("node:path");
const {spawn} = require("node:child_process");
const pkg = require("../package.json");
if (pkg.os[0] !== process.platform || pkg.cpu[0] !== process.arch) throw new Error("Package platform mismatch; reinstall for this platform");
const binary = path.join(__dirname, "../vendor/", pkg.octoBinary);
const child = spawn(binary, process.argv.slice(2), {stdio: "inherit", env: process.env});
// Keep the wrapper alive until the native process has finished shutting down.
const signalHandlers = new Map(["SIGINT", "SIGTERM"].map(signal => [signal, () => child.kill(signal)]));
for (const [signal, handler] of signalHandlers) process.on(signal, handler);
function stopForwarding() {
  for (const [signal, handler] of signalHandlers) process.removeListener(signal, handler);
}
child.on("error", error => { stopForwarding(); console.error("Unable to start " + pkg.octoBinary + ": " + error.message); process.exitCode = 1; });
child.on("exit", (code, signal) => { stopForwarding(); if (signal) process.kill(process.pid, signal); else process.exitCode = code ?? 1; });
`;
function packNpm(dir, manifest, out) {
  if (fs.existsSync(out)) throw new Error("npm output already exists; reuse verified immutable artifacts or choose a new output");
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "octo-npm-pack-"));
  const result = {schemaVersion: 1, kind: "npm-release", component: manifest.component, name: packageName(manifest.component), branch: manifest.branch, version: manifest.version, commit: manifest.commit, sourceRef: manifest.sourceRef, targets: {}};
  try {
    const staged = path.join(temporary, "output"); fs.mkdirSync(staged);
    for (const target of TARGETS) {
      const [goos, goarch] = target.split("/");
      const platform = goos === "windows" ? "win32" : goos;
      const arch = goarch === "amd64" ? "x64" : goarch;
      const binary = `octo-${manifest.component}${platform === "win32" ? ".exe" : ""}`;
      const packageDir = path.join(temporary, `${goos}-${goarch}`, "package");
      fs.mkdirSync(path.join(packageDir, "bin"), {recursive: true}); fs.mkdirSync(path.join(packageDir, "vendor"));
      const pkg = {name: result.name, version: result.version, description: `Self-contained COS distribution of octo-${manifest.component}`, private: true, os: [platform], cpu: [arch], bin: {[`octo-${manifest.component}`]: "bin/run.js"}, engines: {node: ">=22"}, octoBinary: binary};
      fs.writeFileSync(path.join(packageDir, "package.json"), JSON.stringify(pkg, null, 2) + "\n");
      fs.writeFileSync(path.join(packageDir, "bin/run.js"), LAUNCHER, {mode: 0o755});
      fs.writeFileSync(path.join(packageDir, "vendor", binary), extractBinary(path.join(dir, manifest.targets[target].file), binary), {mode: 0o755});
      const files = Object.fromEntries(["package.json", "bin/run.js", `vendor/${binary}`].map(f => [f, sha256(fs.readFileSync(path.join(packageDir, f)))]));
      const file = `octo-${manifest.component}-${manifest.version}-${goos}-${goarch}.tgz`;
      execFileSync("tar", ["-czf", path.join(staged, file), "-C", path.dirname(packageDir), "package"], {env: {...process.env, COPYFILE_DISABLE: "1"}});
      const bytes = fs.readFileSync(path.join(staged, file));
      result.targets[target] = {file, size: bytes.length, sha256: sha256(bytes), files};
    }
    assertNpmManifest(result);
    fs.writeFileSync(path.join(staged, "npm-release.json"), JSON.stringify(result, null, 2) + "\n");
    fs.mkdirSync(path.dirname(out), {recursive: true});
    fs.mkdirSync(out);
    try { fs.cpSync(staged, out, {recursive: true, force: false, errorOnExist: true}); }
    catch (e) { fs.rmSync(out, {recursive: true, force: true}); throw e; }
    return result;
  } finally { fs.rmSync(temporary, {recursive: true, force: true}); }
}
function verifyNpm(dir) {
  const m = assertNpmManifest(JSON.parse(fs.readFileSync(path.join(dir, "npm-release.json"), "utf8")));
  for (const a of Object.values(m.targets)) {
    const file = path.join(dir, a.file);
    if (!fs.lstatSync(file).isFile()) throw new Error("npm artifact must be a regular file");
    const bytes = fs.readFileSync(file);
    if (bytes.length !== a.size || sha256(bytes) !== a.sha256) throw new Error("npm artifact checksum mismatch");
  }
  return m;
}
module.exports = {packNpm, verifyNpm, assertNpmManifest, packageName};
if (require.main === module) {
  try {
    const {parseArgs, verifyDist} = require("./lib");
    const args = parseArgs(process.argv.slice(2), ["--dist", "--out"]);
    if (!args["--dist"]) throw new Error("--dist is required");
    const dir = path.resolve(args["--dist"]);
    const out = path.resolve(args["--out"] || path.join(dir, "npm"));
    const m = packNpm(dir, verifyDist(dir), out);
    console.log(`Packed ${m.name}@${m.version}: six self-contained npm packages\n${out}`);
  } catch (e) { console.error(e.message); process.exitCode = 1; }
}
