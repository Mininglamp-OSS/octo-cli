#!/usr/bin/env node
"use strict";
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const {execFileSync} = require("node:child_process");
const {ROOT, project, TARGETS, run, withoutCosCredentials, sourceRef, checkVersion, parseArgs, sha256, assertManifest, targetName, assertNpmManifest, packageName, verifyDist} = require("./lib");
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
      execFileSync("tar", ["-czf", path.join(staged, file), "-C", path.dirname(packageDir), "package"], {env: {...withoutCosCredentials(), COPYFILE_DISABLE: "1"}});
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
function verifyNpm(dir, manifestBytes = fs.readFileSync(path.join(dir, "npm-release.json"))) {
  const m = assertNpmManifest(JSON.parse(manifestBytes.toString()));
  for (const a of Object.values(m.targets)) {
    const file = path.join(dir, a.file);
    if (!fs.lstatSync(file).isFile()) throw new Error("npm artifact must be a regular file");
    const bytes = fs.readFileSync(file);
    if (bytes.length !== a.size || sha256(bytes) !== a.sha256) throw new Error("npm artifact checksum mismatch");
  }
  return m;
}
function extractBinary(archive, binary) {
  // Read only the exact regular root entry; never unpack arbitrary archive paths.
  if (!/^octo-(daemon|cli)(\.exe)?$/.test(binary)) throw new Error("Invalid archive binary name");
  const options = {maxBuffer: 512 * 1024 * 1024, timeout: 30000, env: withoutCosCredentials()};
  const entries = execFileSync("tar", ["-tf", archive], {...options, encoding: "utf8"}).trim().split(/\r?\n/);
  if (entries.filter(entry => entry === binary).length !== 1) throw new Error("Archive must contain exactly one root binary");
  const detail = execFileSync("tar", ["-tvf", archive, binary], {...options, encoding: "utf8"});
  if (!detail.startsWith("-")) throw new Error("Archive binary must be a regular file");
  return execFileSync("tar", ["-xOf", archive, binary], options);
}
function smokeDist(dir) {
  const manifest = verifyDist(dir);
  const asset = manifest.targets[targetName(process.platform, process.arch)];
  const binary = `octo-${manifest.component}${process.platform === "win32" ? ".exe" : ""}`;
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "octo-release-smoke-"));
  try {
    const candidate = path.join(temporary, binary);
    fs.writeFileSync(candidate, extractBinary(path.join(dir, asset.file), binary), {mode: 0o755});
    const output = execFileSync(candidate, ["version", "--format", "json"], {encoding: "utf8", timeout: 30000, env: withoutCosCredentials()});
    let result;
    try { result = JSON.parse(output); }
    catch { throw new Error("Host CLI version probe did not return valid JSON"); }
    const reported = result.data?.version ?? result.version;
    if (reported !== manifest.version) throw new Error("Host binary reports an unexpected version");
    return manifest;
  } finally { fs.rmSync(temporary, {recursive: true, force: true}); }
}

