"use strict";

const SEMVER = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;

function parse(version) {
  if (typeof version !== "string") return null;
  const match = SEMVER.exec(version);
  if (!match) return null;
  const prerelease = match[4] ? match[4].split(".") : [];
  if (prerelease.some((part) => /^\d+$/.test(part) && part.length > 1 && part[0] === "0")) return null;
  return {
    major: BigInt(match[1]),
    minor: BigInt(match[2]),
    patch: BigInt(match[3]),
    prerelease,
  };
}

function compare(left, right) {
  for (const key of ["major", "minor", "patch"]) {
    if (left[key] !== right[key]) return left[key] < right[key] ? -1 : 1;
  }
  return 0;
}

function normalizeBuildVersion(version) {
  return version.startsWith("v") ? version.slice(1) : version;
}

module.exports = { compare, normalizeBuildVersion, parse };
