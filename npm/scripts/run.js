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
        "[octo-cli] Try reinstalling: npm install -g @mininglamp-oss/octo-cli",
    );
  } else {
    console.error(`[octo-cli] ${res.error.message}`);
  }
  process.exit(1);
}

// If the binary was killed by a signal, re-raise the same signal so callers
// observe the conventional 128+signum exit code (e.g. 130 for SIGINT) instead
// of a generic 1 that hides Ctrl-C from shells and supervisors.
if (res.signal) {
  process.kill(process.pid, res.signal);
  // process.kill is async w.r.t. the event loop on some platforms; fall back
  // to the conventional exit code so we don't return success by accident.
  const SIGNUMS = { SIGHUP: 1, SIGINT: 2, SIGQUIT: 3, SIGKILL: 9, SIGTERM: 15 };
  process.exit(128 + (SIGNUMS[res.signal] || 0));
}

// Propagate the child's exit code.
process.exit(res.status === null ? 1 : res.status);
