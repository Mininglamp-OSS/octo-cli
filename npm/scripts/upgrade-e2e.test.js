"use strict";

// Explicit integration suite: real managers, registry tarballs and Go executables.
// Run separately from the fast npm test suite; never uses the operator's globals.
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const http = require("node:http");
const crypto = require("node:crypto");
const { spawn } = require("node:child_process");
const { PLATFORMS, binFileName } = require("./prepare-packages.js");

const ROOT = path.resolve(__dirname, "../..");
const PACKAGE = "@mininglamp-oss/octo-cli";
const OLD = "9.8.0";
const TARGET = "9.8.1";
const STALE = "9.8.2";
const UNLAUNCHABLE = "9.8.3";
const MISSING_PLATFORM = "9.8.4";
const BINARY = process.platform === "win32" ? "octo-cli.exe" : "octo-cli";

function execute(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { ...options, stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (data) => { stdout += data; });
    child.stderr.on("data", (data) => { stderr += data; });
    child.on("error", reject);
    child.on("close", (status, signal) => resolve({ status, signal, stdout, stderr }));
  });
}

async function checked(command, args, options) {
  const result = await execute(command, args, options);
  assert.equal(result.status, 0, `${command} ${args.join(" ")}\n${result.stderr}\n${result.stdout}`);
  return result;
}

// Node cannot exec .cmd directly. Resolve the native manager or its JS entry,
// without a shell; the same mechanism works with ordinary Unix bin symlinks.
function managerCommand(name) {
  const suffix = process.platform === "win32" ? [".exe", ".cmd", ""] : [""];
  for (const dir of process.env.PATH.split(path.delimiter)) {
    for (const ext of suffix) {
      const candidate = path.join(dir, name + ext);
      if (!fs.existsSync(candidate)) continue;
      const resolved = fs.realpathSync(candidate);
      if (ext === ".cmd") {
        const entries = name === "npm"
          ? ["node_modules/npm/bin/npm-cli.js"]
          : ["node_modules/pnpm/pnpm.exe", "node_modules/pnpm/bin/pnpm.cjs", "node_modules/corepack/dist/pnpm.js"];
        const entry = entries.map((file) => path.join(dir, file)).find((file) => fs.existsSync(file));
        assert.ok(entry, `cannot resolve ${candidate} to its manager entry`);
        return entry.endsWith(".exe") ? { file: entry, args: [] } : { file: process.execPath, args: [entry] };
      }
      return /\.(?:c?js|mjs)$/.test(resolved)
        ? { file: process.execPath, args: [resolved] }
        : { file: resolved, args: [] };
    }
  }
  throw new Error(`${name} is required for upgrade acceptance`);
}

function managerRun(manager, args, options) {
  return checked(manager.file, [...manager.args, ...args], options);
}

function isolatedEnvironment(home) {
  const env = { ...process.env };
  for (const key of Object.keys(env)) {
    if (/^(?:npm_config_|pnpm_|bun_|octo_)/i.test(key) || key.toLowerCase() === "path") delete env[key];
  }
  return {
    ...env,
    PATH: process.env.PATH,
    HOME: home,
    USERPROFILE: home,
    APPDATA: path.join(home, "appdata"),
    LOCALAPPDATA: path.join(home, "localappdata"),
    XDG_CONFIG_HOME: path.join(home, "config"),
    XDG_CACHE_HOME: path.join(home, "cache"),
    NPM_CONFIG_USERCONFIG: path.join(home, ".npmrc"),
    NPM_CONFIG_CACHE: path.join(home, "npm-cache"),
    COREPACK_ENABLE_PROJECT_SPEC: "0",
    COREPACK_ENABLE_DOWNLOAD_PROMPT: "0",
    CI: "1",
  };
}

