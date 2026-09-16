"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const { normalizeVersion, checkVersion, compareVersions, targetName, readConfig, assertManifest, project } = require("../lib");

test("versions follow existing CLI next.N convention and branch mapping", () => {
  assert.equal(normalizeVersion("v1.2.3-next.4"), "1.2.3-next.4");
  assert.equal(checkVersion("test", "1.2.3-next.4"), "1.2.3-next.4");
  assert.equal(checkVersion("main", "1.2.3"), "1.2.3");
  for (const [branch, version] of [["test", "1.2.3"], ["main", "1.2.3-next.1"], ["feature", "1.2.3"], ["test", "1.2.3-dev.1"], ["test", "1.2.3-next.01"], ["main", "01.2.3"]]) {
    assert.throws(() => checkVersion(branch, version));
  }
});
test("version comparison is numeric and respects prereleases", () => {
  assert.equal(compareVersions("1.2.3-next.10", "1.2.3-next.9"), 1);
  assert.equal(compareVersions("1.2.3-next.10", "1.2.3"), -1);
  assert.equal(compareVersions("1.2.3", "1.2.3"), 0);
});
test("Node platform names map to the existing Go matrix", () => {
  assert.equal(targetName("win32", "x64"), "windows/amd64");
  assert.equal(targetName("darwin", "arm64"), "darwin/arm64");
  assert.throws(() => targetName("linux", "ia32"));
});
test("example configuration loads but cannot be used for actual upload", () => {
  const path = require("node:path").join(__dirname, "../config.example.json");
  assert.equal(readConfig(path).cdnOrigin, "https://cdn.example.com");
  assert.throws(() => readConfig(path, true), /example|replace/);
});
test("manifest rejects foreign components, invalid paths and missing platforms", () => {
  assert.throws(() => assertManifest({schemaVersion: 1, component: project.component, branch: "test", version: "1.2.3-next.1", targets: {}}));
});
