"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const http = require("node:http");
const os = require("node:os");
const path = require("node:path");
const { spawn } = require("node:child_process");

const PACKAGE_NAME = "@mininglamp-oss/octo-cli";
const PLATFORM_PACKAGE = `${PACKAGE_NAME}-${process.platform}-${process.arch}`;
const VERSION = "1.2.3";
const SHIM = path.join(__dirname, "..", "bin", "octo-cli.js");
const NPM_DIR = path.join(__dirname, "..");
const shim = require(SHIM);

function findExecutable(name) {
  for (const directory of (process.env.PATH || "").split(path.delimiter)) {
    if (!directory) continue;
    const candidate = path.join(directory, name);
    try {
      fs.accessSync(candidate, fs.constants.X_OK);
      if (fs.statSync(candidate).isFile()) return candidate;
    } catch (_err) {
      // Keep searching the inherited PATH.
    }
  }
  return undefined;
}

function runProcess(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      ...options,
      stdio: ["ignore", "pipe", "pipe"],
      timeout: options.timeout || 60_000,
    });
    let stdout = "";
    let stderr = "";
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk) => { stdout += chunk; });
    child.stderr.on("data", (chunk) => { stderr += chunk; });
    child.once("error", reject);
    child.once("close", (status, signal) => resolve({ status, signal, stdout, stderr }));
  });
}

async function checked(command, args, options = {}) {
  const result = await runProcess(command, args, options);
  assert.equal(result.status, 0, `${command} ${args.join(" ")} failed:\n${result.stderr}\n${result.stdout}`);
  return result;
}

function cleanNpmEnvironment(userConfig, prefix, cache) {
  const env = { ...process.env };
  for (const key of Object.keys(env)) {
    if (/^npm_config_/i.test(key) || key.toLowerCase() === "npm_execpath") delete env[key];
  }
  return {
    ...env,
    npm_config_userconfig: userConfig,
    npm_config_prefix: prefix,
    npm_config_cache: cache,
    npm_config_update_notifier: "false",
    npm_config_audit: "false",
    npm_config_fund: "false",
    npm_config_fetch_retries: "0",
    npm_config_fetch_timeout: "1500",
    npm_config_user_agent: "pnpm/99.0.0",
  };
}

function fakeGoSource() {
  return `#!/usr/bin/env node
"use strict";
const fs = require("node:fs");
const path = require("node:path");
const trace = process.env.OCTO_CLI_TEST_TRACE;
const args = process.argv.slice(2);
const version = process.env.OCTO_CLI_TEST_BINARY_VERSION || "1.2.3";
let phase = "other";
function events() {
  if (!fs.existsSync(trace)) return [];
  return fs.readFileSync(trace, "utf8").trim().split(/\\r?\\n/).filter(Boolean).map((line) => JSON.parse(line));
}
function record(event) {
  fs.appendFileSync(trace, JSON.stringify(event) + "\\n");
}
process.on("exit", () => {
  if (phase === "request") record({ phase: "go-exit", pid: process.pid });
});
if (process.env.OCTO_CLI_UPGRADE_REPORT === "1") {
  const handoff = process.env.OCTO_CLI_UPGRADE_HANDOFF;
  if (!handoff) {
    process.stderr.write("report handoff was not provided\\n");
    process.exit(71);
  }
  const request = JSON.parse(fs.readFileSync(path.join(handoff, "request.json"), "utf8"));
  const result = JSON.parse(fs.readFileSync(path.join(handoff, "result.json"), "utf8"));
  const initial = events().find((event) => event.phase === "request" && event.handoff === handoff);
  const exited = events().find((event) => event.phase === "go-exit" && initial && event.pid === initial.pid);
  if (!initial || !exited || request.version !== version || !request.dry_run && args[0] !== "upgrade" || args.join(" ") !== (request.dry_run ? "upgrade --dry-run" : "upgrade")) {
    process.stderr.write("invalid upgrade report protocol\\n");
    process.exit(72);
  }
  try {
    process.kill(exited.pid, 0);
    process.stderr.write("original Go process is still running\\n");
    process.exit(73);
  } catch (err) {
    if (err.code !== "ESRCH") throw err;
  }
  record({ phase: "report", argv: args });
  if (result.error) {
    process.stderr.write(JSON.stringify({ ok: false, error: result.error }) + "\\n");
    process.exit(result.error.exit_code);
  }
  process.stdout.write(JSON.stringify({ ok: true, data: result.data }) + "\\n");
  process.exit(0);
}
if (args[0] !== "upgrade") {
  process.stdout.write("fake-go " + args.join(" ") + "\\n");
  process.exit(0);
}
const handoff = process.env.OCTO_CLI_UPGRADE_HANDOFF || process.env.OCTO_CLI_TEST_HANDOFF_FALLBACK;
if (!handoff) process.exit(0);
fs.mkdirSync(handoff, { recursive: true, mode: 0o700 });
fs.writeFileSync(path.join(handoff, "request.json"), JSON.stringify({ version, dry_run: args.includes("--dry-run") }), { mode: 0o600 });
phase = "request";
record({ phase: "request", handoff, pid: process.pid, argv: args });
process.exit(0);
`;
}

