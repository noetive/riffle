#!/usr/bin/env node
"use strict";

// Argv router for @noetive/riffle. Editor configuration (init, remove, list,
// doctor, help) is handled here; everything else, `mcp` above all, execs the
// native binary. The pass-through default is load-bearing: every editor entry
// written by `init` runs `npx -y @noetive/riffle mcp`.

const { spawn, spawnSync } = require("node:child_process");

const CONFIG_COMMANDS = new Set(["init", "remove", "list", "doctor", "help"]);

function native() {
  return require("../src/resolveBinary.js").resolveBinary();
}

function main() {
  const argv = process.argv.slice(2);
  const first = argv[0];

  if (first === "--help" || first === "-h") return runCli(["help"]);
  if (first !== undefined && CONFIG_COMMANDS.has(first)) return runCli(argv);
  return exec(argv);
}

function runCli(argv) {
  const { run } = require("../src/cli.js");
  const runNative = (args) => {
    const result = spawnSync(native(), args, { stdio: "inherit" });
    if (result.error) throw result.error;
    return result.status ?? 1;
  };
  run(argv, { runNative }).then(
    (code) => process.exit(code),
    (err) => {
      console.error(`riffle: ${err && err.message ? err.message : err}`);
      process.exit(1);
    },
  );
}

function exec(argv) {
  let binary;
  try {
    binary = native();
  } catch (err) {
    console.error(`riffle: ${err.message}`);
    process.exit(1);
  }

  // stdio is inherited: MCP runs over this process's own stdin and stdout.
  const child = spawn(binary, argv, { stdio: "inherit" });
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) process.on(signal, () => child.kill(signal));
  child.on("error", (err) => {
    console.error(`riffle: could not start ${binary}: ${err.message}`);
    process.exit(1);
  });
  child.on("exit", (code, signal) => process.exit(signal || code === null ? 1 : code));
}

main();