async function registryFixture(tmp) {
  const packages = new Map();
  const tarballs = new Map();
  const requests = [];
  let latest = TARGET;
  let nextTag;
  const server = http.createServer((req, res) => {
    const pathname = decodeURIComponent(new URL(req.url, "http://fixture.invalid").pathname);
    requests.push({ path: pathname, authorization: req.headers.authorization });
    if (req.headers.authorization !== "Bearer fixture-not-a-real-secret") {
      res.writeHead(401, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: "fixture registry requires authentication" }));
      return;
    }
    if (tarballs.has(pathname)) {
      res.setHeader("content-type", "application/octet-stream");
      res.end(tarballs.get(pathname));
      return;
    }
    const name = pathname.slice(1);
    if (!packages.has(name)) {
      res.writeHead(404, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: "fixture package not found" }));
      return;
    }
    const versions = packages.get(name);
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({
      name,
      "dist-tags": { latest },
      time: Object.fromEntries(Object.keys(versions).map((version) => [version, "2020-01-01T00:00:00.000Z"])),
      versions,
    }));
    if (name === PACKAGE && nextTag) {
      latest = nextTag;
      nextTag = undefined;
    }
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const url = `http://127.0.0.1:${server.address().port}/`;
  const npm = managerCommand("npm");
  try {
    for (const version of [OLD, TARGET, STALE, UNLAUNCHABLE, MISSING_PLATFORM]) {
      const stage = path.join(tmp, `release-${version}`);
      const archives = path.join(stage, "archives");
      const output = path.join(stage, "packages");
      const hostBinary = path.join(stage, BINARY);
      fs.mkdirSync(archives, { recursive: true });
      if (version === OLD || version === TARGET) {
        await checked("go", ["build", "-ldflags", `-X github.com/Mininglamp-OSS/octo-cli/cmd.Version=${version}`, "-o", hostBinary, "./cmd/octo-cli"], { cwd: ROOT });
      } else if (version === UNLAUNCHABLE) {
        fs.writeFileSync(hostBinary, "unlaunchable fixture artifact\n", { mode: 0o755 });
      } else {
        fs.copyFileSync(path.join(tmp, `release-${TARGET}`, BINARY), hostBinary);
      }
      for (const platform of PLATFORMS) {
        const memberDir = path.join(stage, `${platform.goOs}-${platform.goArch}`);
        fs.mkdirSync(memberDir);
        const member = binFileName(platform.goOs);
        // Only the host platform is executed. Other archive members exercise
        // packaging/resolution, not foreign-architecture execution.
        fs.copyFileSync(hostBinary, path.join(memberDir, member));
        await checked("tar", ["-czf", path.join(archives, `octo-cli_${version}_${platform.goOs}_${platform.goArch}.tar.gz`), "-C", memberDir, member]);
      }
      await checked(process.execPath, [path.join(__dirname, "prepare-packages.js"), "--version", version, "--dist", archives, "--out", output]);
      for (const entry of fs.readdirSync(output)) {
        const dir = path.join(output, entry);
        if (version === MISSING_PLATFORM && entry !== "octo-cli") continue;
        const manifest = JSON.parse(fs.readFileSync(path.join(dir, "package.json"), "utf8"));
        const packed = JSON.parse((await managerRun(npm, ["pack", "--ignore-scripts", "--json"], { cwd: dir })).stdout)[0];
        const bytes = fs.readFileSync(path.join(dir, packed.filename));
        const route = `/tarballs/${packed.filename}`;
        tarballs.set(route, bytes);
        if (!packages.has(manifest.name)) packages.set(manifest.name, {});
        packages.get(manifest.name)[version] = {
          ...manifest,
          dist: {
            tarball: url.slice(0, -1) + route,
            shasum: crypto.createHash("sha1").update(bytes).digest("hex"),
            integrity: `sha512-${crypto.createHash("sha512").update(bytes).digest("base64")}`,
          },
        };
      }
    }
  } catch (error) {
    await new Promise((resolve) => server.close(resolve));
    throw error;
  }
  return {
    url, requests,
    setLatest(value) { latest = value; nextTag = undefined; },
    changeTagAfterLookup(value) { nextTag = value; },
    close: () => new Promise((resolve) => server.close(resolve)),
  };
}

