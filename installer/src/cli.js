"use strict";

const { existsSync } = require("node:fs");
const readline = require("node:readline/promises");

const { assertUsableWorkspace, clientIds, clientSpec, context, defaultScope, isInstalled, stateName } = require("./clients");
const { bundledSkills, install, installSkills, remove, removeSkills, status } = require("./installer");

const HELP = `riffle: a browser your agent can read

Usage
  npx @noetive/riffle init [--client ID[,ID]] [--scope NAME] [--chrome PATH] [--keep-state NAME] [--yes] [--dry-run]
  npx @noetive/riffle remove --client ID [--scope NAME] [--dry-run]
  npx @noetive/riffle list
  npx @noetive/riffle doctor

Any other command (mcp, run, view, ...) runs the riffle binary.

Clients: ${clientIds().join(", ")}

  --client      editors to configure; omit to configure every editor found
  --scope       which config file: user or project (default depends on the editor)
  --chrome      write RIFFLE_CHROME into the entry to point at a browser
  --keep-state  keep the agent signed in between runs, under NAME
  --yes         do not ask for confirmation
  --dry-run     show what would change and write nothing
`;

const FLAGS_WITH_VALUE = new Set(["client", "scope", "chrome", "keep-state"]);
const BOOLEAN_FLAGS = new Set(["yes", "dry-run", "help"]);

function parseArgs(argv) {
  const flags = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "-y") {
      flags.yes = true;
      continue;
    }
    if (!arg.startsWith("--")) throw new Error(`unexpected argument ${JSON.stringify(arg)}`);
    const [name, inline] = arg.slice(2).split(/=(.*)/s, 2);
    if (BOOLEAN_FLAGS.has(name)) {
      flags[name] = true;
    } else if (FLAGS_WITH_VALUE.has(name)) {
      const value = inline ?? argv[++i];
      if (value === undefined || value.startsWith("--")) throw new Error(`--${name} needs a value`);
      flags[name] = value;
    } else {
      throw new Error(`unknown flag --${name}`);
    }
  }
  return flags;
}

// The editors a command applies to: those named, else those detected.
function pickClients(flags, ctx) {
  if (flags.client) return flags.client.split(",").map((id) => id.trim()).filter(Boolean);
  return clientIds().filter((id) => isInstalled(clientSpec(id), ctx, existsSync));
}

async function confirm(question, io) {
  if (!io.stdin.isTTY) return false;
  const rl = readline.createInterface({ input: io.stdin, output: io.stdout });
  try {
    return /^(|y|yes)$/i.test((await rl.question(`${question} [Y/n] `)).trim());
  } finally {
    rl.close();
  }
}

async function init(flags, io, ctx) {
  const keepState = flags["keep-state"];
  if (keepState !== undefined && !stateName(keepState)) {
    io.err(`--keep-state ${JSON.stringify(keepState)}: use at most 64 lower case letters, digits, - and _, starting with a letter or digit`);
    return 1;
  }
  const ids = pickClients(flags, ctx);
  if (ids.length === 0) {
    io.err(`no supported editor found. Name one with --client: ${clientIds().join(", ")}`);
    return 1;
  }

  // Detection guesses at what the user wants, so it asks; an explicit --client
  // is already the answer.
  if (!flags.client && !flags.yes && !flags["dry-run"]) {
    if (!io.stdin.isTTY) {
      io.err(`found ${ids.join(", ")}. Re-run with --yes to configure them, or name one with --client.`);
      return 1;
    }
    if (!(await confirm(`Add riffle to ${ids.map((id) => clientSpec(id).displayName).join(", ")}?`, io))) {
      io.out("nothing changed");
      return 0;
    }
  }

  const skills = bundledSkills();
  let failed = false;
  for (const id of ids) {
    try {
      const spec = clientSpec(id);
      const scope = flags.scope ?? defaultScope(spec);
      assertUsableWorkspace(spec, scope, ctx);
      const options = { chrome: flags.chrome, keepState, dryRun: flags["dry-run"] };

      const result = install(spec, scope, ctx, options);
      io.out(`${spec.displayName}: ${result.changed ? (options.dryRun ? "would update" : "updated") : "already configured"} ${result.target}`);
      if (result.diff) io.out(result.diff);
      if (result.backup) io.out(`  previous file saved to ${result.backup}`);

      const skillResult = installSkills(spec, scope, ctx, skills, options);
      if (skillResult.written.length > 0) io.out(`  ${options.dryRun ? "would write" : "wrote"} skill: ${skillResult.written.join(", ")}`);
      if (result.changed && !options.dryRun) io.out(`  ${spec.restartHint}`);
    } catch (err) {
      failed = true;
      io.err(`${id}: ${err.message}`);
    }
  }
  return failed ? 1 : 0;
}