async function listen(server) {
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  return server.address().port;
}

async function close(server) {
  if (!server.listening) return;
  await new Promise((resolve, reject) => server.close((err) => err ? reject(err) : resolve()));
}

async function createNpmFixture(t, npm) {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-upgrade-"));
  const prefix = path.join(tmp, "npm-prefix");
  const cache = path.join(tmp, "npm-cache");
  const packages = new Map();
  const tarballs = new Map();
  const requests = [];
  let latest = VERSION;
  let rejectPackuments = false;
  const server = http.createServer((request, response) => {
    const pathname = decodeURIComponent(new URL(request.url, "http://fixture.invalid").pathname);
    requests.push({ path: pathname, method: request.method });
    if (tarballs.has(pathname)) {
      response.setHeader("content-type", "application/octet-stream");
      response.end(tarballs.get(pathname));
      return;
    }
    const name = pathname.replace(/^\/+/, "");
    if (rejectPackuments) {
      response.writeHead(401, { "content-type": "application/json" });
      response.end(JSON.stringify({ error: "fixture auth rejected; fixture-not-a-real-secret" }));
      return;
    }
    if (!packages.has(name)) {
      response.writeHead(404, { "content-type": "application/json" });
      response.end(JSON.stringify({ error: "fixture package not found" }));
      return;
    }
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({ name, "dist-tags": { latest }, versions: packages.get(name) }));
  });
  const port = await listen(server);
  const registry = `http://127.0.0.1:${port}/`;
  const userConfig = path.join(tmp, "user.npmrc");
  fs.writeFileSync(userConfig, `registry=${registry}\n@mininglamp-oss:registry=${registry}\n//127.0.0.1:${port}/:_authToken=fixture-not-a-real-secret\naudit=false\nfund=false\n`);
  t.after(async () => {
    await close(server);
    fs.rmSync(tmp, { recursive: true, force: true });
  });

  const mainSource = path.join(tmp, "main-source");
  const platformSource = path.join(tmp, "platform-source");
  fs.mkdirSync(path.join(mainSource, "bin"), { recursive: true });
  fs.mkdirSync(path.join(platformSource, "bin"), { recursive: true });
  fs.copyFileSync(SHIM, path.join(mainSource, "bin", "octo-cli.js"));
  fs.chmodSync(path.join(mainSource, "bin", "octo-cli.js"), 0o755);
  const librarySource = path.join(NPM_DIR, "lib");
  if (fs.existsSync(librarySource)) fs.cpSync(librarySource, path.join(mainSource, "lib"), { recursive: true });
  fs.writeFileSync(path.join(mainSource, "package.json"), JSON.stringify({
    name: PACKAGE_NAME,
    version: VERSION,
    bin: { "octo-cli": "bin/octo-cli.js" },
    optionalDependencies: { [PLATFORM_PACKAGE]: VERSION },
    files: ["bin/octo-cli.js", "lib/"],
  }, null, 2) + "\n");
  const binaryName = shim.binaryNameFor(process.platform);
  fs.writeFileSync(path.join(platformSource, "package.json"), JSON.stringify({
    name: PLATFORM_PACKAGE,
    version: VERSION,
    os: [process.platform],
    cpu: [process.arch],
    files: [`bin/${binaryName}`],
  }, null, 2) + "\n");
  const binary = path.join(platformSource, "bin", binaryName);
  fs.writeFileSync(binary, fakeGoSource(), { mode: 0o755 });
  fs.chmodSync(binary, 0o755);

  const packEnv = cleanNpmEnvironment(userConfig, prefix, cache);
  for (const directory of [mainSource, platformSource]) {
    const manifest = JSON.parse(fs.readFileSync(path.join(directory, "package.json"), "utf8"));
    const packedResult = await checked(npm, ["pack", "--ignore-scripts", "--json"], { cwd: directory, env: packEnv });
    const archiveName = JSON.parse(packedResult.stdout)[0].filename;
    const archivePath = path.join(directory, archiveName);
    const bytes = fs.readFileSync(archivePath);
    const route = `/tarballs/${archiveName}`;
    tarballs.set(route, bytes);
    if (!packages.has(manifest.name)) packages.set(manifest.name, {});
    packages.get(manifest.name)[VERSION] = {
      ...manifest,
      dist: {
        tarball: registry.slice(0, -1) + route,
        shasum: crypto.createHash("sha1").update(bytes).digest("hex"),
        integrity: `sha512-${crypto.createHash("sha512").update(bytes).digest("base64")}`,
      },
    };
  }

  const env = cleanNpmEnvironment(userConfig, prefix, cache);
  const cwd = path.join(tmp, "neutral-cwd");
  fs.mkdirSync(cwd);
  await checked(npm, ["install", "--global", "--prefix", prefix, "--ignore-scripts", "--no-audit", "--no-fund", `${PACKAGE_NAME}@${VERSION}`], { cwd, env });
  const globalRoot = (await checked(npm, ["root", "--global"], { cwd, env })).stdout.trim();
  const mainPackage = path.join(globalRoot, "@mininglamp-oss", "octo-cli");
  const command = path.join(prefix, "bin", "octo-cli");
  const mainManifestPath = path.join(mainPackage, "package.json");
  const installedBinary = path.join(mainPackage, "node_modules", "@mininglamp-oss", path.basename(PLATFORM_PACKAGE), "bin", binaryName);
  const trace = path.join(tmp, "go-trace.jsonl");
  const fallbackHandoff = path.join(tmp, "baseline-handoff");
  assert.equal(fs.realpathSync(command), fs.realpathSync(path.join(mainPackage, "bin", "octo-cli.js")));
  assert.ok(fs.existsSync(installedBinary), "npm must install the pinned platform optional dependency from the registry");

  return {
    npm, tmp, env, cwd, prefix, mainPackage, command, mainManifestPath, installedBinary, trace, fallbackHandoff,
    requests,
    setLatest(value) {
      latest = value;
      const versions = packages.get(PACKAGE_NAME);
      if (!versions[value]) versions[value] = { ...versions[VERSION], version: value };
    },
    rejectPackuments(value) { rejectPackuments = value; },
    registryStart() { return requests.length; },
  };
}

