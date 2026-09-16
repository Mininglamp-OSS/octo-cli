#!/usr/bin/env node
"use strict";
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const YAML = require("yaml");
const {ROOT, project, TARGETS, run, sourceRef, checkVersion, parseArgs, sha256, assertManifest} = require("./lib");
function build(argv = process.argv.slice(2)) {
  const args = parseArgs(argv, ["--ref", "--version", "--out"], ["--help"]);
  if (args["--help"]) { console.log("node scripts/release/build.js --ref test|main --version X.Y.Z[-next.N] [--out directory]\nBuilds the exact origin branch commit (local branch fallback); never uploads."); return; }
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
    fs.mkdirSync(path.dirname(out), {recursive: true});
    // Reserve output atomically: concurrent builds must not merge into one version directory.
    fs.mkdirSync(out);
    try { fs.cpSync(stage, out, {recursive: true, force: false, errorOnExist: true}); }
    catch (error) { fs.rmSync(out, {recursive: true, force: true}); throw error; }
    console.log(`Built ${project.binary} ${version} from ${source.ref}@${source.commit}\n${out}`);
  } finally { fs.rmSync(temporary, {recursive: true, force: true}); }
}
module.exports = {build};
if (require.main === module) { try { build(); } catch (e) { console.error(e.message); process.exitCode = 1; } }
