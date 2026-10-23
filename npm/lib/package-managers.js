"use strict";

const fs = require("node:fs");
const path = require("node:path");
const { spawnSync } = require("node:child_process");

const MANAGERS = ["npm", "bun", "pnpm"];
const OUTPUT_LIMIT = 8 * 1024 * 1024;

function pathIsAbsolute(value) {
  return path.isAbsolute(value) || (process.platform === "win32" && /^[a-z]:[\\/]/i.test(value));
}

function resolveRelativeConfigurationPaths(env, originalCwd) {
  const keys = [
    "npm_config_userconfig", "NPM_CONFIG_USERCONFIG",
    "npm_config_globalconfig", "NPM_CONFIG_GLOBALCONFIG",
    "npm_config_prefix", "NPM_CONFIG_PREFIX",
    "npm_config_cache", "NPM_CONFIG_CACHE",
    "npm_config_cafile", "NPM_CONFIG_CAFILE",
    "BUN_CONFIG_FILE", "bun_config_file",
    "BUN_INSTALL", "BUN_INSTALL_GLOBAL_DIR", "BUN_INSTALL_BIN",
    "PNPM_HOME", "pnpm_home", "COREPACK_HOME",
    "NODE_EXTRA_CA_CERTS",
    "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
    "XDG_CONFIG_HOME", "XDG_CACHE_HOME",
  ];
  for (const key of keys) {
    const value = env[key];
    if (typeof value === "string" && value !== "" && !pathIsAbsolute(value)) {
      env[key] = path.resolve(originalCwd, value);
    }
  }
  for (const key of Object.keys(env)) {
    if (key.toLowerCase() !== "path" || typeof env[key] !== "string") continue;
    env[key] = env[key].split(path.delimiter).map((entry) => entry ? (pathIsAbsolute(entry) ? entry : path.resolve(originalCwd, entry)) : originalCwd).join(path.delimiter);
  }
  return env;
}

function windowsShimTarget(file, commandName) {
  try {
    const text = fs.readFileSync(file, "utf8");
    const targetName = commandName === "npm" ? "npm-cli" : commandName;
    const escapedName = targetName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    const extension = commandName === "npm" || commandName === "octo-cli" ? "(?:c?js|mjs)" : "(?:c?js|mjs|exe)";
    const match = text.match(new RegExp(`(?:%~dp0|%dp0%)([^\\r\\n"']*?${escapedName}\\.${extension})(?=["'\\s])`, "i"));
    if (!match) return null;
    const relativeTarget = match[1].replace(/[\\/]/g, path.sep).replace(/^[\\/]+/, "");
    return path.resolve(path.dirname(file), relativeTarget);
  } catch (_err) {
    return null;
  }
}

function managerCommand(name, env = process.env) {
  const windows = process.platform === "win32";
  const suffixes = windows
    ? (name === "bun" ? [".exe", ".cmd", ""] : [".cmd", ".exe", ""])
    : [""];
  const searchPath = env.PATH || env.Path || "";
  for (const directory of searchPath.split(path.delimiter)) {
    if (!directory) continue;
    for (const suffix of suffixes) {
      const candidate = path.join(directory, `${name}${suffix}`);
      if (!fs.existsSync(candidate)) continue;
      let resolved;
      try {
        resolved = fs.realpathSync(candidate);
      } catch (_err) {
        continue;
      }
      const extension = path.extname(candidate).toLowerCase();
      if (windows && [".cmd", ".bat"].includes(extension)) {
        const entry = windowsShimTarget(candidate, name);
        if (entry && fs.existsSync(entry)) {
          if (path.extname(entry).toLowerCase() === ".exe") return { name, file: entry, args: [], path: candidate };
          return { name, file: process.execPath, args: [entry], path: candidate };
        }
        continue;
      }
      if (/\.(?:c?js|mjs)$/i.test(resolved)) {
        return { name, file: process.execPath, args: [resolved], path: candidate };
      }
      return { name, file: resolved, args: [], path: candidate };
    }
  }
  return null;
}

function run(manager, args, options) {
  let result;
  try {
    result = spawnSync(manager.file, [...manager.args, ...args], {
      cwd: options.cwd,
      env: options.env,
      encoding: "utf8",
      windowsHide: true,
      maxBuffer: OUTPUT_LIMIT,
    });
  } catch (_err) {
    return { status: null, stdout: "", stderr: "", error: true };
  }
  return {
    status: result.status,
    stdout: result.stdout || "",
    stderr: result.stderr || "",
    error: Boolean(result.error),
    signal: result.signal,
  };
}

