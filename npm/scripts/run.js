#!/usr/bin/env node
"use strict";

// Thin shim: forward all args and stdio to the prebuilt Go binary that
// scripts/install.js placed in ../bin during postinstall.
const path = require("path");
const os = require("os");
const { spawnSync } = require("child_process");

const binName = process.platform === "win32" ? "octo-cli.exe" : "octo-cli";
const bin = path.join(__dirname, "..", "bin", binName);

const res = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });

if (res.error) {
  if (res.error.code === "ENOENT") {
    console.error(
      "[octo-cli] binary not found — the postinstall download may have failed.\n" +
        "[octo-cli] Try reinstalling: npm install -g @mininglamp-oss/octo-cli",
    );
  } else {
    console.error(`[octo-cli] ${res.error.message}`);
  }
  process.exit(1);
}

// If the binary was killed by a signal, re-raise the same signal so callers
// observe the conventional 128+signum exit code (e.g. 130 for SIGINT) instead
// of a generic 1 that hides Ctrl-C from shells and supervisors. Use the
// platform's signal map (os.constants.signals) so signals outside the small
// hand-coded set (e.g. SIGPIPE, SIGUSR1) also get a faithful exit code on
// the fallback path.
if (res.signal) {
  process.kill(process.pid, res.signal);
  const signum = (os.constants && os.constants.signals && os.constants.signals[res.signal]) || 0;
  process.exit(128 + signum);
}

// Propagate the child's exit code.
process.exit(res.status === null ? 1 : res.status);
