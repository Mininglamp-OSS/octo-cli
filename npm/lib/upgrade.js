"use strict";

const fs = require("node:fs");
const path = require("node:path");
const { createRequire } = require("node:module");
const { spawnSync } = require("node:child_process");
const managers = require("./package-managers");
const semver = require("./semver");

const PACKAGE_NAME = "@mininglamp-oss/octo-cli";
const PLATFORM_PACKAGES = {
  "darwin-arm64": "@mininglamp-oss/octo-cli-darwin-arm64",
  "darwin-x64": "@mininglamp-oss/octo-cli-darwin-x64",
  "linux-arm64": "@mininglamp-oss/octo-cli-linux-arm64",
  "linux-x64": "@mininglamp-oss/octo-cli-linux-x64",
  "win32-arm64": "@mininglamp-oss/octo-cli-win32-arm64",
  "win32-x64": "@mininglamp-oss/octo-cli-win32-x64",
};

class UpgradeFailure extends Error {
  constructor(type, code, message, hint, exitCode) {
    super(message);
    this.result = { type, code, message, hint, exit_code: exitCode };
  }
}

function fail(type, code, message, hint, exitCode) {
  throw new UpgradeFailure(type, code, message, hint, exitCode);
}

function validation(code, message, hint) {
  fail("validation", code, message, hint, 2);
}

function configuration(code, message, hint) {
  fail("config", code, message, hint, 2);
}

function operational(code, message, hint) {
  fail("api_error", code, message, hint, 1);
}

function safeResultError(error) {
  if (error instanceof UpgradeFailure) return error.result;
  return {
    type: "api_error",
    code: "UPGRADE_FAILED",
    message: "The upgrade could not be completed.",
    hint: "Check the installed package manager and try again.",
    exit_code: 1,
  };
}

function writeResult(handoffDir, result) {
  const target = path.join(handoffDir, "result.json");
  fs.writeFileSync(target, `${JSON.stringify(result)}\n`, { mode: 0o600 });
}

function packageManifestAt(file) {
  let directory = path.dirname(file);
  while (true) {
    const candidate = path.join(directory, "package.json");
    try {
      const manifest = JSON.parse(fs.readFileSync(candidate, "utf8"));
      if (manifest.name === PACKAGE_NAME) return { directory, file: candidate, manifest };
    } catch (_err) {
      // Keep walking until the package root is found.
    }
    const parent = path.dirname(directory);
    if (parent === directory) return null;
    directory = parent;
  }
}

function readJson(file) {
  try {
    const value = JSON.parse(fs.readFileSync(file, "utf8"));
    return value && typeof value === "object" && !Array.isArray(value) ? value : null;
  } catch (_err) {
    return null;
  }
}

function packagePathFromResolvedBinary(binaryPath, packageName) {
  let directory = path.dirname(binaryPath);
  while (true) {
    const manifestPath = path.join(directory, "package.json");
    try {
      const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
      if (manifest.name === packageName) return { directory, manifest };
    } catch (_err) {
      // Continue to the package root.
    }
    const parent = path.dirname(directory);
    if (parent === directory) return null;
    directory = parent;
  }
}