async function globalInstall(tmp, name, registry, options = {}) {
  const home = options.home || path.join(tmp, options.id || name);
  fs.mkdirSync(home, { recursive: true });
  const env = isolatedEnvironment(home);
  const cwd = path.join(home, "neutral");
  fs.mkdirSync(cwd, { recursive: true });
  // The unrelated project registry deliberately cannot serve the package.
  const project = path.join(home, "project");
  fs.mkdirSync(project, { recursive: true });
  fs.writeFileSync(path.join(project, "package.json"), "{\"name\":\"unrelated-project\",\"private\":true}");
  fs.writeFileSync(path.join(project, ".npmrc"), "registry=http://127.0.0.1:1/\n@mininglamp-oss:registry=http://127.0.0.1:1/\n");
  const npmrc = `registry=${registry}\n@mininglamp-oss:registry=${registry}\n//127.0.0.1:${new URL(registry).port}/:_authToken=fixture-not-a-real-secret\naudit=false\nfund=false\n`;
  fs.writeFileSync(path.join(home, ".npmrc"), npmrc);
  const manager = managerCommand(name);
  let command;
  if (name === "npm") {
    const prefix = path.join(home, process.platform === "win32" ? "prefix global" : "prefix & global");
    const realPrefix = path.join(home, "real-prefix");
    fs.mkdirSync(realPrefix);
    fs.symlinkSync(realPrefix, prefix, process.platform === "win32" ? "junction" : "dir");
    env.NPM_CONFIG_PREFIX = prefix;
    await managerRun(manager, ["install", "-g", `${PACKAGE}@${OLD}`], { cwd, env });
    command = path.join(prefix, process.platform === "win32" ? "octo-cli.cmd" : "bin/octo-cli");
  } else if (name === "bun") {
    const globalDir = path.join(home, "global & packages");
    const binDir = path.join(home, "custom bin");
    fs.mkdirSync(env.XDG_CONFIG_HOME, { recursive: true });
    const bunfigPath = path.join(env.XDG_CONFIG_HOME, ".bunfig.toml");
    const bunfig = `[install]\nglobalDir = ${JSON.stringify(globalDir)}\nglobalBinDir = ${JSON.stringify(binDir)}\nregistry = "${registry}"\n[install.scopes]\n"@mininglamp-oss" = { url = "${registry}", token = "fixture-not-a-real-secret" }\n`;
    fs.writeFileSync(bunfigPath, bunfig);
    await managerRun(manager, ["install", "-g", `${PACKAGE}@${OLD}`], { cwd, env });
    if (options.omitOptional) {
      fs.writeFileSync(bunfigPath, bunfig.replace("[install]\n", "[install]\noptional = false\n"));
    }
    command = path.join(binDir, process.platform === "win32" ? "octo-cli.exe" : "octo-cli");
  } else {
    env.PNPM_HOME = path.join(home, "pnpm home");
    const globalBin = path.join(env.PNPM_HOME, "bin");
    fs.mkdirSync(globalBin, { recursive: true });
    env.PATH = [globalBin, env.PNPM_HOME, env.PATH].join(path.delimiter);
    await managerRun(manager, ["config", "set", "global-bin-dir", globalBin, "--global"], { cwd, env });
    await managerRun(manager, ["config", "set", "registry", registry, "--global"], { cwd, env });
    await managerRun(manager, ["add", "-g", `${PACKAGE}@${OLD}`], { cwd, env });
    if (options.omitOptional) {
      await managerRun(manager, ["config", "set", "optional", "false", "--global"], { cwd, env });
    }
    command = path.join(globalBin, process.platform === "win32" ? "octo-cli.cmd" : "octo-cli");
  }
  // Re-open the fixed manager-created command on every invocation: pnpm can
  // replace the group directory to which its Windows command shim points.
  env.NPM_CONFIG_USERCONFIG = "../.npmrc";
  env.NPM_CONFIG_REGISTRY = "http://127.0.0.1:1/";
  const run = (args, extraEnv = {}) => {
    const options = { cwd: project, env: { ...env, ...extraEnv } };
    if (process.platform === "win32" && command.endsWith(".cmd")) {
      // Fixture arguments are fixed words/flags. Quote the fixture command
      // path, which deliberately includes spaces and an ampersand.
      return execute(process.env.ComSpec, ["/d", "/s", "/c", `""${command}" ${args.join(" ")}"`], {
        ...options, windowsVerbatimArguments: true,
      });
    }
    return execute(command, args, options);
  };
  if (options.omitOptional) {
    // Omit only during upgrade: the starting binary must exist to be invoked.
    env.NPM_CONFIG_OMIT = "optional";
    env.npm_config_optional = "false";
  }
  return { run, env, home, command, manager, cwd, project };
}

const selected = (process.env.OCTO_UPGRADE_TEST_MANAGERS || "npm,bun,pnpm").split(",");

