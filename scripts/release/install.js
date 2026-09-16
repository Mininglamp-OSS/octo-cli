#!/usr/bin/env node
"use strict";
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const {spawnSync} = require("node:child_process");
const {parseArgs, checkVersion, targetName, assertManifest, sha256, download} = require("./runtime");
// Replaced by the publisher. No real deployment configuration is checked in.
const SETTINGS = /* RELEASE_SETTINGS */ null;
function command(command, args, options = {}) {
  const r = spawnSync(command, args, {maxBuffer: 512 * 1024 * 1024, ...options});
  if (r.error || r.status !== 0) throw new Error(`${command} failed (${r.error?.code || r.signal || r.status}): ${String(r.stderr || "").slice(0, 500)}`);
  return r.stdout;
}
function extractBinary(archive, binary) {
  // Stream only the exact regular binary entry; never extract arbitrary archive paths.
  const entries = command("tar", ["-tf", archive], {encoding: "utf8"}).trim().split(/\r?\n/);
  if (entries.filter(e => e === binary).length !== 1) throw new Error("Archive must contain exactly one root binary");
  const detail = command("tar", ["-tvf", archive, binary], {encoding: "utf8"});
  if (!detail.startsWith("-")) throw new Error("Archive binary must be a regular file");
  return command("tar", ["-xOf", archive, binary]);
}
function activate(prefix, settings, version, bytes) {
  const binary = settings.binary + (process.platform === "win32" ? ".exe" : "");
  const binDir = path.join(prefix, "bin");
  const stateDir = path.join(prefix, "lib", settings.binary);
  const receiptPath = path.join(stateDir, "installed.json");
  const destination = path.join(binDir, binary);
  fs.mkdirSync(binDir, {recursive: true}); fs.mkdirSync(stateDir, {recursive: true});
  let previous = null; let oldBytes = null;
  const currentFile = fs.lstatSync(destination, {throwIfNoEntry: false});
  if (currentFile) {
    if (currentFile.isSymbolicLink() || !fs.existsSync(receiptPath)) throw new Error(`Refusing to overwrite an unmanaged command: ${destination}. Use --prefix or migrate the old installation explicitly.`);
    previous = fs.readFileSync(receiptPath);
    const receipt = JSON.parse(previous);
    oldBytes = fs.readFileSync(destination);
    if (receipt.component !== settings.component || receipt.sha256 !== sha256(oldBytes)) throw new Error("Existing command does not match its installation receipt");
  }
  const temp = fs.mkdtempSync(path.join(stateDir, ".install-"));
  const candidate = path.join(temp, binary);
  try {
    fs.writeFileSync(candidate, bytes, {mode: 0o755});
    const output = command(candidate, settings.component === "daemon" ? ["--version"] : ["version"], {encoding: "utf8", timeout: 30000});
    let reported;
    if (settings.component === "daemon") reported = output.match(/^octo-daemon (v?[^\s]+)/)?.[1]?.replace(/^v/, "");
    else {
      const result = JSON.parse(output);
      reported = result.data?.version ?? result.version;
    }
    if (reported !== version) throw new Error("Downloaded binary reports an unexpected version");
    const versionsDir = path.join(stateDir, "versions", version);
    fs.mkdirSync(versionsDir, {recursive: true});
    const saved = path.join(versionsDir, binary);
    if (fs.existsSync(saved) && sha256(fs.readFileSync(saved)) !== sha256(bytes)) throw new Error("Version already installed with different contents");
    fs.copyFileSync(candidate, saved); fs.chmodSync(saved, 0o755);
    const next = path.join(binDir, `.${binary}-${process.pid}.tmp`);
    let swapped = false;
    try {
      fs.copyFileSync(candidate, next); fs.chmodSync(next, 0o755);
      fs.renameSync(next, destination); swapped = true;
      const receipt = {component: settings.component, branch: settings.branch, version, sha256: sha256(bytes)};
      fs.writeFileSync(path.join(temp, "receipt.json"), JSON.stringify(receipt, null, 2));
      fs.renameSync(path.join(temp, "receipt.json"), receiptPath);
    } catch (error) {
      if (swapped) {
        if (oldBytes) {
          fs.writeFileSync(next, oldBytes, {mode: 0o755}); fs.renameSync(next, destination);
          fs.writeFileSync(receiptPath, previous);
        } else { fs.rmSync(destination, {force: true}); }
      }
      throw error;
    } finally { fs.rmSync(next, {force: true}); }
    return destination;
  } finally { fs.rmSync(temp, {recursive: true, force: true}); }
}
async function install(settings, argv, fetcher = fetch) {
  if (!settings) throw new Error("This is an installer template; use the generated CDN installer");
  const args = parseArgs(argv, ["--version", "--prefix"], ["--help"]);
  if (args["--help"]) { console.log("node install.js [--version X.Y.Z[-next.N]] [--prefix directory]"); return; }
  const target = targetName(process.platform, process.arch);
  const base = new URL(settings.baseUrl);
  if (base.protocol !== "https:" || base.username || base.password || base.search || base.hash || !base.pathname.endsWith("/")) throw new Error("Invalid installer origin");
  const envVersion = process.env[settings.component === "cli" ? "OCTO_CLI_VERSION" : "OCTO_DAEMON_VERSION"];
  let version = args["--version"] || envVersion;
  const root = `${base.href}${settings.component}/`;
  if (!version) {
    const latest = JSON.parse((await download(`${root}latest.json`, 1024 * 1024, fetcher)).toString());
    if (latest.schemaVersion !== 1 || latest.component !== settings.component || latest.branch !== settings.branch) throw new Error("Unexpected latest channel/component");
    version = latest.version;
  }
  version = checkVersion(settings.branch, version);
  const releaseUrl = `${root}releases/${version}/`;
  const m = assertManifest(JSON.parse((await download(`${releaseUrl}release.json`, 1024 * 1024, fetcher)).toString()), settings.component);
  if (m.branch !== settings.branch || m.version !== version) throw new Error("Release channel/version mismatch");
  const asset = m.targets[target];
  const bytes = await download(`${releaseUrl}${asset.file}`, asset.size, fetcher);
  if (bytes.length !== asset.size || sha256(bytes) !== asset.sha256) throw new Error("Archive SHA-256/size mismatch; installation stopped");
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), `${settings.binary}-install-`));
  try {
    const archive = path.join(temporary, asset.file); fs.writeFileSync(archive, bytes);
    const binary = settings.binary + (process.platform === "win32" ? ".exe" : "");
    const binaryBytes = extractBinary(archive, binary);
    const defaultPrefix = process.platform === "win32" ? path.join(process.env.LOCALAPPDATA || os.homedir(), "Octo") : path.join(os.homedir(), ".local");
    const commandPath = activate(path.resolve(args["--prefix"] || defaultPrefix), settings, version, binaryBytes);
    console.log(`Installed ${settings.binary} ${version}: ${commandPath}\nAdd ${path.dirname(commandPath)} to PATH. Check command resolution if npm/Homebrew is also installed.`);
    if (settings.component === "daemon") console.log("Install octo-cli separately and configure OCTO_CLI_PATH if needed. Restart the daemon explicitly after upgrading. COS auto-update is not enabled by this installer.");
    return commandPath;
  } finally { fs.rmSync(temporary, {recursive: true, force: true}); }
}
module.exports = {install, activate, extractBinary};
if (require.main === module) install(SETTINGS, process.argv.slice(2)).catch(e => { console.error(e.message); process.exitCode = 1; });