function validatePackage(mainPackagePath, launcherPath, binaryPath, buildVersion) {
  const launcherManifestPath = path.join(mainPackagePath, "package.json");
  const mainManifest = readJson(launcherManifestPath);
  if (!mainManifest) {
    configuration("UPGRADE_INVALID_INSTALLATION", "The installed main package metadata is missing or invalid.", "Reinstall octo-cli with npm, Bun, or pnpm.");
  }
  const mainVersion = mainManifest.version;
  const parsedMain = semver.parse(mainVersion);
  if (mainManifest.name !== PACKAGE_NAME || !parsedMain || parsedMain.prerelease.length !== 0) {
    configuration("UPGRADE_INVALID_INSTALLATION", "The installed octo-cli package is not a stable release.", "Reinstall a stable npm, Bun, or pnpm release.");
  }
  const normalizedBuildVersion = semver.normalizeBuildVersion(buildVersion);
  const parsedBuild = semver.parse(normalizedBuildVersion);
  if (!parsedBuild || parsedBuild.prerelease.length !== 0 || normalizedBuildVersion !== mainVersion) {
    validation("UPGRADE_VERSION_MISMATCH", "The running octo-cli binary does not match its installed package version.", "Reinstall octo-cli through the package manager that owns this command.");
  }

  const platformKey = `${process.platform}-${process.arch}`;
  const platformPackage = PLATFORM_PACKAGES[platformKey];
  if (!platformPackage) {
    validation("UPGRADE_UNSUPPORTED_PLATFORM", `No stable octo-cli binary is published for ${platformKey}.`, "Install a supported platform release.");
  }
  const dependencies = mainManifest.optionalDependencies || {};
  if (dependencies[platformPackage] !== mainVersion) {
    validation("UPGRADE_PLATFORM_DEPENDENCY_MISMATCH", "The installed platform dependency is not pinned to the main package version.", "Reinstall octo-cli with its matching platform package.");
  }

  const binEntry = typeof mainManifest.bin === "string" ? mainManifest.bin : mainManifest.bin && mainManifest.bin["octo-cli"];
  if (typeof binEntry !== "string" || !managers.samePath(path.resolve(mainPackagePath, binEntry), launcherPath)) {
    validation("UPGRADE_COMMAND_MISMATCH", "The invoked launcher is not the installed octo-cli package command.", "Run the globally installed octo-cli command.");
  }

  const requireFromPackage = createRequire(launcherManifestPath);
  const binaryName = process.platform === "win32" ? "octo-cli.exe" : "octo-cli";
  let resolvedDependencyBinary;
  try {
    resolvedDependencyBinary = requireFromPackage.resolve(`${platformPackage}/bin/${binaryName}`);
  } catch (_err) {
    validation("UPGRADE_PLATFORM_DEPENDENCY_MISSING", "The matching platform binary is not installed.", "Reinstall octo-cli with optional dependencies enabled.");
  }
  let canonicalBinary;
  let canonicalResolvedDependency;
  try {
    canonicalBinary = fs.realpathSync(binaryPath);
    canonicalResolvedDependency = fs.realpathSync(resolvedDependencyBinary);
  } catch (_err) {
    validation("UPGRADE_PLATFORM_DEPENDENCY_MISSING", "The matching platform binary is not installed.", "Reinstall octo-cli with optional dependencies enabled.");
  }
  if (!managers.samePath(canonicalBinary, canonicalResolvedDependency)) {
    validation("UPGRADE_PLATFORM_BINARY_MISMATCH", "The running executable is not the platform binary pinned by this package.", "Reinstall octo-cli with its matching platform package.");
  }
  const platformInstall = packagePathFromResolvedBinary(canonicalResolvedDependency, platformPackage);
  if (!platformInstall || platformInstall.manifest.version !== mainVersion) {
    validation("UPGRADE_PLATFORM_VERSION_MISMATCH", "The installed platform binary version does not match octo-cli.", "Reinstall octo-cli with its matching platform package.");
  }
  return { mainVersion, parsedMain, platformPackage };
}

function managerState(name, command, context) {
  if (!command) return null;
  const local = managers.queryLocalState(command, { cwd: context.cwd, env: context.env });
  if (!local) return null;
  const owned = managers.stateOwnsInvocation(
    local,
    PACKAGE_NAME,
    context.current.mainVersion,
    context.current.mainPackagePath,
    context.current.launcherPath,
  );
  return owned ? { ...owned, managerName: name, environment: context.env } : null;
}

function findOwner(context) {
  const commands = managers.inspectManagerCommands(context.env);
  const owners = [];
  for (const name of managers.MANAGERS) {
    const owner = managerState(name, commands[name], context);
    if (owner) owners.push(owner);
  }
  if (owners.length === 0) {
    const prefix = context.env.npm_config_prefix || context.env.NPM_CONFIG_PREFIX;
    if (!commands.npm && managers.npmPrefixOwnsInvocation(
      prefix,
      PACKAGE_NAME,
      context.current.mainPackagePath,
      context.current.launcherPath,
    )) {
      operational("UPGRADE_MANAGER_UNAVAILABLE", "The owning npm package manager is not available.", "Restore npm on PATH and retry the upgrade.");
    }
    validation("UPGRADE_UNSUPPORTED_INSTALLATION", "This octo-cli installation is not owned by a supported global package manager.", "Use the npm, Bun, or pnpm global launcher that installed octo-cli.");
  }
  if (owners.length > 1) {
    validation("UPGRADE_AMBIGUOUS_INSTALLATION", "More than one package manager claims this octo-cli command.", "Remove the conflicting global installation and retry with one manager.");
  }
  return owners[0];
}