test("real global installations upgrade through their configured registry", { timeout: 900000 }, async (t) => {
  const tmp = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "octo-upgrade-e2e-")));
  const registry = await registryFixture(tmp);
  t.after(async () => {
    await registry.close();
    fs.rmSync(tmp, { recursive: true, force: true });
  });
  const installed = [];
  const sharedHome = path.join(tmp, "coexisting");
  for (const name of selected) {
    installed.push({ name, ...await globalInstall(tmp, name, registry.url, { home: sharedHome, omitOptional: true }) });
  }
  const sharedLocations = {};
  for (const instance of installed) {
    for (const key of ["NPM_CONFIG_PREFIX", "BUN_INSTALL_GLOBAL_DIR", "BUN_INSTALL_BIN", "PNPM_HOME"]) {
      if (instance.env[key]) sharedLocations[key] = instance.env[key];
    }
  }
  const competingBins = installed.map((instance) => path.dirname(instance.command));
  for (const instance of installed) {
    Object.assign(instance.env, sharedLocations);
    instance.env.PATH = [...competingBins, instance.env.PATH].join(path.delimiter);
  }
  const expectedVersions = new Map(selected.map((name) => [name, OLD]));
  for (const instance of installed) {
    const { name } = instance;
    await t.test(name, async () => {
      registry.setLatest(TARGET);
      let result = await instance.run(["version"]);
      assert.equal(result.status, 0, result.stderr);
      assert.equal(JSON.parse(result.stdout).data.version, OLD);

      const beforeDryRun = registry.requests.length;
      result = await instance.run(["--profile", "unconfigured-profile", "--space", "unused-space", "upgrade", "--dry-run"], { npm_config_user_agent: "pnpm/0 npm/? bun/?" });
      if (process.platform === "win32" && result.status !== 0) {
        const localArgs = name === "bun" ? ["pm", "ls", "-g"] : ["list", "-g", "--depth=0", "--json"];
        const binArgs = name === "bun" ? ["pm", "bin", "-g"] : ["bin", "-g"];
        const options = { cwd: instance.project, env: instance.env };
        const entries = fs.readdirSync(path.dirname(instance.command))
          .filter((entry) => entry.startsWith("octo-cli"))
          .map((entry) => ({
            name: entry,
            contents: /\.(?:cmd|bunx)$/.test(entry)
              ? fs.readFileSync(path.join(path.dirname(instance.command), entry)).toString(entry.endsWith(".bunx") ? "base64" : "utf8")
              : null,
          }));
        t.diagnostic(JSON.stringify({
          manager: name,
          native_list: await execute(instance.manager.file, [...instance.manager.args, ...localArgs], options),
          native_bin: await execute(instance.manager.file, [...instance.manager.args, ...binArgs], options),
          entries,
        }));
      }
      assert.equal(result.status, 0, result.stderr);
      const plan = JSON.parse(result.stdout).data;
      assert.equal(plan.manager, name);
      assert.equal(plan.current_version, OLD);
      assert.equal(plan.dry_run, true);
      assert.equal(registry.requests.length, beforeDryRun, "dry run contacted registry");

      registry.changeTagAfterLookup(STALE);
      result = await instance.run(["upgrade"], { npm_config_user_agent: "unrelated-manager/99" });
      assert.equal(result.status, 0, result.stderr);
      assert.deepEqual(JSON.parse(result.stdout).data, {
        manager: name, previous_version: OLD, target_version: TARGET, current_version: TARGET, updated: true,
      });
      assert.equal(result.stderr, "");
      assert.ok(!result.stdout.includes("fixture-not-a-real-secret"));
      result = await instance.run(["version"]);
      assert.equal(result.status, 0, result.stderr);
      assert.equal(JSON.parse(result.stdout).data.version, TARGET);
      expectedVersions.set(name, TARGET);
      registry.setLatest(TARGET);

      result = await instance.run(["upgrade", "--jq", "empty"]);
      assert.equal(result.status, 0, result.stderr);
      registry.setLatest(OLD);
      result = await instance.run(["upgrade"]);
      assert.equal(result.status, 0, result.stderr);
      assert.deepEqual(JSON.parse(result.stdout).data, {
        manager: name, previous_version: TARGET, target_version: OLD, current_version: TARGET, updated: false,
      });
      for (const other of installed.filter((copy) => copy !== instance)) {
        const unchanged = await other.run(["version"]);
        assert.equal(unchanged.status, 0, unchanged.stderr);
        assert.equal(JSON.parse(unchanged.stdout).data.version, expectedVersions.get(other.name));
      }
    });
  }
  for (const name of selected) {
    for (const [version, reason] of [
      [STALE, "stale executable version"],
      [UNLAUNCHABLE, "unlaunchable executable"],
      [MISSING_PLATFORM, "missing platform dependency"],
    ]) {
      await t.test(`${name}: ${reason}`, async () => {
        registry.setLatest(TARGET);
        const instance = await globalInstall(tmp, name, registry.url, { id: `${name}-${version}` });
        registry.setLatest(version);
        const result = await instance.run(["upgrade"]);
        assert.equal(result.status, 1, `${reason}: ${result.stderr}\n${result.stdout}`);
        assert.equal(result.stdout, "", "failed verification emitted a success result");
        const failure = JSON.parse(result.stderr);
        assert.equal(failure.ok, false);
        assert.ok(failure.error.code.startsWith("UPGRADE_"));
        assert.ok(!result.stderr.includes("fixture-not-a-real-secret"));
      });
    }
  }
});
