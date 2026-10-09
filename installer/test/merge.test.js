"use strict";

const assert = require("node:assert/strict");
const { existsSync, readFileSync } = require("node:fs");
const { join } = require("node:path");
const { test } = require("node:test");

const { clientSpec, configPath, buildEntry } = require("../src/clients");
const { install, remove, status } = require("../src/installer");
const { put, sandbox } = require("./helpers");

const spec = clientSpec("cursor");
const pathOf = (ctx) => configPath(spec, "user", ctx);
const json = (path) => JSON.parse(readFileSync(path, "utf8"));

test("install creates a missing config with only the riffle entry", (t) => {
  const { ctx } = sandbox(t);
  const result = install(spec, "user", ctx);
  assert.equal(result.changed, true);
  assert.deepEqual(json(pathOf(ctx)), { mcpServers: { riffle: buildEntry(spec) } });
});

test("install leaves other servers and keys untouched and backs up the old file", (t) => {
  const { ctx } = sandbox(t);
  const original = { theme: "dark", mcpServers: { other: { command: "x", args: ["1"] } } };
  put(pathOf(ctx), JSON.stringify(original));

  const result = install(spec, "user", ctx);
  const after = json(pathOf(ctx));
  assert.deepEqual(after.mcpServers.other, original.mcpServers.other);
  assert.equal(after.theme, "dark");
  assert.ok(after.mcpServers.riffle);
  assert.deepEqual(JSON.parse(readFileSync(result.backup, "utf8")), original);
});

test("install keeps comments in a commented config", (t) => {
  const { ctx } = sandbox(t);
  put(pathOf(ctx), '{\n  // my servers\n  "mcpServers": {\n    "other": { "command": "x" },\n  },\n}\n');
  install(spec, "user", ctx);
  assert.match(readFileSync(pathOf(ctx), "utf8"), /\/\/ my servers/);
});

test("install twice changes nothing the second time", (t) => {
  const { ctx } = sandbox(t);
  install(spec, "user", ctx);
  assert.equal(install(spec, "user", ctx).changed, false);
});

test("install leaves a matching entry alone however the editor formatted the file", (t) => {
  const doc = { unrelated: true, mcpServers: { other: { command: "echo" }, riffle: buildEntry(spec) } };
  const saved = {
    "no trailing newline": JSON.stringify(doc, null, 2),
    compact: JSON.stringify(doc),
    "four spaces": `${JSON.stringify(doc, null, 4)}\n`,
    "CRLF line ends": `${JSON.stringify(doc, null, 2).replace(/\n/g, "\r\n")}\r\n`,
  };
  for (const [style, text] of Object.entries(saved)) {
    const { ctx } = sandbox(t);
    put(pathOf(ctx), text);
    assert.equal(install(spec, "user", ctx).changed, false, style);
    assert.equal(readFileSync(pathOf(ctx), "utf8"), text, style);
    assert.equal(existsSync(`${pathOf(ctx)}.riffle.bak`), false, style);
  }
});

test("install refuses a corrupt config and leaves it as it was", (t) => {
  const { ctx } = sandbox(t);
  put(pathOf(ctx), "{ not json");
  assert.throws(() => install(spec, "user", ctx), /not valid JSON/);
  assert.equal(readFileSync(pathOf(ctx), "utf8"), "{ not json");
});

test("dry run reports a diff and writes nothing", (t) => {
  const { ctx } = sandbox(t);
  const result = install(spec, "user", ctx, { dryRun: true });
  assert.equal(result.changed, true);
  assert.match(result.diff, /\+.*riffle/);
  assert.equal(existsSync(pathOf(ctx)), false);
});

test("chrome option lands in the entry env", (t) => {
  const { ctx } = sandbox(t);
  install(spec, "user", ctx, { chrome: "/opt/chrome" });
  assert.equal(json(pathOf(ctx)).mcpServers.riffle.env.RIFFLE_CHROME, "/opt/chrome");
});

test("the entry runs the wrapper with the mcp argument and needs no key", () => {
  const entry = buildEntry(spec);
  assert.deepEqual(entry.args.slice(-2), ["@noetive/riffle", "mcp"]);
  assert.equal(entry.env, undefined);
});

test("on Windows the entry runs npx through cmd, elsewhere directly", (t) => {
  for (const [platform, command, prefix] of [
    ["win32", "cmd", ["/c", "npx"]],
    ["linux", "npx", []],
    ["darwin", "npx", []],
  ]) {
    const { ctx } = sandbox(t);
    install(spec, "user", { ...ctx, platform }, { keepState: "shop" });
    const entry = json(pathOf(ctx)).mcpServers.riffle;
    assert.equal(entry.command, command, platform);
    assert.deepEqual(entry.args, [...prefix, "-y", "@noetive/riffle", "mcp", "-keep-state", "shop"], platform);
  }
});

test("init on Windows replaces an entry that runs npx directly, then leaves it alone", (t) => {
  const { ctx } = sandbox(t);
  const win = { ...ctx, platform: "win32" };
  install(spec, "user", { ...ctx, platform: "linux" });
  assert.equal(install(spec, "user", win).changed, true);
  assert.equal(json(pathOf(ctx)).mcpServers.riffle.command, "cmd");
  assert.equal(install(spec, "user", win).changed, false);
});

test("client extras and top-level key follow the editor", (t) => {
  const { ctx } = sandbox(t);
  const copilot = clientSpec("copilot");
  install(copilot, "project", ctx);
  const doc = json(configPath(copilot, "project", ctx));
  assert.equal(doc.servers.riffle.type, "stdio");
});

test("remove deletes only the riffle entry", (t) => {
  const { ctx } = sandbox(t);
  put(pathOf(ctx), JSON.stringify({ mcpServers: { other: { command: "x" } } }));
  install(spec, "user", ctx);
  assert.equal(remove(spec, "user", ctx).changed, true);
  assert.deepEqual(json(pathOf(ctx)), { mcpServers: { other: { command: "x" } } });
  assert.equal(remove(spec, "user", ctx).changed, false);
});

test("remove dry run keeps the file", (t) => {
  const { ctx } = sandbox(t);
  install(spec, "user", ctx);
  remove(spec, "user", ctx, { dryRun: true });
  assert.equal(status(spec, "user", ctx).configured, "yes");
});

test("status reports configured, unconfigured and unreadable", (t) => {
  const { ctx } = sandbox(t);
  assert.equal(status(spec, "user", ctx).configured, "no");
  install(spec, "user", ctx);
  assert.equal(status(spec, "user", ctx).configured, "yes");
  put(pathOf(ctx), "{ nope");
  const broken = status(spec, "user", ctx);
  assert.equal(broken.configured, "no");
  assert.match(broken.detail, /not valid JSON/);
});

test("user and project scopes of claude-code write different files", (t) => {
  const { ctx } = sandbox(t);
  const cc = clientSpec("claude-code");
  assert.equal(configPath(cc, "user", ctx), join(ctx.home, ".claude.json"));
  assert.equal(configPath(cc, "project", ctx), join(ctx.workspace, ".mcp.json"));
  assert.throws(() => configPath(cc, "nope", ctx), /no scope/);
});

test("keep-state option names the kept session in the entry's arguments", (t) => {
  const { ctx } = sandbox(t);
  install(spec, "user", ctx, { keepState: "shop" });
  assert.deepEqual(json(pathOf(ctx)).mcpServers.riffle.args.slice(-3), ["mcp", "-keep-state", "shop"]);
});