function packageVersionFromMetadata(state, context) {
  const raw = managers.metadataVersion(state, PACKAGE_NAME, context.cwd, state.environment);
  if (!raw) operational("UPGRADE_REGISTRY_FAILED", "The owning package manager could not read the stable release metadata.", "Check the configured registry and authentication, then retry.");
  const parsed = semver.parse(raw);
  if (!parsed || parsed.prerelease.length !== 0) {
    configuration("UPGRADE_INVALID_TARGET_VERSION", "The configured registry's latest tag is not a stable SemVer release.", "Publish a stable SemVer release to the latest tag.");
  }
  return { version: raw, parsed };
}

function ensureStillOwned(owner, context) {
  const local = managers.queryLocalState(owner.manager, { cwd: context.cwd, env: owner.environment });
  if (!local) operational("UPGRADE_MANAGER_UNAVAILABLE", "The owning package manager is no longer available.", "Restore the package manager and retry the upgrade.");
  const current = managers.stateOwnsInvocation(local, PACKAGE_NAME, context.current.mainVersion, context.current.mainPackagePath, context.current.launcherPath);
  if (!current || !sameLogicalPath(current.commandPath, owner.commandPath)) {
    validation("UPGRADE_OWNERSHIP_CHANGED", "The owning global installation changed before the update could start.", "Retry the upgrade from the installed global command.");
  }
}

function sameLogicalPath(left, right) {
  if (process.platform === "win32") return path.resolve(left).toLowerCase() === path.resolve(right).toLowerCase();
  return path.resolve(left) === path.resolve(right);
}

function stableInstallVersion(state, targetVersion, context) {
  const target = managers.installExact(state, PACKAGE_NAME, context.current.platformPackage, targetVersion, context.cwd, state.environment);
  if (target.status !== 0 || target.error || target.signal) {
    operational("UPGRADE_INSTALL_FAILED", "The owning package manager could not install the selected stable release.", "Check package-manager permissions and registry access, then retry.");
  }
}

function readGoVersion(binaryPath, cwd, env) {
  const cleanEnvironment = { ...env };
  delete cleanEnvironment.OCTO_CLI_UPGRADE_HANDOFF;
  delete cleanEnvironment.OCTO_CLI_UPGRADE_REPORT;
  let result;
  try {
    result = spawnSync(binaryPath, ["--format=json", "version"], {
      cwd,
      env: cleanEnvironment,
      encoding: "utf8",
      windowsHide: true,
      maxBuffer: 1024 * 1024,
    });
  } catch (_err) {
    return null;
  }
  if (result.status !== 0 || result.error || result.signal) return null;
  try {
    const envelope = JSON.parse(result.stdout);
    const version = envelope && envelope.data && envelope.data.version;
    return typeof version === "string" ? semver.normalizeBuildVersion(version) : null;
  } catch (_err) {
    return null;
  }
}