async function removeCommand(flags, io, ctx) {
  if (!flags.client) {
    io.err("remove needs --client");
    return 1;
  }
  const skills = bundledSkills();
  let failed = false;
  for (const id of pickClients(flags, ctx)) {
    try {
      const spec = clientSpec(id);
      const scope = flags.scope ?? defaultScope(spec);
      const options = { dryRun: flags["dry-run"] };
      const result = remove(spec, scope, ctx, options);
      io.out(`${spec.displayName}: ${result.changed ? (options.dryRun ? "would remove" : "removed") : "no riffle entry in"} ${result.target}`);
      if (result.diff) io.out(result.diff);
      const skillResult = removeSkills(spec, scope, ctx, skills, options);
      for (const dir of skillResult.removed) io.out(`  ${options.dryRun ? "would remove" : "removed"} skill ${dir}`);
    } catch (err) {
      failed = true;
      io.err(`${id}: ${err.message}`);
    }
  }
  return failed ? 1 : 0;
}

// Every editor and scope, and whether it holds the riffle entry. Returns the
// number of configured entries.
function report(io, ctx) {
  let configured = 0;
  for (const id of clientIds()) {
    const spec = clientSpec(id);
    for (const scope of Object.keys(spec.scopes)) {
      const s = status(spec, scope, ctx);
      if (s.configured === "yes") configured += 1;
      else if (!s.installed) continue;
      const label = s.configured === "yes" ? "configured" : "not configured";
      io.out(`${spec.displayName} (${scope}): ${label}  ${s.target}${s.detail ? `  ${s.detail}` : ""}`);
    }
  }
  return configured;
}

function list(io, ctx) {
  report(io, ctx);
  return 0;
}

// Runs the native `riffle doctor` (browser found, starts, scripts run), then
// lists which editors hold the entry. Only the native check sets the exit code.
function doctor(io, ctx, runNative) {
  let code = 0;
  try {
    code = runNative(["doctor"]);
  } catch (err) {
    io.err(err.message);
    code = 1;
  }
  io.out("");
  if (report(io, ctx) === 0) {
    io.out("riffle is not configured in any editor. Run: npx @noetive/riffle init");
  }
  return code;
}

// `io` and `runNative` are injectable so tests drive the CLI with no terminal
// and no binary.
async function run(argv, deps = {}) {
  const io = deps.io ?? {
    out: (line) => console.log(line),
    err: (line) => console.error(`riffle: ${line}`),
    stdin: process.stdin,
    stdout: process.stdout,
  };
  const ctx = deps.ctx ?? context();
  const [command, ...rest] = argv;

  try {
    const flags = parseArgs(rest);
    if (flags.help || command === undefined || command === "help") {
      io.out(HELP);
      return 0;
    }
    switch (command) {
      case "init":
        return await init(flags, io, ctx);
      case "remove":
        return await removeCommand(flags, io, ctx);
      case "list":
        return list(io, ctx);
      case "doctor":
        return doctor(io, ctx, deps.runNative);
      default:
        io.err(`unknown command ${JSON.stringify(command)}\n${HELP}`);
        return 2;
    }
  } catch (err) {
    io.err(err.message);
    return 1;
  }
}

module.exports = { parseArgs, run };
