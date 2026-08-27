#!/usr/bin/env node

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

"use strict";

const { spawnSync } = require("child_process");
const os = require("os");
const { resolveNativeBinary } = require("../scripts/platform");

function launcherExitCode(result) {
  if (result.signal) {
    return 128 + (os.constants.signals[result.signal] ?? 1);
  }
  return result.status ?? (result.error ? 1 : 0);
}

module.exports = { launcherExitCode };

if (require.main === module) {
  const resolved = resolveNativeBinary();
  if (!resolved) {
    console.error("[ERROR] ACR binary not found. Reinstall @agent-code-review/cli.");
    process.exit(1);
  }
  const result = spawnSync(resolved.path, process.argv.slice(2), {
    stdio: "inherit",
    env: process.env,
  });
  process.exit(launcherExitCode(result));
}