function verifyInstalled(owner, targetVersion, context) {
  const local = managers.queryLocalState(owner.manager, { cwd: context.cwd, env: owner.environment });
  if (!local) operational("UPGRADE_VERIFY_FAILED", "The updated installation could not be located through its package manager.", "Reinstall the selected release through the owning package manager.");
  const installed = managers.stateOwnsInvocation(local, PACKAGE_NAME, targetVersion, null, null);
  if (!installed || !sameLogicalPath(installed.commandPath, owner.commandPath)) {
    operational("UPGRADE_VERIFY_FAILED", "The package manager did not leave the selected release at the original global command.", "Reinstall the selected release through the owning package manager.");
  }
  const packagePath = installed.packagePath;
  const manifestPath = path.join(packagePath, "package.json");
  const manifest = readJson(manifestPath);
  if (!manifest) {
    operational("UPGRADE_VERIFY_FAILED", "The updated package metadata is missing or invalid.", "Reinstall the selected release through the owning package manager.");
  }
  if (manifest.version !== targetVersion || manifest.name !== PACKAGE_NAME) {
    operational("UPGRADE_VERIFY_FAILED", "The updated package version does not match the selected release.", "Reinstall the selected release through the owning package manager.");
  }
  if (!manifest.optionalDependencies || manifest.optionalDependencies[context.current.platformPackage] !== targetVersion) {
    operational("UPGRADE_VERIFY_FAILED", "The updated package does not pin its matching platform dependency.", "Reinstall the selected release with its matching platform package.");
  }
  let binaryPath;
  try {
    // Resolve from the physical package, as Node does for the launcher. pnpm's
    // logical global entry does not expose its virtual-store dependency siblings.
    const physicalManifestPath = process.platform === "win32"
      ? fs.realpathSync.native(manifestPath)
      : fs.realpathSync(manifestPath);
    binaryPath = createRequire(physicalManifestPath).resolve(
      `${context.current.platformPackage}/bin/${process.platform === "win32" ? "octo-cli.exe" : "octo-cli"}`,
    );
  } catch (_err) {
    operational("UPGRADE_VERIFY_FAILED", "The updated package is missing its matching platform binary.", "Reinstall the selected release with optional dependencies enabled.");
  }
  const platformInstall = packagePathFromResolvedBinary(binaryPath, context.current.platformPackage);
  if (!platformInstall || platformInstall.manifest.version !== targetVersion) {
    operational("UPGRADE_VERIFY_FAILED", "The updated platform binary does not match the selected release.", "Reinstall the selected release with its matching platform package.");
  }
  const binEntry = typeof manifest.bin === "string" ? manifest.bin : manifest.bin && manifest.bin["octo-cli"];
  if (typeof binEntry !== "string" || !managers.samePath(path.join(packagePath, binEntry), installed.commandTarget)) {
    operational("UPGRADE_VERIFY_FAILED", "The fixed global command no longer targets the updated package.", "Reinstall the selected release through the owning package manager.");
  }
  const binaryVersion = readGoVersion(binaryPath, context.cwd, context.env);
  if (!binaryVersion || binaryVersion !== targetVersion) {
    operational("UPGRADE_VERIFY_FAILED", "The updated executable could not be verified at the selected version.", "Reinstall the selected release through the owning package manager.");
  }
  return { packagePath, binaryPath };
}

function successData(owner, context, targetVersion) {
  return {
    manager: owner.managerName,
    previous_version: context.current.mainVersion,
    target_version: targetVersion,
    current_version: targetVersion,
    updated: true,
  };
}

function perform(options) {
  const { launcherPath, binaryPath, request, cwd: originalCwd, env: inheritedEnv } = options;
  const env = managers.resolveRelativeConfigurationPaths({ ...inheritedEnv }, originalCwd);
  const cleanEnv = { ...env };
  delete cleanEnv.OCTO_CLI_UPGRADE_HANDOFF;
  delete cleanEnv.OCTO_CLI_UPGRADE_REPORT;
  cleanEnv.COREPACK_ENABLE_NETWORK = "0";
  cleanEnv.COREPACK_DEFAULT_TO_LATEST = "0";
  const canonicalLauncher = fs.realpathSync(launcherPath);
  const launcherPackage = packageManifestAt(canonicalLauncher);
  if (!launcherPackage) {
    validation("UPGRADE_UNSUPPORTED_INSTALLATION", "The invoked octo-cli launcher is not inside an installed package.", "Use the globally installed npm, Bun, or pnpm command.");
  }
  const mainPackagePath = launcherPackage.directory;
  const current = validatePackage(mainPackagePath, canonicalLauncher, binaryPath, request.version);
  const context = {
    cwd: options.handoffDir,
    env: cleanEnv,
    current: { ...current, mainPackagePath, launcherPath: canonicalLauncher },
  };
  const owner = findOwner(context);

  if (request.dry_run) {
    return {
      dry_run: true,
      package: PACKAGE_NAME,
      manager: owner.managerName,
      current_version: current.mainVersion,
      target: "latest",
      package_path: owner.packagePath,
      command_path: owner.commandPath,
    };
  }

  const target = packageVersionFromMetadata(owner, context);
  if (semver.compare(target.parsed, current.parsedMain) <= 0) {
    return {
      manager: owner.managerName,
      previous_version: current.mainVersion,
      target_version: target.version,
      current_version: current.mainVersion,
      updated: false,
    };
  }

  ensureStillOwned(owner, context);
  stableInstallVersion(owner, target.version, context);
  verifyInstalled(owner, target.version, context);
  return successData(owner, context, target.version);
}

function runUpgrade(options) {
  let result;
  try {
    result = { data: perform(options) };
  } catch (error) {
    result = { error: safeResultError(error) };
  }
  writeResult(options.handoffDir, result);
  return result;
}

module.exports = { runUpgrade };
