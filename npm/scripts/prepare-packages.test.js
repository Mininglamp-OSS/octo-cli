"use strict";

const test = require("node:test");
const assert = require("node:assert");
const fs = require("fs");
const os = require("os");
const path = require("path");
const { execFileSync, spawnSync } = require("child_process");

const SCRIPT = path.join(__dirname, "prepare-packages.js");
const SHIM = path.join(__dirname, "..", "bin", "octo-cli.js");
const GORELEASER_YAML = path.join(__dirname, "..", "..", ".goreleaser.yaml");
const { PLATFORMS, binFileName } = require(SCRIPT);
const shim = require(SHIM);
const VERSION = "9.9.9";

function makeDist(distDir) {
  fs.mkdirSync(distDir, { recursive: true });
  for (const { goOs, goArch } of PLATFORMS) {
    const stage = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-stage-"));
    const bin = binFileName(goOs);
    fs.writeFileSync(path.join(stage, bin), `#!/bin/sh\necho fake ${goOs}/${goArch} "$@"\n`, {
      mode: 0o755,
    });
    execFileSync("tar", [
      "-czf",
      path.join(distDir, `octo-cli_${VERSION}_${goOs}_${goArch}.tar.gz`),
      "-C",
      stage,
      bin,
    ]);
    fs.rmSync(stage, { recursive: true, force: true });
  }
}

function run(args) {
  return spawnSync(process.execPath, [SCRIPT, ...args], { encoding: "utf8" });
}

test("npm matrix matches .goreleaser.yaml goos x goarch", () => {
  const yaml = fs.readFileSync(GORELEASER_YAML, "utf8");
  const section = (name) => {
    const m = yaml.match(new RegExp(`${name}:\\n((?:\\s*(?:#[^\\n]*)?\\n)*(?:\\s+-\\s+\\S+\\n)+)`));
    assert.ok(m, `cannot find ${name}: list in .goreleaser.yaml`);
    return [...m[1].matchAll(/-\s+(\S+)/g)].map((x) => x[1]);
  };
  const goos = section("goos");
  const goarch = section("goarch");
  const releaser = new Set(goos.flatMap((o) => goarch.map((a) => `${o}/${a}`)));
  const npm = new Set(PLATFORMS.map((p) => `${p.goOs}/${p.goArch}`));
  assert.deepStrictEqual([...npm].sort(), [...releaser].sort());
});

test("emits one package per platform and a pinned main package", (t) => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-prep-"));
  t.after(() => fs.rmSync(tmp, { recursive: true, force: true }));
  const distDir = path.join(tmp, "dist");
  const outDir = path.join(tmp, "out");
  makeDist(distDir);

  const res = run(["--version", VERSION, "--dist", distDir, "--out", outDir]);
  assert.strictEqual(res.status, 0, res.stderr);

  for (const { goOs, npmOs, npmCpu } of PLATFORMS) {
    const pkgDir = path.join(outDir, `octo-cli-${npmOs}-${npmCpu}`);
    const manifest = JSON.parse(fs.readFileSync(path.join(pkgDir, "package.json"), "utf8"));
    assert.strictEqual(manifest.name, `@mininglamp-oss/octo-cli-${npmOs}-${npmCpu}`);
    assert.strictEqual(manifest.version, VERSION);
    assert.deepStrictEqual(manifest.os, [npmOs]);
    assert.deepStrictEqual(manifest.cpu, [npmCpu]);
    assert.deepStrictEqual(manifest.files, [`bin/${binFileName(goOs)}`, "LICENSE"]);
    const bin = path.join(pkgDir, "bin", binFileName(goOs));
    assert.ok(fs.existsSync(bin), `missing ${bin}`);
    assert.ok(fs.statSync(bin).mode & 0o100, `${bin} not executable`);
    assert.ok(fs.existsSync(path.join(pkgDir, "LICENSE")), `missing ${pkgDir}/LICENSE`);
  }

  const main = JSON.parse(fs.readFileSync(path.join(outDir, "octo-cli", "package.json"), "utf8"));
  assert.strictEqual(main.name, "@mininglamp-oss/octo-cli");
  assert.strictEqual(main.version, VERSION);
  assert.strictEqual(main.scripts, undefined);
  assert.strictEqual(main.os, undefined);
  assert.strictEqual(main.cpu, undefined);
  assert.deepStrictEqual(
    Object.keys(main.optionalDependencies).sort(),
    PLATFORMS.map((p) => `@mininglamp-oss/octo-cli-${p.npmOs}-${p.npmCpu}`).sort(),
  );
  for (const depVersion of Object.values(main.optionalDependencies)) {
    assert.strictEqual(depVersion, VERSION);
  }
  assert.ok(fs.existsSync(path.join(outDir, "octo-cli", "bin", "octo-cli.js")));
});

