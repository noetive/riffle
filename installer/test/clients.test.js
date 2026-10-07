"use strict";

const assert = require("node:assert/strict");
const { existsSync } = require("node:fs");
const { join } = require("node:path");
const { test } = require("node:test");

const { assertUsableWorkspace, clientIds, clientSpec, isInstalled, stateName } = require("../src/clients");
const { put, sandbox } = require("./helpers");

test("an editor is detected by its config directory and not otherwise", (t) => {
  const { ctx } = sandbox(t);
  assert.deepEqual(clientIds().filter((id) => isInstalled(clientSpec(id), ctx, existsSync)), []);

  put(join(ctx.home, ".cursor", "mcp.json"), "{}");
  put(join(ctx.home, ".kiro", "x"), "");
  assert.deepEqual(
    clientIds().filter((id) => isInstalled(clientSpec(id), ctx, existsSync)),
    ["cursor", "kiro"],
  );
});

test("copilot is detected from the workspace .vscode directory", (t) => {
  const { ctx } = sandbox(t);
  put(join(ctx.workspace, ".vscode", "settings.json"), "{}");
  assert.equal(isInstalled(clientSpec("copilot"), ctx, existsSync), true);
});

test("an unknown client names the supported ones", () => {
  assert.throws(() => clientSpec("emacs"), /supported: .*claude-code/);
});

test("a project-scoped install from the home directory is refused", (t) => {
  const { ctx } = sandbox(t);
  const home = { home: ctx.home, workspace: ctx.home };
  assert.throws(() => assertUsableWorkspace(clientSpec("copilot"), "project", home), /home directory/);
  assert.doesNotThrow(() => assertUsableWorkspace(clientSpec("cursor"), "user", home));
  assert.doesNotThrow(() => assertUsableWorkspace(clientSpec("copilot"), "project", ctx));
});

// The same names riffle's own rule accepts and refuses (internal/daemon).
test("a kept-state name follows riffle's rule", () => {
  for (const bad of ["", "Shop", "../shop", "a/b", "-shop", "_x", "con", "lpt1", "a".repeat(65), "shop.json"]) {
    assert.equal(stateName(bad), false, bad);
  }
  for (const good of ["shop", "a", "work-2", "x_y", "a".repeat(64)]) {
    assert.equal(stateName(good), true, good);
  }
});
