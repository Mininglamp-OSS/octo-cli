#!/usr/bin/env node
"use strict";
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const {project, verifyDist, parseArgs} = require("./lib");
const {install} = require("./install");
async function smokeDist(dir) {
  const manifest = verifyDist(dir);
  const temp = fs.mkdtempSync(path.join(os.tmpdir(), `${project.binary}-smoke-`));
  try {
    const root = `/${project.component}/releases/${manifest.version}/`;
    const fetcher = async url => {
      const parsed = new URL(url);
      if (parsed.origin !== "https://release-validation.invalid" || !parsed.pathname.startsWith(root)) throw new Error("Smoke test attempted an unexpected URL");
      const file = parsed.pathname.slice(root.length);
      if (file.includes("/") || file.includes("..")) throw new Error("Invalid smoke-test file");
      return new Response(fs.readFileSync(path.join(dir, file)), {status: 200});
    };
    await install({component: project.component, binary: project.binary, branch: manifest.branch, baseUrl: "https://release-validation.invalid/"},
      ["--version", manifest.version, "--prefix", temp], fetcher);
    console.log("Host installation smoke test passed; temporary installation removed on exit.");
  } finally { fs.rmSync(temp, {recursive: true, force: true}); }
}
async function main() {
  const args = parseArgs(process.argv.slice(2), ["--dist"], ["--smoke", "--help"]);
  if (args["--help"]) { console.log("node scripts/release/verify.js --dist release-dist/<version> [--smoke]\n--smoke installs the host binary into a temporary directory; no network or cloud credentials."); return; }
  if (!args["--dist"]) throw new Error("--dist is required");
  const dir = path.resolve(args["--dist"]);
  const manifest = verifyDist(dir);
  if (args["--smoke"]) await smokeDist(dir);
  console.log(`Verified ${manifest.component} ${manifest.version}: 6 archive hashes and manifest`);
}
module.exports = {smokeDist};
if (require.main === module) main().catch(e => { console.error(e.message); process.exitCode = 1; });
