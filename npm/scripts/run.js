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

// If the binary was killed by a signal, propagate it so callers observe the
// conventional 128+signum exit code instead of a generic 1 that hides
// Ctrl-C / kill from shells and supervisors. Two cases, handled in order:
//
//   1. Terminating signals (SIGINT, SIGTERM, SIGQUIT, SIGHUP, ...) —
//      `process.kill(self, sig)` re-raises the signal, the Node runtime
//      handles it as it would for a direct signal: it terminates the
//      process and the shell sees 128+signum (e.g. 130 for SIGINT). The
//      `process.exit(...)` line below is unreachable on this path.
//
//   2. Default-ignored or default-stopping signals (SIGPIPE, SIGUSR1/2,
//      SIGCHLD, ...) — Node's default disposition is to ignore them, so
//      `process.kill(self, sig)` returns and the script keeps running.
//      In that case the explicit `process.exit(128 + signum)` is what
//      sets the exit code (e.g. 141 for SIGPIPE) — without it the shim
//      would exit 0 even though the child died from a signal.
//
// os.constants.signals gives the platform's real signal numbers so the
// fallback isn't limited to a hand-coded set.
if (res.signal) {
  process.kill(process.pid, res.signal);
  const signum = (os.constants && os.constants.signals && os.constants.signals[res.signal]) || 0;
  process.exit(128 + signum);
}

// Propagate the child's exit code.
process.exit(res.status === null ? 1 : res.status);
