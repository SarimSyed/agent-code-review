// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

"use strict";

const assert = require("assert");
const os = require("os");
const { launcherExitCode } = require("./acr");

assert.strictEqual(launcherExitCode({ status: 0 }), 0);
assert.strictEqual(launcherExitCode({ status: 2 }), 2);
assert.strictEqual(launcherExitCode({ status: null, error: new Error("enoent") }), 1);
assert.strictEqual(
  launcherExitCode({ status: null, signal: "SIGTERM" }),
  128 + os.constants.signals.SIGTERM
);
assert.strictEqual(launcherExitCode({ status: null, signal: "SIGFAKE" }) > 0, true);

console.log("acr launcher exit-code tests passed");