function upgradeEnvironment(fixture, extra = {}) {
  return {
    ...fixture.env,
    OCTO_CLI_TEST_HANDOFF_FALLBACK: fixture.fallbackHandoff,
    OCTO_CLI_TEST_TRACE: fixture.trace,
    ...extra,
  };
}

async function runUpgrade(fixture, command, args, extraEnv = {}) {
  return runProcess(command, args, {
    cwd: fixture.cwd,
    env: upgradeEnvironment(fixture, extraEnv),
  });
}

function createNpmObserver(fixture, name) {
  const directory = path.join(fixture.tmp, `npm-observer-${name}`);
  const bin = path.join(directory, "bin");
  fs.mkdirSync(bin, { recursive: true });
  const entry = path.join(bin, "npm.js");
  const command = path.join(bin, "npm");
  const observations = path.join(directory, "observations.jsonl");
  fs.writeFileSync(entry, `#!/usr/bin/env node
"use strict";
const fs = require("node:fs");
const { spawnSync } = require("node:child_process");
fs.appendFileSync(process.env.OCTO_CLI_TEST_NPM_TRACE, JSON.stringify({
  args: process.argv.slice(2),
  cwd: process.cwd(),
  cert: process.env.npm_config_cert,
  key: process.env.npm_config_key
}) + "\\n");
const result = spawnSync(${JSON.stringify(fixture.npm)}, process.argv.slice(2), {
  cwd: process.cwd(),
  env: process.env,
  stdio: "inherit"
});
if (result.error) {
  console.error(result.error.message);
  process.exit(1);
}
process.exit(result.status === null ? 1 : result.status);
`);
  fs.chmodSync(entry, 0o755);
  fs.symlinkSync(entry, command);
  return { path: `${bin}${path.delimiter}${fixture.env.PATH}`, observations };
}

