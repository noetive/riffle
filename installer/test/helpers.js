"use strict";

const { mkdirSync, mkdtempSync, rmSync, writeFileSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { dirname, join } = require("node:path");

// A throwaway home and workspace. Every test runs against these, never the real
// machine.
function sandbox(t) {
  const root = mkdtempSync(join(tmpdir(), "riffle-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const home = join(root, "home");
  const workspace = join(root, "work");
  mkdirSync(home);
  mkdirSync(workspace);
  return { root, ctx: { home, workspace } };
}

function put(path, text) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, text);
}

// An io that records instead of printing, with no terminal attached.
function capture() {
  const out = [];
  const err = [];
  return { out: (l) => out.push(l), err: (l) => err.push(l), stdin: { isTTY: false }, stdout: {}, lines: out, errors: err };
}

module.exports = { capture, put, sandbox };
