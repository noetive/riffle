"use strict";

const { homedir } = require("node:os");
const { isAbsolute, join, normalize, resolve } = require("node:path");

// The one key this installer writes. It never touches any other.
const SERVER_NAME = "riffle";
const PACKAGE_NAME = "@noetive/riffle";

// Every supported editor keeps MCP servers in a JSON object keyed by name, so
// one merge routine covers them all. Editors whose config is TOML or YAML
// (Codex, Hermes) are not listed; use the JSON snippet in the README for those.
//
// Paths use ~ for the home directory and ${workspace} for the project.
const CLIENTS = {
  "claude-code": {
    displayName: "Claude Code",
    topLevelKey: "mcpServers",
    detect: ["~/.claude", "~/.claude.json"],
    scopes: {
      user: { path: "~/.claude.json", default: true },
      project: { path: "${workspace}/.mcp.json" },
    },
    skills: { user: "~/.claude/skills", project: "${workspace}/.claude/skills" },
    restartHint: "Start a new Claude Code session. Project-scope servers ask for approval on first use.",
  },
  cursor: {
    displayName: "Cursor",
    topLevelKey: "mcpServers",
    detect: ["~/.cursor"],
    scopes: {
      user: { path: "~/.cursor/mcp.json", default: true },
      project: { path: "${workspace}/.cursor/mcp.json" },
    },
    restartHint: "Reload Cursor to pick up the new server.",
  },
  copilot: {
    displayName: "GitHub Copilot (VS Code)",
    topLevelKey: "servers",
    detect: ["${workspace}/.vscode", "~/.vscode"],
    entryExtras: { type: "stdio" },
    scopes: { project: { path: "${workspace}/.vscode/mcp.json", default: true } },
    restartHint: "Switch Copilot Chat to Agent mode: MCP tools are only available there.",
  },
  kiro: {
    displayName: "Kiro",
    topLevelKey: "mcpServers",
    detect: ["~/.kiro"],
    entryExtras: { disabled: false, autoApprove: [] },
    scopes: {
      user: { path: "~/.kiro/settings/mcp.json", default: true },
      project: { path: "${workspace}/.kiro/settings/mcp.json" },
    },
    restartHint: "Kiro reloads mcp.json on save.",
  },
  antigravity: {
    displayName: "Antigravity",
    topLevelKey: "mcpServers",
    detect: ["~/.gemini"],
    entryExtras: { disabled: false },
    scopes: {
      user: { path: "~/.gemini/config/mcp_config.json", default: true },
      project: { path: "${workspace}/.agents/mcp_config.json" },
    },
    restartHint: "Restart Antigravity to pick up the new server.",
  },
};

const clientIds = () => Object.keys(CLIENTS);

function clientSpec(id) {
  const spec = CLIENTS[id];
  if (!spec) throw new Error(`unknown client ${JSON.stringify(id)}; supported: ${clientIds().join(", ")}`);
  return spec;
}

function defaultScope(spec) {
  const named = Object.entries(spec.scopes).find(([, s]) => s.default);
  return named ? named[0] : Object.keys(spec.scopes)[0];
}

// `ctx` is { home, workspace }, passed explicitly so tests never touch the
// real home directory.
function context(overrides = {}) {
  return { home: overrides.home ?? homedir(), workspace: overrides.workspace ?? process.cwd() };
}

function expandPath(template, ctx) {
  let path = template;
  if (path.startsWith("~/")) path = join(ctx.home, path.slice(2));
  path = path.replace("${workspace}", ctx.workspace);
  // Normalised once so Windows paths do not mix separators downstream.
  return normalize(isAbsolute(path) ? path : resolve(ctx.workspace, path));
}

function configPath(spec, scope, ctx) {
  const declared = spec.scopes[scope];
  if (!declared) {
    throw new Error(`${spec.displayName} has no scope ${JSON.stringify(scope)}; supported: ${Object.keys(spec.scopes).join(", ")}`);
  }
  return expandPath(declared.path, ctx);
}

function skillsPath(spec, scope, ctx) {
  if (!spec.skills) return undefined;
  const template = spec.skills[scope] ?? Object.values(spec.skills)[0];
  return expandPath(template, ctx);
}

const isProjectScoped = (spec, scope) => spec.scopes[scope]?.path.includes("${workspace}") ?? false;

// A project-scoped install run from the home directory writes a file the
// editor never reads as a workspace config, and would report success.
function assertUsableWorkspace(spec, scope, ctx) {
  if (!isProjectScoped(spec, scope)) return;
  if (resolve(ctx.workspace) !== resolve(ctx.home)) return;
  const alternative = Object.keys(spec.scopes).find((s) => !isProjectScoped(spec, s));
  throw new Error(
    `the ${scope} scope for ${spec.displayName} writes into the current directory, and you are in your home directory, ` +
      `where ${spec.displayName} would not read it. ` +
      (alternative ? `Change to your project directory, or use --scope ${alternative}.` : `Change to your project directory and run again.`),
  );
}

function isInstalled(spec, ctx, exists) {
  return spec.detect.some((candidate) => exists(expandPath(candidate, ctx)));
}

// A name riffle accepts for kept state: lower case letters, digits, - and _,
// starting with a letter or digit, and no name Windows reserves. The rule is
// riffle's own; it is checked here too so a bad name fails at init, not later
// when the editor starts the server and shows nobody why.
const RESERVED = /^(con|prn|aux|nul|com[1-9]|lpt[1-9])$/;
function stateName(name) {
  return /^[a-z0-9][a-z0-9_-]{0,63}$/.test(name) && !RESERVED.test(name);
}

// The entry editors run. `npx -y` because an editor spawns the server with no
// terminal, and without -y npx blocks on a prompt nobody can answer. The
// wrapper then execs the native binary with `mcp`.
function buildEntry(spec, options = {}) {
  const entry = { command: "npx", args: ["-y", PACKAGE_NAME, "mcp"], ...(spec.entryExtras ?? {}) };
  if (options.keepState) entry.args.push("-keep-state", options.keepState);
  if (options.chrome) entry.env = { RIFFLE_CHROME: options.chrome };
  return entry;
}

module.exports = {
  PACKAGE_NAME,
  SERVER_NAME,
  assertUsableWorkspace,
  buildEntry,
  clientIds,
  clientSpec,
  configPath,
  context,
  defaultScope,
  isInstalled,
  skillsPath,
  stateName,
};