function readNpmObservations(file) {
  return fs.readFileSync(file, "utf8").trim().split(/\r?\n/).map((line) => JSON.parse(line));
}

function nodeOnlyPath(fixture) {
  const directory = path.join(fixture.tmp, "node-only-bin");
  fs.mkdirSync(directory);
  fs.symlinkSync(process.execPath, path.join(directory, "node"));
  return directory;
}

function successEnvelope(result) {
  assert.equal(result.signal, null);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stderr, "");
  const lines = result.stdout.trim().split(/\r?\n/);
  assert.equal(lines.length, 1, "upgrade must produce exactly one public result envelope");
  const envelope = JSON.parse(lines[0]);
  assert.equal(envelope.ok, true);
  return envelope.data;
}

function errorEnvelope(result, exitCode, type) {
  assert.equal(result.signal, null);
  assert.equal(result.status, exitCode, result.stdout);
  assert.equal(result.stdout, "", "upgrade failure must not write a success result");
  const lines = result.stderr.trim().split(/\r?\n/);
  assert.equal(lines.length, 1, "upgrade failure must produce exactly one public error envelope");
  const envelope = JSON.parse(lines[0]);
  assert.equal(envelope.ok, false);
  assert.equal(envelope.error.type, type);
  return envelope.error;
}

test("Windows npm command shims resolve the invoked npm CLI entry", async (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "octo-cli-windows-shim-"));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const npmBin = path.join(directory, "node_modules", "npm", "bin");
  fs.mkdirSync(npmBin, { recursive: true });
  const prefixHelper = path.join(npmBin, "npm-prefix.js");
  const cliEntry = path.join(npmBin, "npm-cli.js");
  fs.writeFileSync(prefixHelper, "process.exit(0);\n");
  fs.writeFileSync(cliEntry, "process.exit(0);\n");
  const shim = path.join(directory, "npm.cmd");
  fs.writeFileSync(shim, [
    '@SET "NPM_PREFIX_JS=%~dp0/node_modules/npm/bin/npm-prefix.js"',
    '@SET "NPM_CLI_JS=%~dp0/node_modules/npm/bin/npm-cli.js"',
    '"%_prog%" "%NPM_PREFIX_JS%" %*',
    '"%_prog%" "%NPM_CLI_JS%" %*',
    "",
  ].join("\r\n"));
  const probe = `
    const modulePath = ${JSON.stringify(path.join(NPM_DIR, "lib", "package-managers.js"))};
    const helpers = require(modulePath);
    if (process.platform !== "win32") Object.defineProperty(process, "platform", { value: "win32" });
    const manager = helpers.inspectManagerCommands({ PATH: ${JSON.stringify(directory)} }).npm;
    process.stdout.write(JSON.stringify(manager));
  `;
  const result = await runProcess(process.execPath, ["-e", probe]);
  assert.equal(result.status, 0, result.stderr);
  const manager = JSON.parse(result.stdout);
  assert.equal(manager.args[0], cliEntry, "npm must launch npm-cli.js, not the earlier npm-prefix.js assignment");
});


