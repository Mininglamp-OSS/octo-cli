#!/usr/bin/env node
"use strict";

// Thin shim: forward all args and stdio to the prebuilt Go binary that
// scripts/install.js placed in ../bin during postinstall.
const path = require("path");
const { spawnSync } = require("child_process");

const binName = process.platform === "win32" ? "octo-cli.exe" : "octo-cli";
const bin = path.join(__dirname, "..", "bin", binName);

const res = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });

if (res.error) {
  if (res.error.code === "ENOENT") {
    console.error(
      "[octo-cli] binary not found — the postinstall download may have failed.\n" +
        "[octo-cli] Try reinstalling: npm install -g @mininglamp-oss/octo-cli"
    );
  } else {
    console.error(`[octo-cli] ${res.error.message}`);
  }
  process.exit(1);
}

// Propagate the child's exit code; if killed by a signal, use 1.
process.exit(res.status === null ? 1 : res.status);