function completed(result) {
  return result.status === 0 && !result.error && !result.signal;
}

function parseJson(text) {
  try {
    return JSON.parse(text);
  } catch (_err) {
    return null;
  }
}

function listingHasPackage(text, packageName, version) {
  const escapedName = packageName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const escapedVersion = version.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const withoutAnsi = text.replace(/\u001b\[[0-9;]*m/g, "");
  return new RegExp(`(?:^|[\\s├└─*])${escapedName}@${escapedVersion}(?:$|[\\s(])`, "m").test(withoutAnsi);
}


function bunRootFromListing(text) {
  for (const line of text.replace(/\u001b\[[0-9;]*m/g, "").split(/\r?\n/)) {
    const candidate = line.trim();
    const header = candidate.match(/^(.+?)\s+node_modules(?:\s+\(\d+\))?$/i);
    if (header && pathIsAbsolute(header[1])) return path.resolve(header[1], "node_modules");
  }
  return null;
}


function queryLocalState(manager, options) {
  if (manager.name === "npm") {
    const prefixResult = run(manager, ["prefix", "--global"], options);
    const rootResult = run(manager, ["root", "--global"], options);
    const listResult = run(manager, ["ls", "--global", "--long", "--json", "--depth=0"], options);
    if (![prefixResult, rootResult, listResult].every(completed)) return null;
    const prefix = prefixResult.stdout.trim();
    const root = rootResult.stdout.trim();
    const listing = parseJson(listResult.stdout);
    if (!prefix || !root || !listing) return null;
    return { manager, prefix, root, bin: path.join(prefix, process.platform === "win32" ? "" : "bin"), listing };
  }

  if (manager.name === "bun") {
    const binResult = run(manager, ["pm", "bin", "--global"], options);
    const listResult = run(manager, ["pm", "ls", "--global"], options);
    if (![binResult, listResult].every(completed)) return null;
    const bin = binResult.stdout.trim();
    if (!bin || !listResult.stdout.trim()) return null;
    const root = bunRootFromListing(listResult.stdout);
    if (!root) return null;
    return { manager, prefix: null, root, bin, listing: listResult.stdout };
  }

  const rootResult = run(manager, ["root", "--global"], options);
  const binResult = run(manager, ["bin", "--global"], options);
  const listResult = run(manager, ["list", "--global", "--depth=0", "--json"], options);
  if (![rootResult, binResult, listResult].every(completed)) return null;
  const root = rootResult.stdout.trim();
  const bin = binResult.stdout.trim();
  const listing = parseJson(listResult.stdout);
  if (!root || !bin || !listing) return null;
  return { manager, prefix: null, root, bin, listing };
}

function packageRecord(state, packageName) {
  if (state.manager.name === "npm") {
    return state.listing.dependencies && state.listing.dependencies[packageName] || null;
  }
  if (state.manager.name === "pnpm") {
    const roots = Array.isArray(state.listing) ? state.listing : [state.listing];
    for (const root of roots) {
      const record = root && root.dependencies && root.dependencies[packageName];
      if (record) return record;
    }
  }
  return null;
}

function managerListsPackage(state, packageName, version) {
  if (state.manager.name === "bun") return listingHasPackage(state.listing, packageName, version);
  const record = packageRecord(state, packageName);
  return Boolean(record && String(record.version) === version);
}

function commandEntryCandidates(state) {
  const names = process.platform === "win32"
    ? (state.manager.name === "bun" ? ["octo-cli.exe", "octo-cli.cmd", "octo-cli"] : ["octo-cli.cmd", "octo-cli.exe", "octo-cli"])
    : ["octo-cli"];
  return names.map((name) => path.join(state.bin, name)).filter((file) => fs.existsSync(file));
}

function bunShimTarget(executable) {
  const metadataPaths = [`${executable}.bunx`, executable.replace(/\.exe$/i, ".bunx")];
  for (const metadataPath of metadataPaths) {
    try {
      const bytes = fs.readFileSync(metadataPath);
      let quoteOffset = -1;
      for (let offset = 0; offset + 1 < bytes.length; offset += 2) {
        if (bytes[offset] === 0x22 && bytes[offset + 1] === 0) {
          quoteOffset = offset;
          break;
        }
      }
      if (quoteOffset < 0 || quoteOffset + 6 > bytes.length) continue;
      if (bytes.readUInt16LE(quoteOffset + 2) !== 0) continue;
      const target = bytes.subarray(0, quoteOffset).toString("utf16le");
      if (!target) continue;
      const absoluteTarget = (bytes.readUInt16LE(bytes.length - 2) & 0b1000) !== 0;
      if (absoluteTarget && !pathIsAbsolute(target)) continue;
      if (pathIsAbsolute(target)) return path.normalize(target);
      return path.resolve(path.dirname(path.dirname(executable)), target);
    } catch (_err) {
      // Try the other Bun sidecar spelling.
    }
  }
  return null;
}

function pnpmShimTarget(entry) {
  let source;
  try {
    source = fs.readFileSync(entry, "utf8");
  } catch (_err) {
    return null;
  }
  const basedir = path.dirname(entry);
  let basedirAbs = basedir;
  try {
    basedirAbs = fs.realpathSync(basedir);
  } catch (_err) {
    // Some package managers emit a usable logical basedir without readlink.
  }
  for (const line of source.split(/\r?\n/)) {
    if (!/\bexec\b/.test(line)) continue;
    const tokens = line.match(/"[^"]*"|'[^']*'|[^\s]+/g) || [];
    for (const token of tokens) {
      const raw = token.replace(/^["']|["']$/g, "");
      if (!/\.(?:c?js|mjs)$/i.test(raw)) continue;
      if (/\$(?!\{?basedir(?:_abs|_win)?\}?)/.test(raw) || raw.includes("`")) continue;
      const expanded = raw.replace(/\$\{?basedir(_abs|_win)?\}?/g, (_match, suffix) => suffix === "_abs" ? basedirAbs : basedir);
      const candidate = path.isAbsolute(expanded) ? expanded : path.resolve(basedir, expanded);
      if (fs.existsSync(candidate)) return fs.realpathSync(candidate);
    }
  }
  return null;
}

function entryTarget(state, entry) {
  if (process.platform !== "win32") {
    try {
      const target = fs.realpathSync(entry);
      return state.manager.name === "pnpm" ? pnpmShimTarget(entry) || target : target;
    } catch (_err) {
      return null;
    }
  }
  if (state.manager.name === "bun" && entry.toLowerCase().endsWith(".exe")) return bunShimTarget(entry);
  if ([".cmd", ".bat"].includes(path.extname(entry).toLowerCase())) return windowsShimTarget(entry, "octo-cli");
  try {
    return fs.realpathSync(entry);
  } catch (_err) {
    return null;
  }
}


function samePath(left, right) {
  if (!left || !right) return false;
  let a;
  let b;
  try {
    a = fs.realpathSync(left);
    b = fs.realpathSync(right);
  } catch (_err) {
    return false;
  }
  if (process.platform === "win32") return path.resolve(a).toLowerCase() === path.resolve(b).toLowerCase();
  return path.resolve(a) === path.resolve(b);
}

function packageIsLinked(state, packageName) {
  if (state.manager.name === "bun" && typeof state.listing === "string") {
    const line = state.listing.split(/\r?\n/).find((item) => item.includes(packageName));
    return Boolean(line && (/\blink(?:\s|:|$)/i.test(line) || /\s->\s/.test(line)));
  }
  const record = packageRecord(state, packageName);
  if (record && record.link === true) return true;
  const resolved = record && record.resolved;
  if (typeof resolved === "string" && /^file:/i.test(resolved)) return true;
  return state.manager.name === "npm" && typeof resolved === "string" && !/^https?:\/\//i.test(resolved);
}


function registryPackagePath(state, packagePath) {
  try {
    const stat = fs.lstatSync(packagePath);
    return !stat.isSymbolicLink() || state.manager.name === "pnpm";
  } catch (_err) {
    return false;
  }
}

function isRegistryManaged(state, packageName, packagePath) {
  return !packageIsLinked(state, packageName) && registryPackagePath(state, packagePath);
}

function stateOwnsInvocation(state, packageName, version, expectedPackagePath = null, expectedLauncherPath = null) {
  if (!managerListsPackage(state, packageName, version)) return null;
  const record = packageRecord(state, packageName);
  let packagePath;
  if (state.manager.name === "pnpm") {
    packagePath = record && record.path;
    const logicalPath = path.join(state.root, ...packageName.split("/"));
    if (packagePath && pathIsAbsolute(state.root) && pathIsAbsolute(packagePath) && fs.existsSync(logicalPath) && samePath(logicalPath, packagePath)) {
      packagePath = logicalPath;
    }
  } else {
    packagePath = path.join(state.root, ...packageName.split("/"));
  }
  if (!packagePath || !pathIsAbsolute(packagePath) || !isRegistryManaged(state, packageName, packagePath)) return null;
  const manifestPath = path.join(packagePath, "package.json");
  let manifest;
  try {
    manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
  } catch (_err) {
    return null;
  }
  if (manifest.name !== packageName || manifest.version !== version) return null;
  if (expectedPackagePath && !samePath(packagePath, expectedPackagePath)) return null;
  for (const commandPath of commandEntryCandidates(state)) {
    const target = entryTarget(state, commandPath);
    if (!expectedLauncherPath || (target && samePath(target, expectedLauncherPath))) {
      return { ...state, packagePath, manifest, commandPath, commandTarget: target };
    }
  }
  return null;
}

function npmPrefixOwnsInvocation(prefix, packageName, expectedPackagePath, expectedLauncherPath) {
  if (!prefix || !pathIsAbsolute(prefix)) return false;
  const globalRoot = process.platform === "win32"
    ? path.join(prefix, "node_modules")
    : path.join(prefix, "lib", "node_modules");
  const packagePath = path.join(globalRoot, ...packageName.split("/"));
  if (!samePath(packagePath, expectedPackagePath)) return false;
  const bin = path.join(prefix, process.platform === "win32" ? "" : "bin");
  const names = process.platform === "win32" ? ["octo-cli.cmd", "octo-cli.exe", "octo-cli"] : ["octo-cli"];
  for (const name of names) {
    const commandPath = path.join(bin, name);
    if (!fs.existsSync(commandPath)) continue;
    const target = entryTarget({ manager: { name: "npm" } }, commandPath);
    if (target && samePath(target, expectedLauncherPath)) return true;
  }
  return false;
}

function inspectManagerCommands(env) {
  return Object.fromEntries(MANAGERS.map((name) => [name, managerCommand(name, env)]));
}

function metadataVersion(state, packageName, cwd, env) {
  let args;
  if (state.manager.name === "npm") args = ["view", "--global", `${packageName}@latest`, "version", "--json", "--prefer-online"];
  else if (state.manager.name === "bun") args = ["info", "--global", `${packageName}@latest`, "version", "--json"];
  else args = ["view", `${packageName}@latest`, "version", "--json"];
  const result = run(state.manager, args, { cwd, env });
  if (!completed(result)) return null;
  const text = result.stdout.trim();
  const parsed = parseJson(text);
  if (typeof parsed === "string") return parsed;
  if (Array.isArray(parsed) && parsed.length === 1 && typeof parsed[0] === "string") return parsed[0];
  if (parsed && typeof parsed.version === "string") return parsed.version;
  return text.replace(/^['"]|['"]$/g, "");
}

function installExact(state, packageName, platformPackage, version, cwd, env) {
  let args;
  if (state.manager.name === "npm") {
    args = ["install", "--global", "--prefix", state.prefix, "--include=optional", `${packageName}@${version}`];
    env = { ...env, npm_config_prefix: state.prefix };
    delete env.NPM_CONFIG_PREFIX;
  } else if (state.manager.name === "bun") {
    // Bun has no inverse of --omit optional; install the exact platform package
    // explicitly as well so the launcher's pinned dependency is always resolvable.
    args = ["install", "--global", `${packageName}@${version}`, `${platformPackage}@${version}`];
  } else {
    args = ["add", "--global", "--config.optional=true", `${packageName}@${version}`];
  }
  return run(state.manager, args, { cwd, env });
}

module.exports = {
  MANAGERS,
  inspectManagerCommands,
  installExact,
  metadataVersion,
  queryLocalState,
  resolveRelativeConfigurationPaths,
  samePath,
  npmPrefixOwnsInvocation,
  stateOwnsInvocation,
};