test("npm global upgrade handoff and local refusal behavior", { timeout: 240_000 }, async (t) => {
  if (process.platform === "win32") {
    t.skip("the installed-command fixture uses a Unix executable symlink");
    return;
  }
  const npm = findExecutable("npm");
  if (!npm) {
    t.skip("npm is not available on this test host");
    return;
  }
  const fixture = await createNpmFixture(t, npm);
  const manifestBefore = fs.readFileSync(fixture.mainManifestPath, "utf8");
  const binaryBefore = fs.readFileSync(fixture.installedBinary, "utf8");

  await t.test("dry-run reports ownership after Go exits without registry access or mutation", async () => {
    const requestStart = fixture.registryStart();
    const result = await runUpgrade(fixture, fixture.command, ["upgrade", "--dry-run"]);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(fs.readFileSync(fixture.mainManifestPath, "utf8"), manifestBefore);
    assert.equal(fs.readFileSync(fixture.installedBinary, "utf8"), binaryBefore);
    assert.equal(fixture.requests.length, requestStart, "dry-run must not contact the configured registry");
    assert.notEqual(result.stdout.trim(), "", "the launcher must report the Go upgrade request after the child exits");
    const plan = successEnvelope(result);
    assert.equal(plan.dry_run, true);
    assert.equal(plan.package, PACKAGE_NAME);
    assert.equal(plan.manager, "npm");
    assert.equal(plan.current_version, VERSION);
    assert.equal(plan.target, "latest");
    assert.equal(plan.package_path, fixture.mainPackage);
    assert.equal(plan.command_path, fixture.command);

    const events = fs.readFileSync(fixture.trace, "utf8").trim().split(/\r?\n/).map((line) => JSON.parse(line));
    assert.deepEqual(events.map((event) => event.phase), ["request", "go-exit", "report"]);
    assert.deepEqual(events[0].argv, ["upgrade", "--dry-run"]);
    assert.deepEqual(events[2].argv, ["upgrade", "--dry-run"], "the old Go reporter must receive the original arguments");
  });

  await t.test("an equal stable target is a no-op", async () => {
    fixture.setLatest(VERSION);
    const requestStart = fixture.registryStart();
    const result = await runUpgrade(fixture, fixture.command, ["upgrade"]);
    const data = successEnvelope(result);
    assert.deepEqual(data, {
      manager: "npm",
      previous_version: VERSION,
      target_version: VERSION,
      current_version: VERSION,
      updated: false,
    });
    assert.ok(!fixture.requests.slice(requestStart).some((request) => request.path.startsWith("/tarballs/")), "equal target must not install a tarball");
    assert.equal(fs.readFileSync(fixture.mainManifestPath, "utf8"), manifestBefore);
    assert.equal(fs.readFileSync(fixture.installedBinary, "utf8"), binaryBefore);
  });

  await t.test("a Go/package version mismatch is refused before registry access", async () => {
    const requestStart = fixture.registryStart();
    const result = await runUpgrade(fixture, fixture.command, ["upgrade"], {
      OCTO_CLI_TEST_BINARY_VERSION: "1.2.4",
    });
    const error = errorEnvelope(result, 2, "validation");
    assert.match(error.code, /^UPGRADE_/);
    assert.equal(fixture.requests.length, requestStart);
    assert.equal(fs.readFileSync(fixture.mainManifestPath, "utf8"), manifestBefore);
  });

  await t.test("a prerelease latest tag is rejected before installation", async () => {
    fixture.setLatest("1.2.4-rc.1");
    const requestStart = fixture.registryStart();
    const result = await runUpgrade(fixture, fixture.command, ["upgrade"], {
      npm_config_cache: path.join(fixture.tmp, "prerelease-cache"),
    });
    const error = errorEnvelope(result, 2, "config");
    assert.match(error.code, /^UPGRADE_/);
    assert.ok(!fixture.requests.slice(requestStart).some((request) => request.path.startsWith("/tarballs/")));
    assert.equal(fs.readFileSync(fixture.mainManifestPath, "utf8"), manifestBefore);
  });

  await t.test("registry failure is structured and does not expose authentication data", async () => {
    fixture.rejectPackuments(true);
    const result = await runUpgrade(fixture, fixture.command, ["upgrade"], {
      npm_config_cache: path.join(fixture.tmp, "registry-error-cache"),
    });
    fixture.rejectPackuments(false);
    const error = errorEnvelope(result, 1, "api_error");
    assert.match(error.code, /^UPGRADE_/);
    assert.ok(!JSON.stringify(error).includes("fixture-not-a-real-secret"));
    assert.ok(!result.stdout.includes("fixture-not-a-real-secret"));
    assert.ok(!result.stderr.includes("fixture-not-a-real-secret"));
  });

  await t.test("a project-local copy is refused despite an installed global npm copy", async () => {
    const localPackage = path.join(fixture.tmp, "project", "node_modules", "@mininglamp-oss", "octo-cli");
    fs.mkdirSync(path.dirname(localPackage), { recursive: true });
    fs.cpSync(fixture.mainPackage, localPackage, { recursive: true });
    const localLauncher = path.join(localPackage, "bin", "octo-cli.js");
    const requestStart = fixture.registryStart();
    const result = await runUpgrade(fixture, process.execPath, [localLauncher, "upgrade", "--dry-run"]);
    const error = errorEnvelope(result, 2, "validation");
    assert.match(error.code, /^UPGRADE_/);
    assert.equal(fixture.requests.length, requestStart);
    assert.equal(fs.readFileSync(fixture.mainManifestPath, "utf8"), manifestBefore);
  });


  await t.test("a missing npm executable is an operational owner failure when npm layout evidence remains", async () => {
    const requestStart = fixture.registryStart();
    const result = await runUpgrade(fixture, fixture.command, ["upgrade"], {
      PATH: nodeOnlyPath(fixture),
    });
    const error = errorEnvelope(result, 1, "api_error");
    assert.equal(error.code, "UPGRADE_MANAGER_UNAVAILABLE");
    assert.equal(fixture.requests.length, requestStart, "owner unavailability must not fall back to another registry");
  });

  await t.test("native npm commands run from the private handoff directory, not the filesystem root", async () => {
    fixture.setLatest(VERSION);
    const observer = createNpmObserver(fixture, "neutral-cwd");
    const result = await runUpgrade(fixture, fixture.command, ["upgrade"], {
      PATH: observer.path,
      OCTO_CLI_TEST_NPM_TRACE: observer.observations,
    });
    successEnvelope(result);
    const observations = readNpmObservations(observer.observations);
    assert.ok(observations.length >= 4, "npm must perform local ownership and configured registry queries");
    const root = path.parse(path.resolve(fixture.cwd)).root;
    for (const observation of observations) {
      assert.notEqual(observation.cwd, root);
      assert.match(path.basename(observation.cwd), /^octo-cli-upgrade-/);
    }
  });

  await t.test("npm certificate and key PEM values are preserved as content", async () => {
    fixture.setLatest(VERSION);
    const cert = "-----BEGIN CERTIFICATE-----\nfixture certificate bytes\n-----END CERTIFICATE-----";
    const key = "-----BEGIN PRIVATE KEY-----\nfixture private key bytes\n-----END PRIVATE KEY-----";
    const observer = createNpmObserver(fixture, "pem");
    const result = await runUpgrade(fixture, fixture.command, ["upgrade"], {
      PATH: observer.path,
      OCTO_CLI_TEST_NPM_TRACE: observer.observations,
      npm_config_cert: cert,
      npm_config_key: key,
    });
    successEnvelope(result);
    const observations = readNpmObservations(observer.observations);
    const registryQuery = observations.find((observation) => observation.args[0] === "view");
    assert.ok(registryQuery, "npm must query the configured registry");
    assert.equal(registryQuery.cert, cert);
    assert.equal(registryQuery.key, key);
  });
});