function build(argv = process.argv.slice(2)) {
  const args = parseArgs(argv, ["--ref", "--version", "--out"], ["--help"]);
  if (args["--help"]) { console.log("node scripts/release/build.js --ref test|main --version X.Y.Z[-next.N] [--out directory]\nBuilds and verifies six self-contained COS packages in <out>/npm from the exact branch commit; never uploads."); return; }
  const YAML = require("yaml");
  const source = sourceRef(ROOT, args["--ref"]);
  const version = checkVersion(source.branch, args["--version"]);
  const out = path.resolve(args["--out"] || path.join(ROOT, "release-dist", version));
  if (fs.existsSync(out)) throw new Error("Output directory already exists; choose a new empty location");
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), `${project.binary}-release-`));
  try {
    const checkout = path.join(temporary, "source");
    run("git", ["clone", "--quiet", "--shared", "--no-checkout", ROOT, checkout]);
    run("git", ["checkout", "--quiet", "--detach", source.commit], {cwd: checkout});
    // Work from committed source only; caller changes and local configuration are never copied.
    run("go", ["test", "./..."], {cwd: checkout, stdio: "inherit"});
    if (fs.existsSync(path.join(checkout, "npm/scripts/prepare-packages.test.js"))) {
      run(process.execPath, ["--test", "npm/scripts/prepare-packages.test.js"], {cwd: checkout, stdio: "inherit"});
    }
    const original = YAML.parse(fs.readFileSync(path.join(checkout, project.goreleaser), "utf8"));
    if (original.builds.length !== 1) throw new Error("Review release tooling before introducing multiple GoReleaser builds");
    const b = original.builds[0];
    const matrix = b.goos.flatMap(o => b.goarch.map(a => `${o}/${a}`)).sort();
    if (matrix.join() !== [...TARGETS].sort().join()) throw new Error("GoReleaser platform matrix changed; review release contract");
    // Only build/archive/checksum settings are reused. Never execute remote publishers or hooks.
    const output = path.join(temporary, "dist");
    const config = {version: 2, project_name: original.project_name, builds: original.builds, archives: original.archives,
      checksum: {name_template: "checksums.txt", algorithm: "sha256"}, snapshot: {version_template: version}, dist: output};
    if (b.hooks) throw new Error("Build hooks require review before local release execution");
    const configFile = path.join(temporary, "goreleaser.yml");
    fs.writeFileSync(configFile, YAML.stringify(config));
    run("goreleaser", ["release", "--snapshot", "--config", configFile, "--parallelism", "4"], {cwd: checkout, stdio: "inherit"});
    const artifacts = JSON.parse(fs.readFileSync(path.join(output, "artifacts.json"), "utf8"));
    const targets = {};
    for (const a of artifacts.filter(a => a.type === "Archive")) {
      const key = `${a.goos}/${a.goarch}`;
      if (!TARGETS.includes(key) || targets[key]) throw new Error("Unexpected GoReleaser archive matrix");
      const bytes = fs.readFileSync(a.path);
      targets[key] = {file: a.name, size: bytes.length, sha256: sha256(bytes), format: a.name.endsWith(".zip") ? "zip" : "tar.gz"};
    }
    const manifest = assertManifest({schemaVersion: 1, component: project.component, branch: source.branch, version,
      commit: source.commit, sourceRef: source.ref, builtAt: new Date().toISOString(), tests: "go test ./...", targets});
    const stage = path.join(temporary, "release"); fs.mkdirSync(stage);
    for (const a of Object.values(targets)) fs.copyFileSync(path.join(output, a.file), path.join(stage, a.file));
    fs.writeFileSync(path.join(stage, "release.json"), JSON.stringify(manifest, null, 2) + "\n");
    fs.writeFileSync(path.join(stage, "checksums.txt"), Object.values(targets).map(a => `${a.sha256}  ${a.file}\n`).join(""));
    smokeDist(stage);
    packNpm(stage, manifest, path.join(stage, "npm"));
    verifyNpm(path.join(stage, "npm"));
    fs.mkdirSync(path.dirname(out), {recursive: true});
    // Reserve output atomically: concurrent builds must not merge into one version directory.
    fs.mkdirSync(out);
    try { fs.cpSync(stage, out, {recursive: true, force: false, errorOnExist: true}); }
    catch (error) { fs.rmSync(out, {recursive: true, force: true}); throw error; }
    console.log(`Built and verified ${project.binary} ${version} from ${source.ref}@${source.commit}\nSix COS packages: ${path.join(out, "npm")}\nHost version smoke test passed; no installation changed.`);
  } finally { fs.rmSync(temporary, {recursive: true, force: true}); }
}
module.exports = {build, packNpm, verifyNpm, extractBinary, smokeDist};
if (require.main === module) { try { build(); } catch (e) { console.error(e.message); process.exitCode = 1; } }
