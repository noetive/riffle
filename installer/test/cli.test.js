"use strict";

const assert = require("node:assert/strict");
const { existsSync, readFileSync } = require("node:fs");
const { join } = require("node:path");
const { test } = require("node:test");

const { parseArgs, run } = require("../src/cli");
const { capture, put, sandbox } = require("./helpers");

test("init --client claude-code writes the entry and the skill", async (t) => {
  const { ctx } = sandbox(t);
  const io = capture();
  assert.equal(await run(["init", "--client", "claude-code", "--scope", "project"], { io, ctx: { ...ctx, platform: "linux" } }), 0);

  const doc = JSON.parse(readFileSync(join(ctx.workspace, ".mcp.json"), "utf8"));
  assert.deepEqual(doc.mcpServers.riffle.args, ["-y", "@noetive/riffle", "mcp"]);
  const skill = readFileSync(join(ctx.workspace, ".claude", "skills", "riffle", "SKILL.md"), "utf8");
  assert.match(skill, /^---\nname: riffle\ndescription: .+\n---/);
});

test("init --dry-run writes nothing anywhere", async (t) => {
  const { ctx } = sandbox(t);
  const io = capture();
  assert.equal(await run(["init", "--client", "claude-code", "--dry-run"], { io, ctx }), 0);
  assert.equal(existsSync(join(ctx.home, ".claude.json")), false);
  assert.equal(existsSync(join(ctx.home, ".claude")), false);
  assert.match(io.lines.join("\n"), /would update/);
});

test("init without --client and without a terminal asks for --yes", async (t) => {
  const { ctx } = sandbox(t);
  put(join(ctx.home, ".cursor", "mcp.json"), "{}");
  const io = capture();
  assert.equal(await run(["init"], { io, ctx }), 1);
  assert.match(io.errors.join("\n"), /--yes/);
  assert.equal(readFileSync(join(ctx.home, ".cursor", "mcp.json"), "utf8"), "{}");
});

test("init --yes configures every detected editor", async (t) => {
  const { ctx } = sandbox(t);
  put(join(ctx.home, ".cursor", "mcp.json"), "{}");
  put(join(ctx.home, ".kiro", "x"), "");
  const io = capture();
  assert.equal(await run(["init", "--yes"], { io, ctx }), 0);
  assert.match(readFileSync(join(ctx.home, ".cursor", "mcp.json"), "utf8"), /riffle/);
  assert.match(readFileSync(join(ctx.home, ".kiro", "settings", "mcp.json"), "utf8"), /riffle/);
});

test("init with nothing detected says how to name an editor", async (t) => {
  const { ctx } = sandbox(t);
  const io = capture();
  assert.equal(await run(["init", "--yes"], { io, ctx }), 1);
  assert.match(io.errors.join("\n"), /--client/);
});

test("remove takes the entry and the skill away again", async (t) => {
  const { ctx } = sandbox(t);
  const io = capture();
  await run(["init", "--client", "claude-code", "--scope", "project"], { io, ctx });
  assert.equal(await run(["remove", "--client", "claude-code", "--scope", "project"], { io, ctx }), 0);
  assert.equal(existsSync(join(ctx.workspace, ".claude", "skills", "riffle")), false);
  assert.equal(JSON.parse(readFileSync(join(ctx.workspace, ".mcp.json"), "utf8")).mcpServers.riffle, undefined);
});

test("doctor runs the native check and lists configured editors", async (t) => {
  const { ctx } = sandbox(t);
  const io = capture();
  await run(["init", "--client", "cursor"], { io, ctx });

  const calls = [];
  const out = capture();
  const code = await run(["doctor"], {
    io: out,
    ctx,
    runNative: (args) => {
      calls.push(args);
      return 0;
    },
  });
  assert.equal(code, 0);
  assert.deepEqual(calls, [["doctor"]]);
  assert.match(out.lines.join("\n"), /Cursor \(user\): configured/);
});

test("doctor passes on a failing native check", async (t) => {
  const { ctx } = sandbox(t);
  const code = await run(["doctor"], { io: capture(), ctx, runNative: () => 3 });
  assert.equal(code, 3);
});

test("doctor reports a missing binary rather than throwing", async (t) => {
  const { ctx } = sandbox(t);
  const io = capture();
  const code = await run(["doctor"], {
    io,
    ctx,
    runNative: () => {
      throw new Error("could not find the riffle binary");
    },
  });
  assert.equal(code, 1);
  assert.match(io.errors.join("\n"), /could not find/);
});

test("bad flags are rejected", () => {
  assert.throws(() => parseArgs(["--nope"]), /unknown flag/);
  assert.throws(() => parseArgs(["--client"]), /needs a value/);
  assert.deepEqual(parseArgs(["--client=cursor", "-y"]), { client: "cursor", yes: true });
});

test("init --keep-state writes an entry that keeps the agent signed in", async (t) => {
  const { ctx } = sandbox(t);
  const io = capture();
  assert.equal(
    await run(["init", "--client", "claude-code", "--scope", "project", "--keep-state", "shop"], {
      io,
      ctx: { ...ctx, platform: "linux" },
    }),
    0,
  );
  const doc = JSON.parse(readFileSync(join(ctx.workspace, ".mcp.json"), "utf8"));
  assert.deepEqual(doc.mcpServers.riffle.args, ["-y", "@noetive/riffle", "mcp", "-keep-state", "shop"]);
});

test("init --keep-state with a name riffle would refuse writes nothing", async (t) => {
  const { ctx } = sandbox(t);
  const io = capture();
  assert.equal(await run(["init", "--client", "claude-code", "--scope", "project", "--keep-state", "../Shop"], { io, ctx }), 1);
  assert.match(io.errors.join("\n"), /--keep-state "..\/Shop": use at most 64 lower case letters/);
  assert.equal(existsSync(join(ctx.workspace, ".mcp.json")), false);
});