test("rejects invalid versions and missing archives", (t) => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-prep-"));
  t.after(() => fs.rmSync(tmp, { recursive: true, force: true }));
  fs.mkdirSync(path.join(tmp, "dist"));

  const badVersion = run(["--version", "v1.2.3", "--dist", path.join(tmp, "dist"), "--out", path.join(tmp, "out")]);
  assert.notStrictEqual(badVersion.status, 0);
  assert.match(badVersion.stderr, /--version must be bare semver/);

  const missing = run(["--version", VERSION, "--dist", path.join(tmp, "dist"), "--out", path.join(tmp, "out")]);
  assert.notStrictEqual(missing.status, 0);
  assert.match(missing.stderr, /missing release archive/);
});

test("rejects unknown and duplicate arguments before touching output paths", (t) => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-prep-"));
  t.after(() => fs.rmSync(tmp, { recursive: true, force: true }));
  fs.mkdirSync(path.join(tmp, "dist"));

  const unknown = run([
    "--version",
    VERSION,
    "--dist",
    path.join(tmp, "dist"),
    "--out",
    path.join(tmp, "out"),
    "--cache",
    path.join(tmp, "cache"),
  ]);
  assert.notStrictEqual(unknown.status, 0);
  assert.match(unknown.stderr, /unknown argument: --cache/);

  const duplicate = run([
    "--version",
    VERSION,
    "--dist",
    path.join(tmp, "dist"),
    "--out",
    path.join(tmp, "out"),
    "--out",
    path.join(tmp, "other-out"),
  ]);
  assert.notStrictEqual(duplicate.status, 0);
  assert.match(duplicate.stderr, /duplicate argument: --out/);
});

test("rejects archive members that are not regular files", (t) => {
  if (process.platform === "win32") {
    return;
  }

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-prep-"));
  t.after(() => fs.rmSync(tmp, { recursive: true, force: true }));
  const distDir = path.join(tmp, "dist");
  const outDir = path.join(tmp, "out");
  fs.mkdirSync(distDir, { recursive: true });

  const first = PLATFORMS[0];
  const stage = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-symlink-"));
  fs.symlinkSync("/etc/passwd", path.join(stage, binFileName(first.goOs)));
  execFileSync("tar", [
    "-czf",
    path.join(distDir, `octo-cli_${VERSION}_${first.goOs}_${first.goArch}.tar.gz`),
    "-C",
    stage,
    binFileName(first.goOs),
  ]);
  fs.rmSync(stage, { recursive: true, force: true });

  const res = run(["--version", VERSION, "--dist", distDir, "--out", outDir]);
  assert.notStrictEqual(res.status, 0);
  assert.match(res.stderr, /non-regular octo-cli/);
});

test("shim resolves the installed platform package and propagates output", (t) => {
  if (process.platform === "win32") {
    return;
  }

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-shim-"));
  t.after(() => fs.rmSync(tmp, { recursive: true, force: true }));

  const scopedPkg = shim.platformPackageFor(process.platform, process.arch);
  assert.ok(scopedPkg, `test host ${process.platform}-${process.arch} must be part of the supported matrix`);
  const pkg = scopedPkg.replace("@mininglamp-oss/", "");

  const pkgDir = path.join(tmp, "node_modules", "@mininglamp-oss", pkg, "bin");
  fs.mkdirSync(pkgDir, { recursive: true });
  const bin = path.join(pkgDir, "octo-cli");
  fs.writeFileSync(bin, "#!/bin/sh\necho shim-ok \"$@\"\n", { mode: 0o755 });

  const res = spawnSync(process.execPath, [SHIM, "--ping"], {
    cwd: tmp,
    env: { ...process.env, NODE_PATH: path.join(tmp, "node_modules") },
    encoding: "utf8",
  });
  assert.strictEqual(res.status, 0, res.stderr);
  assert.strictEqual(res.stdout.trim(), "shim-ok --ping");
});

test("shim resolver maps windows packages to the .exe binary", () => {
  const resolved = shim.resolveBinary("win32", "x64", (specifier) => specifier);
  assert.strictEqual(resolved, "@mininglamp-oss/octo-cli-win32-x64/bin/octo-cli.exe");
  assert.strictEqual(shim.binaryNameFor("win32"), "octo-cli.exe");
  assert.strictEqual(shim.binaryNameFor("linux"), "octo-cli");
});

test("shim reports a missing platform package", (t) => {
  const scopedPkg = shim.platformPackageFor(process.platform, process.arch);
  assert.ok(scopedPkg, `test host ${process.platform}-${process.arch} must be part of the supported matrix`);

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-shim-missing-"));
  t.after(() => fs.rmSync(tmp, { recursive: true, force: true }));
  fs.mkdirSync(path.join(tmp, "node_modules"), { recursive: true });

  const res = spawnSync(process.execPath, [SHIM, "--ping"], {
    cwd: tmp,
    env: { ...process.env, NODE_PATH: path.join(tmp, "node_modules") },
    encoding: "utf8",
  });
  assert.strictEqual(res.status, 1);
  assert.match(res.stderr, new RegExp(`platform package ${scopedPkg} is not installed`));
  assert.match(res.stderr, /Try reinstalling: npm install -g @mininglamp-oss\/octo-cli/);
});
